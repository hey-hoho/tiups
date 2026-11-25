//go:build !windows

package lock

import (
	"path/filepath"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.lock")
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); err != ErrRunning {
		t.Fatalf("second Acquire = %v, want ErrRunning", err)
	}
	release()
	release2, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	release2()
}
