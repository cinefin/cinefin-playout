package server

import (
	"context"
	"embed"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/cinefin/cinefin-playout/internal/version"
)

// panelFS holds the embedded control-panel page served at /ui. It is a plain
// status + player-control view (no config editing) rendered by the native
// webview shell (`--panel`), and reachable only from loopback.
//
//go:embed panel/index.html
var panelFS embed.FS

// registerPanel mounts the loopback-only control panel: the embedded page at
// /ui, a JSON state feed, and player start/stop/restart. These carry no bearer
// token — they are gated to 127.0.0.1 instead, so the panel works locally while
// the LAN-facing API still requires the token.
func (s *Server) registerPanel(mux *http.ServeMux) {
	mux.Handle("GET /ui", loopbackOnly(http.HandlerFunc(s.handlePanelIndex)))
	mux.Handle("GET /ui/state", loopbackOnly(http.HandlerFunc(s.handlePanelState)))
	mux.Handle("POST /ui/player/start", loopbackOnly(http.HandlerFunc(s.handleMPVStart)))
	mux.Handle("POST /ui/player/stop", loopbackOnly(http.HandlerFunc(s.handleMPVStop)))
	mux.Handle("POST /ui/player/restart", loopbackOnly(http.HandlerFunc(s.handleMPVRestart)))
}

// loopbackOnly rejects any request whose peer is not on the loopback interface.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			http.Error(w, "control panel is loopback-only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handlePanelIndex(w http.ResponseWriter, _ *http.Request) {
	page, err := panelFS.ReadFile("panel/index.html")
	if err != nil {
		http.Error(w, "panel unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

// handlePanelState feeds the panel: player/control status plus the pairing
// address and token to copy into Cinefin.
func (s *Server) handlePanelState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	_, reachable := s.backend.Probe(ctx)
	st := s.backend.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           version.Version,
		"os":                runtime.GOOS,
		"arch":              runtime.GOARCH,
		"address":           s.pairingAddress(),
		"token":             s.cfg.Token,
		"player_running":    st.Running,
		"mpv_reachable":     reachable,
		"cinefin_connected": s.control.connected(),
	})
}

// pairingAddress is the URL to paste into Cinefin: the host's primary LAN IP
// (falling back to hostname, then loopback) and the agent port.
func (s *Server) pairingAddress() string {
	host := primaryIP()
	if host == "" {
		if hn, err := os.Hostname(); err == nil && hn != "" {
			host = hn
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(s.cfg.Port))
}

// primaryIP returns the machine's primary outbound IP without sending packets.
func primaryIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	if addr, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}
