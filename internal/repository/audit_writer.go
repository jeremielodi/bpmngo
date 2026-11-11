package repository

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Audit rows are written in batches. An instance writes about ten of them,
// each one used to be its own transaction (BEGIN, INSERT, COMMIT): under
// load the busiest writes of the engine. Started (StartAuditWriter),
// CreateAuditLog queues the row, and a background writer inserts what is
// queued every auditFlushEvery, or as soon as auditFlushAt rows wait, in
// one statement and one transaction.
//
// Durability is the same as before: audit rows were already committed with
// synchronous_commit off, so a crash could lose the last few milliseconds
// of them; it can now lose what was queued in the last auditFlushEvery.
// Engine state is never concerned, it is written by its own transactions.
//
// Every reader of audit_logs calls FlushAuditLogs first, so it sees every
// row logged before it (archiving, token history, step back, audit routes),
// by any BpmnGo instance: each writer records in audit_writer_marks, at each
// batch, the Postgres time until which everything it had queued is written,
// and a reader waits until every live writer is past the time it started
// reading (at most a batch interval, bounded by auditBarrierWait).

const (
	auditFlushEvery = 50 * time.Millisecond
	auditFlushAt    = 500
	// A writer whose mark is older than this is gone (stopped, crashed):
	// readers don't wait for it.
	auditWriterLiveFor = 2 * time.Second
	auditBarrierWait   = time.Second
)

type auditWriter struct {
	db      *sqlx.DB
	mu      sync.Mutex // the queue
	queue   []*models.AuditLog
	full    chan struct{}
	writeMu sync.Mutex // one batch written at a time, in order

	node     int64     // this writer's mark row (the engine node)
	nextMark time.Time // Postgres time read before the next batch is taken
	marksOff bool      // audit_writer_marks unusable (migration 022 missing)
}

var (
	auditWriterMu sync.RWMutex
	auditBatches  *auditWriter
)

// StartAuditWriter batches audit rows until ctx is done, then writes what
// is left.
func StartAuditWriter(ctx context.Context, db *sqlx.DB) {
	w := &auditWriter{db: db, full: make(chan struct{}, 1), node: ids.Node()}
	if err := db.Get(&w.nextMark, `SELECT clock_timestamp()`); err != nil {
		log.Printf("audit: %v", err)
	}
	auditWriterMu.Lock()
	auditBatches = w
	auditWriterMu.Unlock()
	go func() {
		ticker := time.NewTicker(auditFlushEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				w.flush()
				return
			case <-ticker.C:
			case <-w.full:
			}
			w.flush()
		}
	}()
}

// FlushAuditLogs writes every audit row queued so far, then waits for the
// rows the other BpmnGo instances queued before the call. Readers of
// audit_logs call it first.
func FlushAuditLogs() {
	if w := currentAuditWriter(); w != nil {
		w.flush()
		w.waitForOtherWriters()
	}
}

