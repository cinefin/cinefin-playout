// Package instance keeps the agent to one per machine. Two agents would fight
// over the screen, the mpv IPC socket and the mDNS announcement, so a second
// one refuses to start.
//
// The lock is taken by the OS on the process's behalf (a flock on unix, a named
// mutex on Windows), so it is released when the agent exits or dies; a crash
// never leaves a stale lock behind.
package instance

import "errors"

// ErrRunning is returned by Acquire when another agent holds the lock.
var ErrRunning = errors.New("cinefin-playout is already running on this machine")

// Lock is the held single-instance lock. Keep it for the agent's lifetime.
type Lock struct {
	release func()
}

// Release gives the lock up. Exiting does the same.
func (l *Lock) Release() {
	if l != nil && l.release != nil {
		l.release()
		l.release = nil
	}
}
