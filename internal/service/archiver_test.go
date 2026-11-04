package service

import (
	"testing"
	"time"
)

func TestArchiverRetryWaitDoublesUpToMax(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		if got := archiverRetryWait(i + 1); got != w {
			t.Fatalf("after %d failures: got %s, want %s", i+1, got, w)
		}
	}
	if got := archiverRetryWait(1000); got != archiverRetryMax {
		t.Fatalf("after 1000 failures: got %s, want %s", got, archiverRetryMax)
	}
}
