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
func (f *fakePlayer) OnMPVConnect(func(bool))              {}
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

// The confirmation starts from the pairing box that was on screen: the same
// code, address and countdown, so its first frame is the same text and is not
// even sent again.
func TestCardConfirmStartsFromThePairingBox(t *testing.T) {
	srv, _, fp, codes := newPairTestServer(t)
	srv.card.refresh()
	code, _ := codes.Current()
	srv.card.startConfirm("10.0.0.2", code)
	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	srv.card.refresh()
	if frames := fp.frames(); len(frames) != 1 || srv.card.key != "confirm" {
		t.Errorf("scene %q, frames = %v; want the confirmation, drawn as the box already shown", srv.card.key, frames)
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

// The card counts down in whole minutes, rounding up, and never below 1.
func TestCardCountdown(t *testing.T) {
	if got := minutesLeft(4*time.Minute + 10*time.Second); got != 5 {
		t.Errorf("minutesLeft(4m10s) = %d, want 5", got)
	}
	if got := minutesLeft(-time.Second); got != 1 {
		t.Errorf("minutesLeft(expired) = %d, want 1", got)
	}
	if !strings.Contains(pairingBox("340 957", "http://x:8089", "", 150*time.Second, 1), "new code in 3 min") {
		t.Error("countdown text missing")
	}
}

// The pairing scene is the box (fill, border, divider) with the code, the
// instructions, the address with its markup stripped, and a countdown bar
// whose fill is the share of the code's life left. It redraws within a few
// seconds, and as the code expires.
func TestPairingBox(t *testing.T) {
	half := pairing.CodeLifetime / 2
	sc := pairingScene{code: "340 957", address: `http://{x}\:8089`, expires: time.Now().Add(half)}
	text, next, done := sc.frame(0)
	if done || next <= 0 || next > pairingRedraw {
		t.Errorf("frame: next=%v done=%v", next, done)
	}
	for _, want := range []string{
		rect(boxX, boxY, boxW, boxH, boxFill, boxFillOpacity),
		`\1c&H121010&\1a&H40&`,
		border(boxX, boxY, boxW, boxH, boxLine, 1),
		vline(dividerX, boxY+boxPadY, boxH-2*boxPadY, boxLine, 1),
		"PAIRING CODE",
		"340 957",
		`{\b1}Settings › Playout{\b0}`,
		"http://x:8089",
		"m 0 0 l 220 0 220 4 0 4", // the bar's track
		"m 0 0 l 109 0 109 4 0 4", // half of it filled (the clock ran on a little)
		"new code in 3 min",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pairing box lacks %q:\n%s", want, text)
		}
	}

	sc.expires = time.Now().Add(2 * time.Second)
	if _, next, _ := sc.frame(0); next > 3*time.Second {
		t.Errorf("next = %v, want a redraw as the code expires", next)
	}
	if text := pairingBox("1", "a", "", 0, 1); strings.Contains(text, "&HFF7B3A&") {
		t.Error("an expired code should have an empty bar")
	}
}

// A player upgraded from a 0.1 release with a config.toml says so in one more
// line under the countdown; the right column moves up half a row to stay
// centred, and the rest of the box is unchanged.
func TestPairingBoxLegacyNotice(t *testing.T) {
	plain := pairingBox("340 957", "http://x:8089", "", 150*time.Second, 1)
	with := pairingBox("340 957", "http://x:8089", legacyConfigNotice, 150*time.Second, 1)
	if strings.Contains(plain, "config.toml") {
		t.Error("the notice shows without a legacy config")
	}
	up := noticeRow / 2
	for _, want := range []string{
		rect(boxX, boxY, boxW, boxH, boxFill, boxFillOpacity),
		border(boxX, boxY, boxW, boxH, boxLine, 1),
		textAt(7, rightX, instrY-up, 22, textBright, "", `In Cinefin, go to {\b1}Settings › Playout{\b0}\Nand enter this code`),
		textAt(7, rightX, noticeY-up, 14, textMuted, "", legacyConfigNotice),
	} {
		if !strings.Contains(with, want) {
			t.Errorf("pairing box with the notice lacks %q:\n%s", want, with)
		}
	}
	// The column stays inside the box's padding.
	if top, bottom := instrY-up, noticeY-up+14; top < boxY+boxPadY || bottom > boxY+boxH-boxPadY {
		t.Errorf("right column spans %d-%d, outside %d-%d", top, bottom, boxY+boxPadY, boxY+boxH-boxPadY)
	}
}

// The card adds the notice when the agent found a 0.1 config, and the paired
// confirmation starts from that same box.
func TestCardPairingNotice(t *testing.T) {
	srv, _, _, _ := newPairTestServer(t)
	c := srv.card
	c.mu.Lock()
	if sc := c.pairingScene().(pairingScene); sc.notice != "" {
		t.Errorf("notice = %q with no legacy config", sc.notice)
	}
	srv.cfg.LegacyConfig = "/etc/cinefin-playout/config.toml"
	sc := c.pairingScene().(pairingScene)
	c.mu.Unlock()
	if sc.notice != legacyConfigNotice {
		t.Errorf("notice = %q, want %q", sc.notice, legacyConfigNotice)
	}

	c.startConfirm("10.0.0.2", "340957")
	c.mu.Lock()
	confirm := c.confirm
	c.mu.Unlock()
	text, _ := confirmAt(t, confirm, 0)
	if want := pairingBox(confirm.code, confirm.address, legacyConfigNotice, confirm.left, 1); text != want {
		t.Errorf("confirmation's first frame differs from the pairing box:\n got %s\nwant %s", text, want)
	}
}

func TestAssColour(t *testing.T) {
	if got := assColour("#3a7bff"); got != "&HFF7B3A&" {
		t.Errorf("assColour = %q", got)
	}
	if got := assAlpha(0.9); got != "&H1A&" {
		t.Errorf("assAlpha(0.9) = %q", got)
	}
	if got := assAlpha(1); got != "&H00&" {
		t.Errorf("assAlpha(1) = %q", got)
	}
}