// waitForOtherWriters returns once every other live writer has written
// what it queued before the call, or after auditBarrierWait.
func (w *auditWriter) waitForOtherWriters() {
	w.writeMu.Lock()
	off := w.marksOff
	w.writeMu.Unlock()
	if off {
		return
	}
	var started time.Time
	pending := func() (bool, error) {
		var behind bool
		err := w.db.Get(&behind, `
			SELECT EXISTS (SELECT 1 FROM public.audit_writer_marks
			               WHERE node <> $1 AND flushed_until < $2
			                 AND flushed_until > $2 - make_interval(secs => $3))`,
			w.node, started, auditWriterLiveFor.Seconds())
		return behind, err
	}
	if err := w.db.Get(&started, `SELECT clock_timestamp()`); err != nil {
		return
	}
	deadline := time.Now().Add(auditBarrierWait)
	for {
		behind, err := pending()
		if err != nil || !behind {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("audit: another BpmnGo instance's batch is late; reading without it")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func currentAuditWriter() *auditWriter {
	auditWriterMu.RLock()
	defer auditWriterMu.RUnlock()
	return auditBatches
}

func (w *auditWriter) enqueue(entry *models.AuditLog) {
	w.mu.Lock()
	w.queue = append(w.queue, entry)
	full := len(w.queue) >= auditFlushAt
	w.mu.Unlock()
	if full {
		select {
		case w.full <- struct{}{}:
		default:
		}
	}
}

// flush writes the queue. writeMu makes a caller wait for a batch another
// goroutine took but hasn't written yet: when flush returns, every row
// queued before the call is in the table.
func (w *auditWriter) flush() {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	mark := w.nextMark // read before the batch is taken: it covers every row queued until then
	w.mu.Lock()
	batch := w.queue
	w.queue = nil
	w.mu.Unlock()
	defer w.recordMark(mark)
	for start := 0; start < len(batch); start += auditFlushAt {
		part := batch[start:min(start+auditFlushAt, len(batch))]
		if err := insertAuditLogs(w.db, part); err != nil {
			// One bad row must not take the others with it.
			log.Printf("audit: batch of %d rows: %v (writing them one by one)", len(part), err)
			repo := NewAuditRepository(w.db)
			for _, entry := range part {
				if err := repo.insertNow(entry); err != nil {
					log.Printf("audit: %s for instance %v: %v", entry.Action, entry.ProcessInstanceID, err)
				}
			}
		}
	}
}

// recordMark records that everything queued until mark is written, and
// reads the Postgres time the next batch's mark will be (taken before that
// batch is). Called with writeMu held.
func (w *auditWriter) recordMark(mark time.Time) {
	if w.marksOff {
		return
	}
	var err error
	if mark.IsZero() {
		err = w.db.Get(&w.nextMark, `SELECT clock_timestamp()`)
	} else {
		err = w.db.Get(&w.nextMark, `
			INSERT INTO public.audit_writer_marks (node, flushed_until) VALUES ($1, $2)
			ON CONFLICT (node) DO UPDATE SET flushed_until = EXCLUDED.flushed_until
			RETURNING clock_timestamp()`, w.node, mark)
	}
	if err != nil {
		w.nextMark = time.Time{}
		if strings.Contains(err.Error(), "audit_writer_marks") {
			// Migration 022 not applied: a reader then sees only this
			// instance's queued rows, as before it.
			w.marksOff = true
			log.Printf("audit: %v — apply migration 022 for consistent audit reads across several BpmnGo instances", err)
		}
	}
}

func insertAuditLogs(db *sqlx.DB, batch []*models.AuditLog) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type instanceKeyRow struct {
		ID  uuid.UUID `db:"id"`
		Key int64     `db:"engine_key"`
	}
	uniqueIDs := make(map[uuid.UUID]struct{}, len(batch))
	for _, e := range batch {
		if e.ProcessInstanceID != nil {
			uniqueIDs[*e.ProcessInstanceID] = struct{}{}
		}
	}
	ids := make([]uuid.UUID, 0, len(uniqueIDs))
	for id := range uniqueIDs {
		ids = append(ids, id)
	}
	keys := make(map[uuid.UUID]int64, len(ids))
	if len(ids) > 0 {
		var rows []instanceKeyRow
		if err := tx.Select(&rows, `SELECT id, engine_key FROM public.process_instances WHERE id = ANY($1::uuid[])`, pq.Array(ids)); err != nil {
			return err
		}
		for _, row := range rows {
			keys[row.ID] = row.Key
		}
	}
	var sb strings.Builder
	sb.WriteString(`INSERT INTO public.audit_logs
		(id, process_instance_key, task_key, user_id, action, old_data, new_data, created_at) VALUES `)
	args := make([]interface{}, 0, len(batch)*8)
	for i, e := range batch {
		if i > 0 {
			sb.WriteString(", ")
		}
		n := i * 8
		var instanceKey *int64
		if e.ProcessInstanceID != nil {
			key, ok := keys[*e.ProcessInstanceID]
			if !ok {
				return fmt.Errorf("process instance %s not found for audit log", *e.ProcessInstanceID)
			}
			instanceKey = &key
		}
		fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8)
		args = append(args, e.ID, instanceKey, e.TaskID, e.UserID, string(e.Action), e.OldData, e.NewData, e.CreatedAt)
	}
	if _, err := tx.Exec(`SET LOCAL synchronous_commit TO OFF`); err != nil {
		return err
	}
	if _, err := tx.Exec(sb.String(), args...); err != nil {
		return err
	}
	return tx.Commit()
}
