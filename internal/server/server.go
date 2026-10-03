// Package server is the agent's HTTP + WebSocket surface.
//
// It exposes /health and /pair (unauth), /status, /ws/control, /hostconfig,
// /hardware, /mpv/* and /unpair (all auth) — pairing, the control bridge, host
// config and process lifecycle — and a loopback-only /ui status page (see
// panel.go). Bearer-token auth is a constant-time compare against the token the
// agent issued when Cinefin paired (see pair.go); an unpaired agent has no token
// and refuses every authenticated request.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/pairing"
	"github.com/cinefin/cinefin-playout/internal/player"
	"github.com/cinefin/cinefin-playout/internal/state"
	"github.com/cinefin/cinefin-playout/internal/version"
)

// Identity is how the player presents itself: on its screen, over mDNS and to
// Cinefin. ID is stable across unpairing (see state.Store.ID).
type Identity struct {
	ID   string
	Name string
}

// Server holds the agent's runtime dependencies.
type Server struct {
	cfg     config.Config
	id      Identity
	state   *state.Store
	codes   *pairing.Codes
	control *control
	backend player.Backend
	card    *card
	log     *log.Logger

	onPairing func(paired bool)
}

// New builds a Server. st is the agent's persistent state (token + launch
// config) and codes the pairing codes it accepts while unpaired. backend is the
// mpv playback engine, injected so the caller owns its lifecycle. The server
// wires the backend's control channel to its single-client /ws/control bridge
// here — before backend.Run starts delivering frames — so the caller need not
// know about it.
func New(cfg config.Config, id Identity, st *state.Store, codes *pairing.Codes, backend player.Backend, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{cfg: cfg, id: id, state: st, codes: codes, control: newControl(backend), backend: backend, log: logger}
	s.card = &card{s: s}
	backend.OnMessage(s.control.fromMPV)
	// mpv came back after a drop: drop the client so it re-subscribes against
	// the fresh instance (which has none of its observers), and redraw the
	// pairing card, which the fresh instance does not have either.
	backend.OnMPVReconnect(func() {
		s.control.disconnect()
		s.card.forget()
	})
	return s
}

// OnPairingChange registers fn, called after the player is paired or unpaired.
// Call before Run.
func (s *Server) OnPairingChange(fn func(paired bool)) { s.onPairing = fn }

// Paired reports whether a Cinefin has paired with this player.
func (s *Server) Paired() bool { return s.state.Paired() }

// Address is the URL Cinefin reaches this player at, as shown on the pairing
// card and the status page.
func (s *Server) Address() string { return s.pairingAddress() }

// ControlConnected reports whether a /ws/control client is attached (used by the
// desktop tray to show the Cinefin link state).
func (s *Server) ControlConnected() bool { return s.control.connected() }

// Handler returns the agent's HTTP handler with all routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /pair", s.handlePair)
	mux.Handle("POST /unpair", s.auth(http.HandlerFunc(s.handleUnpair)))
	mux.Handle("GET /status", s.auth(http.HandlerFunc(s.handleStatus)))
	mux.Handle("GET /ws/control", s.auth(http.HandlerFunc(s.handleControl)))
	mux.Handle("GET /hostconfig", s.auth(http.HandlerFunc(s.handleGetHostConfig)))
	// The launch config (graphics/audio/autostart) is set by Cinefin and kept in
	// the agent's state file. The narrower idle-media PUT rewrites only the
	// Cinefin-owned cinema ident.
	mux.Handle("PUT /hostconfig", s.auth(http.HandlerFunc(s.handlePutHostConfig)))
	mux.Handle("PUT /hostconfig/idle-media", s.auth(http.HandlerFunc(s.handlePutIdleMedia)))
	mux.Handle("GET /hardware", s.auth(http.HandlerFunc(s.handleHardware)))
	mux.Handle("POST /mpv/start", s.auth(http.HandlerFunc(s.handleMPVStart)))
	mux.Handle("POST /mpv/stop", s.auth(http.HandlerFunc(s.handleMPVStop)))
	mux.Handle("POST /mpv/restart", s.auth(http.HandlerFunc(s.handleMPVRestart)))
	// The loopback-only /ui status page (opened in the browser from the tray).
	s.registerPanel(mux)
	return mux
}

// auth enforces the bearer token. With no token set, every request is refused.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := s.state.Token()
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		want := "Bearer " + tok
		if tok == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
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
		"id":      s.id.ID,
		"name":    s.id.Name,
		"paired":  s.state.Paired(),
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

	// Lift the library's 32 KiB read limit: mpv command frames (an inline
	// playlist, long streaming URLs) can exceed it, and hitting it closes the
	// connection — surfacing as "fails to load". The client is single and authed.
	conn.SetReadLimit(-1)

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
	addr := net.JoinHostPort(s.cfg.Listen, strconv.Itoa(s.cfg.Port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go s.card.run(ctx)

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
