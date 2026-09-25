// Package server is the agent's HTTP + WebSocket surface.
//
// It exposes /health (unauth), /status, /ws/control, /hostconfig, /hardware and
// /mpv/* (all auth) — the control bridge plus host config and process lifecycle —
// and a loopback-only /ui control panel (see panel.go). Bearer-token auth is a
// constant-time compare against server.token; if no
// token is configured a warning is logged and requests are allowed, but the
// agent normally auto-generates one on first run (see config.EnsureToken).
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"git.kef2.net/micky/cinefin-playout/internal/config"
	"git.kef2.net/micky/cinefin-playout/internal/player"
	"git.kef2.net/micky/cinefin-playout/internal/version"
)

// Server holds the agent's runtime dependencies.
type Server struct {
	cfg     config.Config
	control *control
	backend player.Backend
	log     *log.Logger
}

// New builds a Server. backend is the mpv playback engine, injected so the caller
// owns its lifecycle. The server wires the backend's control channel to its
// single-client /ws/control bridge here — before backend.Run starts delivering
// frames — so the caller need not know about it.
func New(cfg config.Config, backend player.Backend, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{cfg: cfg, control: newControl(backend), backend: backend, log: logger}
	backend.OnMessage(s.control.fromMPV)
	// mpv came back after a drop: drop the client so it re-subscribes against
	// the fresh instance (which has none of its observers).
	backend.OnMPVReconnect(s.control.disconnect)
	return s
}

// ControlConnected reports whether a /ws/control client is attached (used by the
// desktop tray to show the Cinefin link state).
func (s *Server) ControlConnected() bool { return s.control.connected() }

// Handler returns the agent's HTTP handler with all routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("GET /status", s.auth(http.HandlerFunc(s.handleStatus)))
	mux.Handle("GET /ws/control", s.auth(http.HandlerFunc(s.handleControl)))
	mux.Handle("GET /hostconfig", s.auth(http.HandlerFunc(s.handleGetHostConfig)))
	// The launch config (graphics/audio/autostart) is edited remotely by Cinefin
	// and persisted to the agent's config.toml, or hand-edited on the host. The
	// narrower idle-media PUT rewrites only the Cinefin-owned cinema ident.
	mux.Handle("PUT /hostconfig", s.auth(http.HandlerFunc(s.handlePutHostConfig)))
	mux.Handle("PUT /hostconfig/idle-media", s.auth(http.HandlerFunc(s.handlePutIdleMedia)))
	mux.Handle("GET /hardware", s.auth(http.HandlerFunc(s.handleHardware)))
	mux.Handle("POST /mpv/start", s.auth(http.HandlerFunc(s.handleMPVStart)))
	mux.Handle("POST /mpv/stop", s.auth(http.HandlerFunc(s.handleMPVStop)))
	mux.Handle("POST /mpv/restart", s.auth(http.HandlerFunc(s.handleMPVRestart)))
	// The loopback-only /ui control panel (served to the native webview shell).
	s.registerPanel(mux)
	return mux
}

// auth enforces the bearer token. An empty configured token allows all requests
// (a warning is logged at boot); in practice the agent auto-generates one.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		want := "Bearer " + s.cfg.Token
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "Invalid or missing bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// boolToCount maps the single-client control link to a 0/1 count for /status,
// keeping the historical clients field shape.
func boolToCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Version,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	mpvVersion, reachable := s.backend.Probe(ctx)
	st := s.backend.Status()

	mpvInfo := map[string]any{
		"ipc_socket":        s.cfg.IPCSocket,
		"reachable":         reachable,
		"version":           mpvVersion,
		"running":           st.Running,
		"mode":              st.Mode,
		"restarts":          st.Restarts,
		"last_exit_code":    st.LastExitCode,
		"socket_present":    st.SocketPresent,
		"socket_responding": reachable, // single probe: reachable already dialed the socket
		"desired_running":   st.DesiredRunning,
	}
	if st.PID != 0 {
		mpvInfo["pid"] = st.PID
	}
	if st.StartedAt != 0 {
		mpvInfo["started_at"] = st.StartedAt
		mpvInfo["uptime_seconds"] = st.UptimeSeconds
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_version": version.Version,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"control": map[string]any{
			"clients": boolToCount(s.control.connected()),
		},
		"mpv": mpvInfo,
	})
}

// handleControl upgrades to a websocket and attaches it to the control bridge.
func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // no origin check: LAN agent, bearer-authed
	})
	if err != nil {
		s.log.Printf("ws accept failed: %v", err)
		return
	}
	// Serve until the client goes away.
	s.serveControl(r.Context(), conn)
}

func (s *Server) serveControl(parent context.Context, conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Outbound frames are serialised through this channel so the control bridge
	// (which may call send from mpv's read goroutine) never touches the
	// websocket concurrently with the read loop.
	out := make(chan []byte, 64)

	client := s.control.attach(func(frame []byte) {
		select {
		case out <- frame:
		case <-ctx.Done():
		}
	}, cancel) // close: cancel the ctx → the read loop ends → the peer disconnects
	defer s.control.detach(client)

	// Writer goroutine.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-out:
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageText, frame)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Read loop.
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		s.control.inbound(data)
	}

	cancel()
	<-writerDone
	closeStatus := websocket.StatusNormalClosure
	_ = conn.Close(closeStatus, "")
}

// Run starts the HTTP server and blocks until ctx is cancelled, then shuts down.
func (s *Server) Run(ctx context.Context) error {
	addr := s.cfg.Host + ":" + strconv.Itoa(s.cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if s.cfg.Token == "" {
		s.log.Printf("WARNING: no auth token configured — API is unauthenticated (server.token)")
	}

	errc := make(chan error, 1)
	go func() {
		s.log.Printf("cinefin-playout agent listening on %s (mpv ipc: %s)", addr, s.cfg.IPCSocket)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errc:
		return err
	}
}
