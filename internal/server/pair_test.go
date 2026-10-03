//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/pairing"
	"github.com/cinefin/cinefin-playout/internal/player"
	"github.com/cinefin/cinefin-playout/internal/state"
)

// fakePlayer is a Backend that records the frames sent to mpv and counts
// restarts. The embedded interface is nil: an unexpected call panics.
type fakePlayer struct {
	player.Backend

	mu        sync.Mutex
	connected bool
	sent      []string
	restarts  int
}

func (f *fakePlayer) OnMessage(func([]byte))               {}
func (f *fakePlayer) OnMPVReconnect(func())                {}
func (f *fakePlayer) Probe(context.Context) (string, bool) { return "", false }
func (f *fakePlayer) Status() player.Status                { return player.Status{} }

func (f *fakePlayer) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakePlayer) Send(frame []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, string(frame))
	return nil
}

func (f *fakePlayer) Restart(bool) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return true, "restarted"
}

func (f *fakePlayer) frames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// newPairTestServer builds an unpaired Server over a fakePlayer.
func newPairTestServer(t *testing.T) (*Server, *httptest.Server, *fakePlayer, *pairing.Codes) {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	st, err := state.Open(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakePlayer{connected: true}
	codes := pairing.New()
	srv := New(cfg, Identity{ID: "abc123", Name: "Booth"}, st, codes, fp, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, fp, codes
}

func post(t *testing.T, url, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func getStatus(t *testing.T, base, token string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Pairing: a wrong code is refused, the right one returns a token that then
// authenticates, and a paired player refuses to pair again.
func TestPairFlow(t *testing.T) {
	srv, ts, _, codes := newPairTestServer(t)
	var changes []bool
	srv.OnPairingChange(func(p bool) { changes = append(changes, p) })

	code, _ := codes.Current()
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if resp, _ := post(t, ts.URL+"/pair", "", map[string]string{"code": wrong}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong code: %d, want 403", resp.StatusCode)
	}

	time.Sleep(1100 * time.Millisecond) // one attempt per second
	code, _ = codes.Current()           // the wrong attempt rotated it
	resp, out := post(t, ts.URL+"/pair", "", map[string]string{"code": pairing.Format(code)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("right code: %d %v", resp.StatusCode, out)
	}
	tok, _ := out["token"].(string)
	if tok == "" || out["id"] != "abc123" || out["name"] != "Booth" {
		t.Fatalf("pair response = %v", out)
	}
	if got := getStatus(t, ts.URL, tok); got != http.StatusOK {
		t.Errorf("/status with the issued token = %d", got)
	}
	if len(changes) != 1 || !changes[0] {
		t.Errorf("pairing change callbacks = %v", changes)
	}

	time.Sleep(1100 * time.Millisecond)
	code, _ = codes.Current()
	if resp, _ := post(t, ts.URL+"/pair", "", map[string]string{"code": code}); resp.StatusCode != http.StatusConflict {
		t.Errorf("pairing a paired player: %d, want 409", resp.StatusCode)
	}
}

// Unpairing revokes the token and restarts the player onto the pairing card.
func TestUnpair(t *testing.T) {
	srv, ts, fp, _ := newPairTestServer(t)
	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := post(t, ts.URL+"/unpair", "tok", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/unpair = %d", resp.StatusCode)
	}
	if srv.Paired() {
		t.Error("still paired after /unpair")
	}
	if got := getStatus(t, ts.URL, "tok"); got != http.StatusUnauthorized {
		t.Errorf("old token after unpair: %d, want 401", got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		fp.mu.Lock()
		n := fp.restarts
		fp.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("player restarts = %d, want 1", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The card shows the code and address while unpaired and is removed on pairing.
func TestCardDrawAndRemove(t *testing.T) {
	srv, _, fp, codes := newPairTestServer(t)
	srv.card.refresh()
	srv.card.refresh() // unchanged text: no second draw
	frames := fp.frames()
	code, _ := codes.Current()
	if len(frames) != 1 || !strings.Contains(frames[0], `"format":"ass-events"`) ||
		!strings.Contains(frames[0], pairing.Format(code)) || !strings.Contains(frames[0], srv.Address()) {
		t.Fatalf("card frames = %v", frames)
	}

	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	srv.card.refresh()
	frames = fp.frames()
	if len(frames) != 2 || !strings.Contains(frames[1], `"format":"none"`) {
		t.Errorf("card not removed after pairing: %v", frames)
	}
}

// Replies to the card's own commands never reach Cinefin.
func TestCardRepliesFiltered(t *testing.T) {
	c := newControl(&fakeLink{connected: true})
	var got []string
	c.attach(func(b []byte) { got = append(got, string(b)) }, nil)
	c.fromMPV([]byte(`{"request_id":2000000001,"error":"success"}`))
	c.fromMPV([]byte(`{"request_id":7,"error":"success"}`))
	if len(got) != 1 || !strings.Contains(got[0], `"request_id":7`) {
		t.Errorf("forwarded = %v", got)
	}
}
