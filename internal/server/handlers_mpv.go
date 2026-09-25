package server

import (
	"net/http"

	"git.kef2.net/micky/cinefin-playout/internal/player"
)

// wantWait parses ?wait= (default true) — whether the call blocks until the
// player is controllable before returning.
func wantWait(r *http.Request) bool {
	q := r.URL.Query().Get("wait")
	switch q {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

// actionResult reports ok/message plus the current player status.
func (s *Server) actionResult(w http.ResponseWriter, ok bool, msg string, st player.Status) {
	status := http.StatusOK
	if !ok {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{
		"ok":      ok,
		"message": msg,
		"status":  st,
	})
}

func (s *Server) handleMPVStart(w http.ResponseWriter, r *http.Request) {
	ok, msg := s.backend.Start(wantWait(r))
	s.actionResult(w, ok, msg, s.backend.Status())
}

func (s *Server) handleMPVStop(w http.ResponseWriter, _ *http.Request) {
	ok, msg := s.backend.Stop()
	s.actionResult(w, ok, msg, s.backend.Status())
}

func (s *Server) handleMPVRestart(w http.ResponseWriter, r *http.Request) {
	ok, msg := s.backend.Restart(wantWait(r))
	s.actionResult(w, ok, msg, s.backend.Status())
}
