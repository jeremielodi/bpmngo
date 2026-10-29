package repository

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// JobAvailableChannel is the Postgres NOTIFY channel used to broadcast job
// creation across every BpmnGo instance sharing this database, not just the
// instance that created the job. This is what lets a horizontally-scaled
// deployment (multiple stateless app instances behind a load balancer, one
// Postgres) still get near-instant gRPC ActivateJobs wake-ups on any
// instance, instead of only the one that happened to run the workflow step.
const JobAvailableChannel = "goflow_job_available"

// NotifyJobAvailable announces, via Postgres NOTIFY, that a job of jobType
// was created — waking a caller waiting for it on every BpmnGo instance
// connected to this database (including this one, which receives its own
// notification back through the same LISTEN connection started by
// StartJobWakeListener). Must only be called after the job's creating
// transaction has committed — waking a waiter before the row is visible
// would just send it back to an empty query.
func NotifyJobAvailable(db *sqlx.DB, jobType string) error {
	_, err := db.Exec(`SELECT pg_notify($1, $2)`, JobAvailableChannel, jobType)
	return err
}

// Waiting for jobs. Each caller looking for jobs of a type (gRPC
// ActivateJobs and StreamActivatedJobs, Camunda 7 fetchAndLock long
// polling) queues a JobWaiter before it looks. A job created wakes ONE
// waiter — the longest waiting — not every waiter of the type: waking them
// all sent each one to fetch, for a single job, the main cost under load.
// No job is forgotten for it:
//   - a waiter woken that leaves without fetching (end of its request)
//     hands the wake to the next waiter (Release);
//   - a fetch that comes back full wakes another waiter (WakeJobWaiter):
//     more jobs may be waiting;
//   - the fallback poll still runs, for a notify missed during a listener
//     reconnect.
var (
	jobWaitMu sync.Mutex
	jobWaits  = make(map[string][]*JobWaiter)
)

// JobWaiter is one caller waiting for jobs of a type. C closes when it is
// woken.
type JobWaiter struct {
	jobType string
	ch      chan struct{}
	woken   bool // under jobWaitMu
	C       <-chan struct{}
}

// WaitForJob queues a waiter for jobType. Call it *before* looking for
// jobs, so a job created while looking still wakes it.
func WaitForJob(jobType string) *JobWaiter {
	ch := make(chan struct{})
	w := &JobWaiter{jobType: jobType, ch: ch, C: ch}
	jobWaitMu.Lock()
	jobWaits[jobType] = append(jobWaits[jobType], w)
	jobWaitMu.Unlock()
	return w
}

// Release takes w out of the queue when its caller stops waiting without
// having acted on a wake: found jobs, timed out, or left. A wake it had
// already received goes to the next waiter.
func (w *JobWaiter) Release() {
	jobWaitMu.Lock()
	woken := w.woken
	if !woken {
		queue := jobWaits[w.jobType]
		for i, other := range queue {
			if other == w {
				jobWaits[w.jobType] = append(queue[:i:i], queue[i+1:]...)
				break
			}
		}
		if len(jobWaits[w.jobType]) == 0 {
			delete(jobWaits, w.jobType)
		}
	}
	jobWaitMu.Unlock()
	if woken {
		wakeLocal(w.jobType)
	}
}

// WakeJobWaiter wakes one waiter for jobType on this BpmnGo instance (a
// fetch came back full: more jobs may be waiting).
func WakeJobWaiter(jobType string) { wakeLocal(jobType) }

// wakeLocal wakes the longest-waiting waiter for jobType, if any. With
// none, nothing is lost: the next waiter looks for jobs after queuing.
func wakeLocal(jobType string) {
	jobWaitMu.Lock()
	defer jobWaitMu.Unlock()
	queue := jobWaits[jobType]
	if len(queue) == 0 {
		return
	}
	w := queue[0]
	if len(queue) == 1 {
		delete(jobWaits, jobType)
	} else {
		jobWaits[jobType] = queue[1:]
	}
	w.woken = true
	close(w.ch)
}

// StartJobWakeListener opens a dedicated Postgres LISTEN connection (via
// lib/pq's reconnecting Listener — LISTEN state is tied to one backend
// connection, so this can't reuse the pooled *sqlx.DB) and translates every
// NOTIFY on JobAvailableChannel into a local wake-up via wakeLocal. Call
// once per process, after the main DB connection is established. If the
// listener connection drops and reconnects, any notifications sent during
// the gap are missed — this is safe because ActivateJobs also polls on a
// fixed fallback interval as a safety net (a job's lock expiring, for
// example, never fires a notify at all).
func StartJobWakeListener(connStr string) (*pq.Listener, error) {
	listener := pq.NewListener(connStr, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			log.Printf("job wake listener: %v", err)
		}
	})
	// The same connection carries InstanceFinishedChannel (see
	// instance_notifier.go) and TimerArmedChannel (timer_notifier.go), with
	// the same fallback-poll safety net.
	for _, channel := range []string{JobAvailableChannel, InstanceFinishedChannel, TimerArmedChannel} {
		if err := listener.Listen(channel); err != nil {
			listener.Close()
			return nil, err
		}
	}

	go func() {
		for n := range listener.Notify {
			if n == nil {
				// A reconnect occurred; nothing to replay, the fallback
				// poll interval covers anything missed during the gap.
				continue
			}
			switch n.Channel {
			case InstanceFinishedChannel:
				if id, err := uuid.Parse(n.Extra); err == nil {
					wakeInstance(id)
				}
			case TimerArmedChannel:
				wakeTimers()
			default:
				wakeLocal(n.Extra)
			}
		}
	}()

	return listener, nil
}
