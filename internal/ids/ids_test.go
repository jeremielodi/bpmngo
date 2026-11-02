package ids

import (
	"testing"
	"time"
)

func TestNewGlobalIDIsVersion7(t *testing.T) {
	if got := NewGlobalID().Version(); got != 7 {
		t.Fatalf("version = %d, want 7", got)
	}
}

func TestEngineKeysAreMonotonicAndEncodeNode(t *testing.T) {
	g, err := NewGenerator(37)
	if err != nil {
		t.Fatal(err)
	}
	g.now = func() time.Time { return engineEpoch.Add(1234 * time.Millisecond) }
	a, b := g.Next(), g.Next()
	if b != a+1 {
		t.Fatalf("keys are not sequential: %d, %d", a, b)
	}
	if node := (a >> sequenceBits) & maxNode; node != 37 {
		t.Fatalf("node bits = %d, want 37", node)
	}
}

func TestEngineKeyGeneratorRejectsInvalidNode(t *testing.T) {
	if _, err := NewGenerator(-1); err == nil {
		t.Fatal("negative node accepted")
	}
	if _, err := NewGenerator(maxNode + 1); err == nil {
		t.Fatal("oversized node accepted")
	}
}
