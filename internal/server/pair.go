package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"runtime"

	"github.com/cinefin/cinefin-playout/internal/pairing"
	"github.com/cinefin/cinefin-playout/internal/version"
)

// handlePair is how Cinefin pairs: it posts the code shown on the player's
// screen and gets back the bearer token for every other endpoint. It needs no
// auth (there is no token yet); the code's rotation and attempt limits guard it
// (see package pairing). A paired player refuses until it is unpaired.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if s.state.Paired() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this player is already paired; unpair it first"})
		return
	}
	switch err := s.codes.Check(body.Code); {
	case errors.Is(err, pairing.ErrWrongCode):
		s.log.Printf("pairing: wrong code from %s", r.RemoteAddr)
		s.card.wake() // the code rotated: show the new one now
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "wrong code; check the code on the player's screen"})
		return
	case err != nil:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": err.Error()})
		return
	}

	tok, err := newToken()
	if err == nil {
		err = s.state.SetToken(tok)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.log.Printf("pairing: paired with %s", r.RemoteAddr)
	s.card.startConfirm(remoteHost(r.RemoteAddr), body.Code)
	s.pairingChanged(true)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":         tok,
		"id":            s.id.ID,
		"name":          s.id.Name,
		"agent_version": version.Version,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"protocol":      protocolVersion,
	})
}

// handleUnpair is Cinefin forgetting this player (the host was deleted).
func (s *Server) handleUnpair(w http.ResponseWriter, _ *http.Request) {
	if err := s.Unpair(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Unpair forgets the pairing and the launch config Cinefin set, drops the
// control link, and restarts the player so it shows the pairing card on this
// machine's default screen. Used by POST /unpair, the loopback /ui/unpair (the
// tray and `cinefin-playout reset`) and the status page.
func (s *Server) Unpair() error {
	if err := s.state.Reset(); err != nil {
		return err
	}
	s.log.Printf("pairing: unpaired")
	s.control.disconnect()
	s.card.setTestCard(false)
	s.tone.reset()
	s.pairingChanged(false)
	// Stopping mpv can take seconds; do not hold the request for it.
	go s.backend.Restart(false)
	return nil
}

func (s *Server) pairingChanged(paired bool) {
	s.card.wake()
	if s.onPairing != nil {
		s.onPairing(paired)
	}
}

// newToken returns a URL-safe base64 bearer token with 24 bytes of entropy.
func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// remoteHost is the host part of a request's RemoteAddr ("10.0.0.2:51234" ->
// "10.0.0.2").
func remoteHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
