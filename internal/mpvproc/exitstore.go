package mpvproc

import (
	"os"
	"sync"
)

// exitStore keeps the ProcessState of recently-exited children keyed by pid, so
// the monitor/Stop paths can read the exit code after Wait() has completed in
// the spawn goroutine. It is intentionally tiny (single most-recent entry is
// enough, but a map keeps it robust to overlapping restarts).
type exitStore struct {
	mu sync.Mutex
	m  map[int]*os.ProcessState
}

var procExit = &exitStore{m: map[int]*os.ProcessState{}}

func (s *exitStore) store(pid int, st *os.ProcessState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[pid] = st
}

func (s *exitStore) load(pid int) (*os.ProcessState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[pid]
	return st, ok
}
