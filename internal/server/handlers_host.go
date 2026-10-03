package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cinefin/cinefin-playout/internal/hardware"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// handleGetHostConfig returns the launch config (autostart + graphics + audio):
// the one Cinefin last set, or this machine's preset defaults.
func (s *Server) handleGetHostConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.state.Launch())
}

// handlePutHostConfig replaces the whole launch config (autostart + graphics +
// audio). Cinefin edits it per PlayoutHost, using /hardware for device
// dropdowns; the agent validates it and keeps it in its state file, applying it
// on the next player restart. Returns {restart_required} when mpv is up.
func (s *Server) handlePutHostConfig(w http.ResponseWriter, r *http.Request) {
	var hc hostconfig.HostConfig
	if err := json.NewDecoder(r.Body).Decode(&hc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := hc.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.saveLaunch(w, hc)
}

// handlePutIdleMedia updates only the idle-screen media (graphics.idle_media) —
// the cinema ident Cinefin owns and streams — leaving every other graphics/
// audio setting untouched. Returns {restart_required} when mpv is up.
func (s *Server) handlePutIdleMedia(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdleMedia string `json:"idle_media"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	hc := s.state.Launch()
	hc.Graphics.IdleMedia = body.IdleMedia
	s.saveLaunch(w, hc)
}

// saveLaunch persists hc and replies {restart_required}.
func (s *Server) saveLaunch(w http.ResponseWriter, hc hostconfig.HostConfig) {
	if err := s.state.SetLaunch(hc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restart_required": s.mpvRunning()})
}

// mpvRunning reports whether mpv is currently up — i.e. reachable on its IPC
// socket, which is what decides whether a launch-config change needs a restart
// to take effect. This is a reachability probe, not just "is our child alive".
func (s *Server) mpvRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, reachable := s.backend.Probe(ctx)
	return reachable
}

// handleHardware enumerates real host devices on demand. Never 500s: a missing
// mpv binary yields empty lists + a note.
func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	rep := hardware.Enumerate(ctx, s.cfg.ResolveMPVBinary(), "")
	writeJSON(w, http.StatusOK, rep)
}
