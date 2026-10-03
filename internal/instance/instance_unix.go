//go:build !windows

package instance

import (
	"errors"
	"os"
	"syscall"
)

// lockPath is machine-wide on purpose: a fixed path in /tmp, not $TMPDIR or a
// per-user directory, so agents run by different users also exclude each other.
var lockPath = "/tmp/cinefin-playout.lock"

// Acquire takes the machine-wide lock, or returns ErrRunning.
func Acquire() (*Lock, error) {
	f, err := openLockFile()
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, err
	}
	return &Lock{release: func() { f.Close() }}, nil
}

// openLockFile opens the lock file read-only (enough for flock), creating it
// only when it does not exist yet: with fs.protected_regular, O_CREAT on a file
// another user created in sticky /tmp is refused even when it exists.
func openLockFile() (*os.File, error) {
	f, err := os.Open(lockPath)
	if os.IsNotExist(err) {
		f, err = os.OpenFile(lockPath, os.O_RDONLY|os.O_CREATE, 0o644)
	}
	return f, err
}
