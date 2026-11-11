package runtime

import (
	"container/list"
	"context"
	"testing"
	"time"
)

func testAdmissionController(limit int) *admissionController {
	return &admissionController{
		baseLimit: limit, minimumLimit: limit, softBacklog: 1000, hardBacklog: 5000,
		limit: limit, changed: make(chan struct{}), waiters: list.New(),
		lastRefresh: time.Now().Add(time.Hour), // no backlog query: the test has no database
	}
}

func TestOldestWaitTracksBlockedAcquires(t *testing.T) {
	a := testAdmissionController(1)
	release, err := a.acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := a.oldestWait(time.Now()); w != 0 {
		t.Fatalf("no waiter yet, oldestWait = %s", w)
	}

	// A second acquire blocks on the full controller and shows up as waiting.
	got := make(chan func(), 1)
	go func() {
		r, _ := a.acquire(context.Background(), nil)
		got <- r
	}()
	deadline := time.Now().Add(2 * time.Second)
	for a.oldestWait(time.Now()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if w := a.oldestWait(time.Now()); w < 50*time.Millisecond {
		t.Fatalf("blocked acquire not tracked: oldestWait = %s", w)
	}

	// Once it gets the slot, it is no longer waiting.
	release()
	second := <-got
	if w := a.oldestWait(time.Now()); w != 0 {
		t.Fatalf("admitted acquire still tracked: oldestWait = %s", w)
	}
	second()
}

func TestOldestWaitForgetsCancelledAcquires(t *testing.T) {
	a := testAdmissionController(1)
	release, _ := a.acquire(context.Background(), nil)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := a.acquire(ctx, nil); err == nil {
		t.Fatal("acquire on a full controller should fail when its context ends")
	}
	if w := a.oldestWait(time.Now()); w != 0 {
		t.Fatalf("cancelled acquire still tracked: oldestWait = %s", w)
	}
}
