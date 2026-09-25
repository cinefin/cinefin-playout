//go:build !windows

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The loopback control panel serves its page and a state feed without a bearer
// token (httptest connects over loopback, which the gate allows).
func TestPanelServesPageAndState(t *testing.T) {
	base, cfg := newHostTestServer(t, "/tmp/does-not-exist-x.sock")

	page, err := http.Get(base + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui = %d", page.StatusCode)
	}
	buf := make([]byte, 256)
	n, _ := page.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "<!DOCTYPE html>") {
		t.Errorf("GET /ui did not return the panel page")
	}

	st, err := http.Get(base + "/ui/state")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Body.Close()
	var s struct {
		Token            string `json:"token"`
		Address          string `json:"address"`
		PlayerRunning    bool   `json:"player_running"`
		CinefinConnected bool   `json:"cinefin_connected"`
	}
	if err := json.NewDecoder(st.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Token != cfg.Token {
		t.Errorf("state token = %q, want %q", s.Token, cfg.Token)
	}
	if !strings.HasPrefix(s.Address, "http://") {
		t.Errorf("state address = %q", s.Address)
	}
}

// The page is served at /ui (no trailing slash), so its fetches MUST be
// absolute — a relative fetch("state") would resolve to /state and 404, leaving
// the panel stuck "offline" with dead controls. Guard against that regression.
func TestPanelUsesAbsolutePaths(t *testing.T) {
	base, _ := newHostTestServer(t, "/tmp/does-not-exist-x.sock")
	resp := do(t, "GET", base+"/ui", nil)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{`fetch("/ui/state"`, `fetch("/ui/player/"`} {
		if !strings.Contains(page, want) {
			t.Errorf("panel page missing absolute path %q (relative paths break when served at /ui)", want)
		}
	}
	if strings.Contains(page, `fetch("state"`) || strings.Contains(page, `fetch("player/`) {
		t.Error("panel page uses a relative fetch path; it must be absolute (/ui/...)")
	}
}
