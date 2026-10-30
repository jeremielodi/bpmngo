package service

import (
	"context"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// Archiving finished instances (archive_service.go) used to run inside the
// request or job completion that finished them — the slowest step of an
// instance's last transaction, measured as the end-to-end throughput
// bottleneck. By default it now runs in the background: the instance is
// only marked completed or terminated, and StartArchiver moves finished
// instances to the historic_* tables in batches. Nothing reads differently
// meanwhile: every read path already serves an instance from the live or
// the historic tables, wherever it currently is.
//
// ARCHIVE_MODE=sync restores archiving in the completing call.

// asyncArchiving tells whether finished instances are archived in the
// background (the default) rather than in the completing call.
func asyncArchiving() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("ARCHIVE_MODE")), "sync")
}

// ArchiveFinishedInstance archives a completed or terminated instance —
// right away with ARCHIVE_MODE=sync, otherwise leaving it to the
// background archiver.
func ArchiveFinishedInstance(db *sqlx.DB, instanceID uuid.UUID, deleteReason *string) error {
	if asyncArchiving() {
		return nil
	}
	return ArchiveAndDeleteProcessInstance(db, instanceID, deleteReason)
}

const (
	archiverBatchMin     = 10
	archiverBatchInitial = 25
	archiverBatchMax     = 100
	archiveCommitBatch   = 10
)

var adaptiveArchiverBatch atomic.Int64

func currentArchiverBatch() int {
	if n := adaptiveArchiverBatch.Load(); n > 0 {
		return int(n)
	}
	adaptiveArchiverBatch.CompareAndSwap(0, archiverBatchInitial)
	return int(adaptiveArchiverBatch.Load())
}

// StartArchiver archives finished instances in the background until ctx
// is done (no-op with ARCHIVE_MODE=sync). Several BpmnGo instances can run
// it: each instance is archived under a session advisory lock — held on
// lockDB, which reaches Postgres directly (see database.NewDirectPostgres)
// — so two archivers never work on the same one.
func StartArchiver(ctx context.Context, db, lockDB *sqlx.DB) {
	if !asyncArchiving() {
		return
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Drain while full batches come back. The next pass observes the
				// batch size adapted from the duration of the previous one.
				for ctx.Err() == nil {
					limit := currentArchiverBatch()
					if archivePass(ctx, db, lockDB) < limit {
						break
					}
				}
			}
		}
	}()
}

// archivePass archives up to archiverBatch finished instances and returns
// how many it found.
func archivePass(ctx context.Context, db, lockDB *sqlx.DB) int {
	started := time.Now()
	limit := currentArchiverBatch()
	defer func() {
		elapsed := time.Since(started)
		next := limit
		switch {
		case elapsed > 750*time.Millisecond && limit > archiverBatchMin:
			next = max(archiverBatchMin, limit/2)
		case elapsed < 200*time.Millisecond && limit < archiverBatchMax:
			next = min(archiverBatchMax, limit+10)
		}
		adaptiveArchiverBatch.Store(int64(next))
	}()
	var finished []struct {
		ID     uuid.UUID `db:"id"`
		Status string    `db:"status"`
	}
	if err := db.SelectContext(ctx, &finished, `
		SELECT id, status FROM public.process_instances
		WHERE status IN ('completed', 'terminated')
		ORDER BY ended_at
		LIMIT $1
	`, limit); err != nil {
		log.Printf("archiver: %v", err)
		return 0
	}
	if len(finished) == 0 {
		return 0
	}
	// Establish one audit visibility barrier for the entire archive pass.
	// Previously every instance forced its own writer flush and transaction.
	repository.FlushAuditLogs()
	conn, err := lockDB.Connx(ctx)
	if err != nil {
		log.Printf("archiver: %v", err)
		return 0
	}
	defer conn.Close()
	var snapshots []archiveSnapshot
	var lockedIDs []uuid.UUID
	flushSnapshots := func() {
		if len(snapshots) == 0 {
			return
		}
		if err := writeArchiveSnapshots(db, snapshots); err != nil {
			log.Printf("archiver: batch of %d instances: %v", len(snapshots), err)
		}
		for _, id := range lockedIDs {
			if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('archive:' || $1))`, id.String()); err != nil {
				log.Printf("archiver: unlock instance %s: %v", id, err)
			}
		}
		snapshots = snapshots[:0]
		lockedIDs = lockedIDs[:0]
	}
	for _, f := range finished {
		var locked bool
		if err := conn.GetContext(ctx, &locked, `SELECT pg_try_advisory_lock(hashtext('archive:' || $1))`, f.ID.String()); err != nil || !locked {
			continue // another archiver has it
		}
		var reason *string
		if f.Status == "terminated" {
			var r string
			if err := db.GetContext(ctx, &r, `SELECT COALESCE(data ->> 'terminationReason', '') FROM public.variables WHERE process_instance_key = (SELECT engine_key FROM process_instances WHERE id = $1)`, f.ID); err == nil && r != "" {
				reason = &r
			}
		}
		snapshot, err := prepareArchiveSnapshot(db, f.ID, reason)
		if err != nil {
			if !strings.Contains(err.Error(), "not found") {
				log.Printf("archiver: instance %s: %v", f.ID, err)
			}
			conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('archive:' || $1))`, f.ID.String())
			continue
		}
		snapshots = append(snapshots, *snapshot)
		lockedIDs = append(lockedIDs, f.ID)
		if len(snapshots) >= archiveCommitBatch {
			flushSnapshots()
		}
	}
	flushSnapshots()
	return len(finished)
}
