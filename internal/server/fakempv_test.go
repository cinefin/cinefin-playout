//go:build !windows

package server

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeMPV is an in-test unix socket that speaks a tiny slice of mpv JSON-IPC.
// A single connection is accepted; each command frame gets a success reply
// echoing the request_id, and observe_property additionally emits a
// property-change event so tests can assert event delivery.
type fakeMPV struct {
	path string
	ln   net.Listener

	mu    sync.Mutex
	conns []net.Conn
}

// shortSocketPath returns a socket path that fits the ~104-byte sun_path limit
// on macOS/BSD — t.TempDir() embeds the test name, which can push it over.
func shortSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cp3-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func newFakeMPV(t *testing.T) *fakeMPV {
	t.Helper()
	path := shortSocketPath(t, "mpv.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen fake mpv: %v", err)
	}
	f := &fakeMPV{path: path, ln: ln}
	go f.acceptLoop()
	t.Cleanup(func() {
		f.Close()
		_ = os.Remove(path)
	})
	return f
}

func (f *fakeMPV) acceptLoop() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, conn)
		f.mu.Unlock()
		go f.handle(conn)
	}
}

func (f *fakeMPV) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			f.respond(conn, line)
		}
		if err != nil {
			return
		}
	}
}

func (f *fakeMPV) respond(conn net.Conn, line []byte) {
	var req struct {
		Command   []any `json:"command"`
		RequestID *int  `json:"request_id"`
	}
	if json.Unmarshal(line, &req) != nil {
		return
	}

	// Reply to the command.
	reply := map[string]any{"error": "success"}
	if req.RequestID != nil {
		reply["request_id"] = *req.RequestID
	}
	if len(req.Command) > 0 {
		if name, _ := req.Command[0].(string); name == "get_property" {
			reply["data"] = "fake-reply"
		}
	}
	writeFrame(conn, reply)

	// If this was observe_property, follow up with a property-change event.
	if len(req.Command) > 0 {
		if name, _ := req.Command[0].(string); name == "observe_property" {
			propName := ""
			if len(req.Command) >= 3 {
				propName, _ = req.Command[2].(string)
			}
			writeFrame(conn, map[string]any{
				"event": "property-change",
				"name":  propName,
				"data":  42.0,
			})
		}
	}
}

func writeFrame(conn net.Conn, v any) {
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	_, _ = conn.Write(b)
}

func (f *fakeMPV) Close() {
	_ = f.ln.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.mu.Unlock()
}
