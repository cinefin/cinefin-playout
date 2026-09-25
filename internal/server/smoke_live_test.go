//go:build !windows && livesmoke

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestLiveSmoke runs against a REAL mpv listening at $CINEFIN_SMOKE_SOCKET
// (default /tmp/mpvsocket). Enable with: go test -tags livesmoke ./internal/server
func TestLiveSmoke(t *testing.T) {
	sock := os.Getenv("CINEFIN_SMOKE_SOCKET")
	if sock == "" {
		sock = "/tmp/mpvsocket"
	}
	if _, err := os.Stat(sock); err != nil {
		t.Skipf("no mpv socket at %s: %v", sock, err)
	}

	ts, base := newTestServer(t, sock, "smoke-token")
	_ = ts
	waitConnected(t, base, "smoke-token")

	conn, resp := dialControl(t, base, "smoke-token")
	if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d", resp.StatusCode)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. get_property mpv-version → success reply, request_id 1.
	conn.Write(ctx, websocket.MessageText, []byte(`{"command":["get_property","mpv-version"],"request_id":1}`))
	reply := readFrame(t, ctx, conn)
	t.Logf("reply: %v", reply)
	if reply["request_id"] != float64(1) || reply["error"] != "success" {
		t.Fatalf("unexpected reply: %v", reply)
	}
	t.Logf("LIVE mpv-version: %v", reply["data"])

	// 2. observe_property time-pos, then trigger a change and expect an event.
	conn.Write(ctx, websocket.MessageText, []byte(`{"command":["observe_property",1,"time-pos"],"request_id":2}`))
	// consume the observe reply
	readFrame(t, ctx, conn)

	// Setting a property that changes generates a property-change. Load nothing;
	// instead set pause to force at least a property-change on the observed one
	// is not guaranteed, so observe a reliably-changing property: seek won't work
	// while idle. Observe "pause" and toggle it — that always fires.
	conn.Write(ctx, websocket.MessageText, []byte(`{"command":["observe_property",2,"pause"],"request_id":3}`))
	readFrame(t, ctx, conn) // observe reply
	conn.Write(ctx, websocket.MessageText, []byte(`{"command":["set_property","pause",true],"request_id":4}`))

	var sawEvent bool
	for i := 0; i < 6 && !sawEvent; i++ {
		f := readFrame(t, ctx, conn)
		t.Logf("frame: %v", f)
		if f["event"] == "property-change" {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Fatalf("no property-change event received from live mpv")
	}
	_ = json.Marshal
}
