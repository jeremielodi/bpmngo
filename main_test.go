package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The demo secret is generated once and read back unchanged afterwards (a
// restart, or another instance sharing the volume).
func TestDemoJWTSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "jwt_secret")
	first, err := demoJWTSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 32 {
		t.Fatalf("secret too short: %d", len(first))
	}
	again, err := demoJWTSecret(path)
	if err != nil || again != first {
		t.Fatalf("second read: %q, %v; want the same secret", again, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("secret file must be private: %v %v", info.Mode(), err)
	}
}
