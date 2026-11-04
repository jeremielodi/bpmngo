package runtime

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// Back-pressure: refuse new work when the engine is already behind, as
// Zeebe does with RESOURCE_EXHAUSTED, instead of queueing every request
// until the client times out.
//
// The signal is the admission controller's own queue: how long the oldest
// execution still waiting for a slot has been waiting. It only grows when
// work arrives faster than the engine finishes it, whatever the hardware,
// and it already accounts for the archive-backlog throttling (a lower
// limit makes the queue wait longer).
//
// Only new work is refused (starting an instance, publishing a message,
// broadcasting a signal): completing or failing a job finishes work already
// accepted and frees a slot, so it always queues.
//
// BACKPRESSURE=off disables it. BACKPRESSURE_MAX_WAIT_MS (default 150) is
// the queueing delay above which new work is refused. An instance queues
// once per step it resumes from (start, then each job completion, which are
// never refused), so its end-to-end latency is a few times this delay: on
// the benchmark's start → 2 jobs → end, 150 ms kept the p99 under 1 s at 50
// and 100 clients, against 1.1 s with 250 ms and 4.3 s without back-pressure,
// for the same throughput (benchmark/results/load-2026-11-04-bp*).

// ErrBackpressure is returned by AdmitNewWork when the engine is overloaded.
var ErrBackpressure = errors.New("engine overloaded: the request was refused, retry later")

const defaultBackpressureMaxWait = 150 * time.Millisecond

var (
	backpressureOnce    sync.Once
	backpressureEnabled bool
	backpressureMaxWait time.Duration
)

func backpressureConfig() (bool, time.Duration) {
	backpressureOnce.Do(func() {
		backpressureEnabled = !strings.EqualFold(strings.TrimSpace(os.Getenv("BACKPRESSURE")), "off")
		backpressureMaxWait = time.Duration(configuredPositiveInt("BACKPRESSURE_MAX_WAIT_MS", int(defaultBackpressureMaxWait/time.Millisecond))) * time.Millisecond
	})
	return backpressureEnabled, backpressureMaxWait
}

// AdmitNewWork tells whether a request that creates new work may proceed:
// ErrBackpressure when the oldest execution waiting for a runtime slot has
// waited longer than BACKPRESSURE_MAX_WAIT_MS.
func AdmitNewWork() error {
	enabled, maxWait := backpressureConfig()
	if !enabled {
		return nil
	}
	if admissionControl().oldestWait(time.Now()) > maxWait {
		return ErrBackpressure
	}
	return nil
}

// oldestWait is how long the oldest acquire still waiting has been waiting
// (0 when none is).
func (a *admissionController) oldestWait(now time.Time) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	front := a.waiters.Front()
	if front == nil {
		return 0
	}
	return now.Sub(front.Value.(time.Time))
}
