package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

// TimerArmedChannel is the Postgres NOTIFY channel announcing a new timer,
// so every BpmnGo instance's timer scheduler re-reads its next deadline
// instead of finding the timer at its next poll.
const TimerArmedChannel = "goflow_timer_armed"

var timerWake = make(chan struct{}, 1)

// NotifyTimerArmedTx announces a timer armed in q. Inside a transaction,
// Postgres delivers it on commit only (and drops it on rollback), so call
// it right after the INSERT.
func NotifyTimerArmedTx(q sqlx.Execer) error {
	_, err := q.Exec(`SELECT pg_notify($1, '')`, TimerArmedChannel)
	return err
}

// TimerWake fires when a timer was armed on any BpmnGo instance (see
// StartJobWakeListener).
func TimerWake() <-chan struct{} { return timerWake }

func wakeTimers() {
	select {
	case timerWake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// NextTimerDueIn is how long until the earliest unfired timer is due, by
// the database's clock (the one claiming compares due_at with); ok is false
// when no timer is armed.
func (r *EngineRepository) NextTimerDueIn(ctx context.Context) (d time.Duration, ok bool, err error) {
	var seconds sql.NullFloat64
	if err := r.db.GetContext(ctx, &seconds, `
		SELECT EXTRACT(EPOCH FROM MIN(due_at) - NOW())::float8
		FROM public.timer_jobs WHERE is_triggered = false
	`); err != nil || !seconds.Valid {
		return 0, false, err
	}
	return time.Duration(seconds.Float64 * float64(time.Second)), true, nil
}
