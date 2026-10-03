//go:build !windows

package instance

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSecondAcquireFailsUntilReleased(t *testing.T) {
	old := lockPath
	lockPath = filepath.Join(t.TempDir(), "cinefin-playout.lock")
	t.Cleanup(func() { lockPath = old })

	first, err := Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(); !errors.Is(err, ErrRunning) {
		t.Fatalf("second Acquire = %v, want ErrRunning", err)
	}
	first.Release()
	again, err := Acquire()
	if err != nil {
		t.Fatalf("Acquire after Release = %v", err)
	}
	again.Release()
}
