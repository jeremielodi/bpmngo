package repository

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
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
// row logged before it (archiving, token history, step back, audit routes).

const (
	auditFlushEvery = 20 * time.Millisecond
	auditFlushAt    = 200
)

type auditWriter struct {
	db      *sqlx.DB
	mu      sync.Mutex // the queue
	queue   []*models.AuditLog
	full    chan struct{}
	writeMu sync.Mutex // one batch written at a time, in order
}

var (
	auditWriterMu sync.RWMutex
	auditBatches  *auditWriter
)

// StartAuditWriter batches audit rows until ctx is done, then writes what
// is left.
func StartAuditWriter(ctx context.Context, db *sqlx.DB) {
	w := &auditWriter{db: db, full: make(chan struct{}, 1)}
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

// FlushAuditLogs writes every audit row queued so far. Readers of
// audit_logs call it first.
func FlushAuditLogs() {
	if w := currentAuditWriter(); w != nil {
		w.flush()
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
	w.mu.Lock()
	batch := w.queue
	w.queue = nil
	w.mu.Unlock()
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

func insertAuditLogs(db *sqlx.DB, batch []*models.AuditLog) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO public.audit_logs
		(id, process_instance_id, task_id, user_id, action, old_data, new_data, created_at) VALUES `)
	args := make([]interface{}, 0, len(batch)*8)
	for i, e := range batch {
		if i > 0 {
			sb.WriteString(", ")
		}
		n := i * 8
		fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8)
		args = append(args, e.ID, e.ProcessInstanceID, e.TaskID, e.UserID, string(e.Action), e.OldData, e.NewData, e.CreatedAt)
	}
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL synchronous_commit TO OFF`); err != nil {
		return err
	}
	if _, err := tx.Exec(sb.String(), args...); err != nil {
		return err
	}
	return tx.Commit()
}
