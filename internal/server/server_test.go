//go:build !windows

package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"git.kef2.net/micky/cinefin-playout/internal/config"
	"git.kef2.net/micky/cinefin-playout/internal/hostconfig"
	"git.kef2.net/micky/cinefin-playout/internal/player"
)

// newServerForTest builds a full Server backed by a running subprocess player
// pointed at cfg.IPCSocket. hcOf may be nil (a headless, non-autostarting linux
// preset is used). The backend's monitor runs but never spawns mpv unless a test
// calls /mpv/start, so it is safe against a bad or fake socket.
func newServerForTest(t *testing.T, cfg config.Config, hcOf player.HostConfigProvider) *httptest.Server {
	t.Helper()
	if hcOf == nil {
		hcOf = func() hostconfig.HostConfig {
			hc := hostconfig.Preset("linux")
			hc.Autostart = false
			return hc
		}
	}
	backend := player.New(cfg, hcOf, log.New(io.Discard, "", 0))
	srv := New(cfg, backend, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	backend.Run(ctx)
	t.Cleanup(func() { cancel(); backend.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// newTestServer wires a Server against the given mpv socket path and token,
// returning an httptest server and its base URL.
func newTestServer(t *testing.T, socketPath, token string) (*httptest.Server, string) {
	t.Helper()
	cfg := config.Default()
	cfg.IPCSocket = socketPath
	cfg.Token = token
	cfg.StateDir = t.TempDir()
	ts := newServerForTest(t, cfg, nil)
	return ts, ts.URL
}

func TestHealthUnauth(t *testing.T) {
	ts, base := newTestServer(t, "/tmp/does-not-exist-mpv.sock", "secret")
	_ = ts

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("health status field = %v", body["status"])
	}
	if body["os"] == nil || body["arch"] == nil || body["version"] == nil {
		t.Fatalf("health missing os/arch/version: %v", body)
	}
}

func TestStatusAuth(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		setHeader  bool
		want       int
	}{
		{"no token", "", false, http.StatusUnauthorized},
		{"wrong token", "Bearer nope", true, http.StatusUnauthorized},
		{"right token", "Bearer secret", true, http.StatusOK},
	}
	ts, base := newTestServer(t, "/tmp/does-not-exist-mpv.sock", "secret")
	_ = ts

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", base+"/status", nil)
			if tc.setHeader {
				req.Header.Set("Authorization", tc.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status code = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestStatusReportsMPVReachable(t *testing.T) {
	fake := newFakeMPV(t)
	ts, base := newTestServer(t, fake.path, "secret")
	_ = ts

	req, _ := http.NewRequest("GET", base+"/status", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		MPV struct {
			IPCSocket string `json:"ipc_socket"`
			Reachable bool   `json:"reachable"`
		} `json:"mpv"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if !body.MPV.Reachable {
		t.Fatalf("expected mpv reachable via fake socket")
	}
	if body.MPV.IPCSocket != fake.path {
		t.Fatalf("ipc_socket = %q, want %q", body.MPV.IPCSocket, fake.path)
	}
}

func wsURL(base string) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/ws/control"
}

func dialControl(t *testing.T, base, token string) (*websocket.Conn, *http.Response) {
	t.Helper()
	opts := &websocket.DialOptions{HTTPHeader: http.Header{}}
	if token != "" {
		opts.HTTPHeader.Set("Authorization", "Bearer "+token)
	}
	conn, resp, err := websocket.Dial(context.Background(), wsURL(base), opts)
	if err != nil && resp == nil {
		t.Fatalf("ws dial: %v", err)
	}
	return conn, resp
}

func TestControlWSAuthRejected(t *testing.T) {
	ts, base := newTestServer(t, "/tmp/does-not-exist-mpv.sock", "secret")
	_ = ts

	// Wrong token → handshake should fail with 401.
	_, resp := dialControl(t, base, "wrong")
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("ws with wrong token status = %d, want 401", got)
	}
}

func TestControlWSRoundTrip(t *testing.T) {
	fake := newFakeMPV(t)
	ts, base := newTestServer(t, fake.path, "secret")
	_ = ts

	// Give the mpv client a moment to connect.
	waitConnected(t, base, "secret")

	conn, resp := dialControl(t, base, "secret")
	if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("ws handshake status = %d", resp.StatusCode)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Send a command; expect a success reply with our request_id restored.
	cmd := `{"command":["get_property","mpv-version"],"request_id":99}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(cmd)); err != nil {
		t.Fatal(err)
	}
	reply := readFrame(t, ctx, conn)
	if got := reply["request_id"]; got != float64(99) {
		t.Fatalf("reply request_id = %v, want 99", got)
	}
	if reply["error"] != "success" {
		t.Fatalf("reply error = %v, want success", reply["error"])
	}

	// observe_property → fake mpv follows the reply with a property-change event.
	obs := `{"command":["observe_property",1,"time-pos"],"request_id":100}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(obs)); err != nil {
		t.Fatal(err)
	}
	// Next two frames: the reply (id 100) then a property-change event.
	var sawEvent bool
	for i := 0; i < 3 && !sawEvent; i++ {
		f := readFrame(t, ctx, conn)
		if f["event"] == "property-change" && f["name"] == "time-pos" {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Fatalf("did not receive property-change event")
	}
}

func waitConnected(t *testing.T, base, token string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", base+"/status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			var body struct {
				MPV struct {
					Reachable bool `json:"reachable"`
				} `json:"mpv"`
			}
			json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if body.MPV.Reachable {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("mpv never became reachable")
}

func readFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("frame not JSON (%s): %v", data, err)
	}
	return m
}
