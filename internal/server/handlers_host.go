package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"git.kef2.net/micky/cinefin-playout/internal/config"
	"git.kef2.net/micky/cinefin-playout/internal/hardware"
	"git.kef2.net/micky/cinefin-playout/internal/hostconfig"
)

// currentConfig re-reads config.toml so a config edit (a PUT, or a hand-edit on
// the host) is reflected, falling back to the startup snapshot if there is no
// file or it fails to load.
func (s *Server) currentConfig() config.Config {
	if s.cfg.Path == "" {
		return s.cfg
	}
	c, err := config.Load(s.cfg.Path)
	if err != nil {
		s.log.Printf("config reload: %v", err)
		return s.cfg
	}
	return c
}

// handleGetHostConfig returns the launch config (autostart + graphics + audio,
// from config.toml's [mpv.graphics]/[mpv.audio] and [mpv].autostart).
func (s *Server) handleGetHostConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentConfig().HostConfig)
}

// handlePutHostConfig replaces the whole launch config (autostart + graphics +
// audio). Cinefin edits it per PlayoutHost, using /hardware for device
// dropdowns; the agent validates it and persists it to config.toml — the source
// of truth on the host — applying it on the next player restart. Returns
// {restart_required} when mpv is up.
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
	c := s.currentConfig()
	c.HostConfig = hc
	if err := config.WriteLaunchConfig(c.WriteTarget(), c); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restart_required": s.mpvRunning()})
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
	c := s.currentConfig()
	c.Graphics.IdleMedia = body.IdleMedia
	if err := config.WriteLaunchConfig(c.WriteTarget(), c); err != nil {
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
