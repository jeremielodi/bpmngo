package repository

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// These tests exercise wakeLocal directly (the in-memory waiter queue)
// rather than NotifyJobAvailable, which needs a live Postgres to NOTIFY —
// that path is covered by TestNotifyJobAvailable_IssuesPgNotify below (via
// sqlmock) and by the e2e suites against a real database.

func woken(w *JobWaiter) bool {
	select {
	case <-w.C:
		return true
	default:
		return false
	}
}

func TestJobWaiter_OneJobWakesOneWaiter(t *testing.T) {
	jobType := "wake-test-" + t.Name()
	first, second := WaitForJob(jobType), WaitForJob(jobType)
	defer second.Release()

	wakeLocal(jobType)
	if !woken(first) || woken(second) {
		t.Fatalf("a job must wake the longest-waiting waiter only: first %v, second %v", woken(first), woken(second))
	}
	wakeLocal(jobType)
	if !woken(second) {
		t.Fatal("the next job must wake the next waiter")
	}
}

func TestJobWaiter_UnrelatedTypeIsNotWoken(t *testing.T) {
	w := WaitForJob("wake-test-watched-" + t.Name())
	defer w.Release()
	wakeLocal("wake-test-other-" + t.Name())
	if woken(w) {
		t.Fatal("a job of another type woke the waiter")
	}
}

func TestJobWaiter_ReleasedWaiterGetsNoWake(t *testing.T) {
	jobType := "wake-test-" + t.Name()
	gone, staying := WaitForJob(jobType), WaitForJob(jobType)
	defer staying.Release()
	gone.Release() // its request ended before any job came

	wakeLocal(jobType)
	if !woken(staying) {
		t.Fatal("the wake went to a waiter that had left")
	}
}

func TestJobWaiter_WokenWaiterLeavingPassesTheWakeOn(t *testing.T) {
	jobType := "wake-test-" + t.Name()
	leaving, next := WaitForJob(jobType), WaitForJob(jobType)
	defer next.Release()

	wakeLocal(jobType) // wakes leaving...
	leaving.Release()  // ...which stops without fetching
	if !woken(next) {
		t.Fatal("a wake received by a waiter that left must go to the next one")
	}
}

func TestJobWaiter_NoWaiterNothingQueued(t *testing.T) {
	jobType := "wake-test-" + t.Name()
	wakeLocal(jobType) // nobody waiting: the next waiter looks for jobs itself
	w := WaitForJob(jobType)
	defer w.Release()
	if woken(w) {
		t.Fatal("a waiter queued after a job must not start woken (it fetches first)")
	}
}

func TestNotifyJobAvailable_IssuesPgNotify(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxDB := sqlx.NewDb(db, "postgres")

	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify($1, $2)")).
		WithArgs(JobAvailableChannel, "my-job-type").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := NotifyJobAvailable(sqlxDB, "my-job-type"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
