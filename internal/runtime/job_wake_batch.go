package runtime

import (
	"context"
	"log"
	"sync"

	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// Announcing a burst of jobs at once. Jobs created one transaction after
// another each send their NOTIFY, and the first one sends a waiting worker
// off with that job alone. Where the jobs belong to one step (the parallel
// instances of a multi-instance activity), they are announced together
// after the last commit instead.

type jobWakeBatchKey struct{}

type jobWakeBatch struct {
	mu    sync.Mutex
	types map[string]bool
}

// batchJobWakes returns a context in which created jobs are not announced,
// and the function announcing them all (each job type once).
func batchJobWakes(ctx context.Context) (context.Context, func(db *sqlx.DB)) {
	if _, nested := ctx.Value(jobWakeBatchKey{}).(*jobWakeBatch); nested {
		return ctx, func(*sqlx.DB) {} // the outer batch announces them
	}
	b := &jobWakeBatch{types: map[string]bool{}}
	return context.WithValue(ctx, jobWakeBatchKey{}, b), func(db *sqlx.DB) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for jobType := range b.types {
			if err := repository.NotifyJobAvailable(db, jobType); err != nil {
				log.Printf("job wake %s: %v", jobType, err)
			}
		}
	}
}

// deferJobWake records jobType in ctx's batch, if any; false means the
// caller announces the job itself.
func deferJobWake(ctx context.Context, jobType string) bool {
	b, ok := ctx.Value(jobWakeBatchKey{}).(*jobWakeBatch)
	if !ok {
		return false
	}
	b.mu.Lock()
	b.types[jobType] = true
	b.mu.Unlock()
	return true
}
