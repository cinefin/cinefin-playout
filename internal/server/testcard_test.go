//go:build !windows

package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/hardware"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
	"github.com/cinefin/cinefin-playout/internal/ident"
)

func TestTestCardAndSoundNeedAuth(t *testing.T) {
	_, ts, _, _ := newPairTestServer(t)
	for _, path := range []string{"/testcard", "/testsound"} {
		if resp, _ := post(t, ts.URL+path, "wrong", map[string]any{"on": true}); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with a wrong token: %d, want 401", path, resp.StatusCode)
		}
	}
}

// chosen is the scene the card would show now.
func chosen(srv *Server) scene {
	srv.card.mu.Lock()
	defer srv.card.mu.Unlock()
	return srv.card.choose()
}

// POST /testcard turns the card on and off; /status reports it; it shows over
// the paired confirmation but not over the pairing box.
func TestTestCardOnOff(t *testing.T) {
	srv, _, call := pairedStandbyServer(t)
	if code, _ := call("POST", "/testcard", map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("no on: %d, want 400", code)
	}
	code, out := call("POST", "/testcard", map[string]any{"on": true})
	if code != http.StatusOK || out["on"] != true || out["off_in_s"] != float64(300) {
		t.Fatalf("on: %d %v", code, out)
	}
	srv.card.startConfirm("10.0.0.2", "123456")
	sc, ok := chosen(srv).(testCardScene)
	if !ok || sc.name != "Booth" {
		t.Fatalf("chosen %#v, want the test card for Booth", chosen(srv))
	}
	if _, st := call("GET", "/status", nil); st["test_card"].(map[string]any)["on"] != true {
		t.Errorf("/status test_card = %v", st["test_card"])
	}

	// The standby spec's player name wins over the agent's.
	call("PUT", "/standby", spec("http://127.0.0.1:1/x", strings.Repeat("a", 64), ""))
	if sc := chosen(srv).(testCardScene); sc.name != "Screen 1" {
		t.Errorf("name %q, want the spec's Screen 1", sc.name)
	}

	// Unpaired: the pairing box wins.
	tok := srv.state.Token()
	srv.state.SetToken("")
	if _, ok := chosen(srv).(pairingScene); !ok {
		t.Errorf("unpaired: chose %T, want the pairing box", chosen(srv))
	}
	srv.state.SetToken(tok)

	if code, out := call("POST", "/testcard", map[string]any{"on": false}); code != http.StatusOK || out["on"] != false {
		t.Fatalf("off: %d %v", code, out)
	}
	if _, ok := chosen(srv).(testCardScene); ok {
		t.Error("test card still chosen after off")
	}
}

func TestTestCardTurnsItselfOff(t *testing.T) {
	srv, _, call := pairedStandbyServer(t)
	call("POST", "/testcard", map[string]any{"on": true})
	srv.card.mu.Lock()
	srv.card.testUntil = time.Now().Add(-time.Millisecond) // 5 minutes later
	srv.card.mu.Unlock()
	if _, ok := chosen(srv).(testCardScene); ok {
		t.Error("test card still chosen after 5 minutes")
	}
	if _, st := call("GET", "/status", nil); st["test_card"].(map[string]any)["on"] != false {
		t.Errorf("/status test_card = %v", st["test_card"])
	}
}

func TestUnpairTurnsTheTestCardOff(t *testing.T) {
	srv, _, call := pairedStandbyServer(t)
	call("POST", "/testcard", map[string]any{"on": true})
	if err := srv.Unpair(); err != nil {
		t.Fatal(err)
	}
	if srv.card.testCardLeft() != 0 {
		t.Error("test card on after unpair")
	}
}

func TestTestCardText(t *testing.T) {
	got := testCardText("Screen {1}", "HDMI-A-1 · 1920 × 1080 · 60 Hz", "")
	lines := strings.Split(got, "\n")
	if lines[0] != rect(0, 0, 1280, 720, "#000000", 1) {
		t.Errorf("first event %q, want the full-screen black cover", lines[0])
	}
	for _, want := range []string{
		"TEST CARD",
		`\b300}Screen 1`, // markup stripped from the name
		"HDMI-A-1 · 1920 × 1080 · 60 Hz",
		rect(200, 420, 125, 44, "#bfbfbf", 1),
		rect(954, 420, 126, 44, "#0000bf", 1),
		rect(200, 470, 80, 36, "#000000", 1),
		rect(1000, 470, 80, 36, "#ffffff", 1),
		border(200, 470, 880, 36, "#34343b", 1), // the ramp's outline, in line with the bars
		border(64, 36, 1152, 648, "#34343b", 1),
		rect(16, 16, 56, 3, "#ffffff", 1),
		rect(1261, 648, 3, 56, "#ffffff", 1),
		"}Left", "}Right",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("test card lacks %q", want)
		}
	}
	if strings.Contains(got, assColour(tcSpeakerOn)) {
		t.Error("a speaker box is highlighted with no sound playing")
	}
	if strings.Contains(testCardText("A", "", ""), "· ") {
		t.Error("an unknown output still drew a line")
	}

	// Left sounding: only the left box is highlighted.
	left := testCardText("A", "", "left")
	if !strings.Contains(left, border(96, 560, 200, 56, tcSpeakerOn, 1)) ||
		strings.Contains(left, border(984, 560, 200, 56, tcSpeakerOn, 1)) ||
		!strings.Contains(left, border(984, 560, 200, 56, tcSpeakerLine, 1)) {
		t.Errorf("left sounding:\n%s", left)
	}
	right := testCardText("A", "", "right")
	if !strings.Contains(right, border(984, 560, 200, 56, tcSpeakerOn, 1)) ||
		strings.Contains(right, border(96, 560, 200, 56, tcSpeakerOn, 1)) {
		t.Errorf("right sounding:\n%s", right)
	}
}

func TestFitName(t *testing.T) {
	if name, size := fitName("Screen 1"); name != "Screen 1" || size != tcNameSize {
		t.Errorf("short name: %q %d", name, size)
	}
	name, size := fitName(strings.Repeat("W", 80))
	if n := len([]rune(name)); n != tcNameMaxRunes || !strings.HasSuffix(name, "…") || size != tcNameMinSize {
		t.Errorf("long name: %q (%d runes) at %d", name, n, size)
	}
	if _, size := fitName("The Grand Picture Palace Auditorium Two"); size >= tcNameSize || size <= tcNameMinSize {
		t.Errorf("medium name at %d, want between", size)
	}
}

func TestOutputLine(t *testing.T) {
	screens := []hardware.Screen{
		{Index: 0, Name: "HDMI-A-1", W: 1920, H: 1080, Hz: 60},
		{Index: 1, Name: "DP-2", W: 3840, H: 2160, Hz: 23.976},
	}
	drm := hostconfig.PresetDRM("DP-2")
	desktop := hostconfig.Preset("linux")
	for _, c := range []struct {
		name      string
		hc        func() hostconfig.HostConfig
		screens   []hardware.Screen
		connected []string
		want      string
	}{
		{"drm connector", func() hostconfig.HostConfig { return drm }, screens, nil, "DP-2 · 3840 × 2160 · 23.98 Hz"},
		{"drm pinned mode", func() hostconfig.HostConfig { hc := drm; hc.Graphics.DRMMode = "1920x1080@50"; return hc }, screens, nil, "DP-2 · 1920 × 1080 · 50 Hz"},
		{"drm first connected", func() hostconfig.HostConfig { hc := drm; hc.Graphics.DRMConnector = ""; return hc }, nil, []string{"HDMI-A-2"}, "HDMI-A-2"},
		{"desktop index", func() hostconfig.HostConfig { hc := desktop; hc.Graphics.Screen = 1; return hc }, screens, nil, "DP-2 · 3840 × 2160 · 23.98 Hz"},
		{"desktop name", func() hostconfig.HostConfig { hc := desktop; hc.Graphics.ScreenName = "HDMI-A-1"; return hc }, screens, nil, "HDMI-A-1 · 1920 × 1080 · 60 Hz"},
		{"desktop unknown", func() hostconfig.HostConfig { return desktop }, nil, nil, "Screen 1"},
		{"windows", func() hostconfig.HostConfig { return desktop }, []hardware.Screen{{Name: `\\.\DISPLAY1`, W: 2560, H: 1440}}, nil, "DISPLAY1 · 2560 × 1440"},
	} {
		if got := outputLine(c.hc(), c.screens, c.connected); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// onStandbyNow puts the paired server's fake mpv on standby: the agent loads
// the ident and mpv reports it as its path.
func onStandbyNow(t *testing.T, srv *Server) string {
	t.Helper()
	if err := srv.enterStandby(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(srv.cfg.StateDir, ident.FileName)
	mpvPath(srv, path)
	if !srv.standby.onStandby() {
		t.Fatal("not on standby")
	}
	return path
}

// mpvPath delivers a change of mpv's path ("" = nothing loaded) on the agent's
// observer.
func mpvPath(srv *Server, path string) {
	data := `null`
	if path != "" {
		data = `"` + path + `"`
	}
	// The tone's URL holds no characters that need escaping in JSON.
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000003,"name":"path","data":` + data + `}`))
}

func mpvTime(srv *Server, pos string) {
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000004,"name":"playback-time","data":` + pos + `}`))
}

// The test sound is refused off standby with the test card off, and while one
// is playing; on standby it loads the tone and replies with its timing.
func TestTestSoundRules(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	mpvPath(srv, "/media/feature.mkv")
	if code, _ := call("POST", "/testsound", nil); code != http.StatusConflict {
		t.Fatalf("during a programme: %d, want 409", code)
	}

	onStandbyNow(t, srv)
	n := len(fp.frames())
	code, out := call("POST", "/testsound", nil)
	if code != http.StatusOK || out["duration_ms"] != float64(3000) || out["frequency_hz"] != float64(440) {
		t.Fatalf("on standby: %d %v", code, out)
	}
	seq := out["sequence"].([]any)
	if r := seq[1].(map[string]any); len(seq) != 2 || r["channel"] != "right" || r["start_ms"] != float64(1500) || r["duration_ms"] != float64(1500) {
		t.Errorf("sequence %v", seq)
	}
	want := []string{
		`{"command":["unobserve_property",2000000004],"request_id":2000000001}`,
		`{"command":["observe_property",2000000004,"playback-time"],"request_id":2000000001}`,
		string(agentCommand("loadfile", toneURL, "replace", -1, toneOptions)),
		`{"command":["set_property","pause",false],"request_id":2000000001}`,
	}
	if got := fp.frames()[n:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("sent %v\nwant %v", got, want)
	}

	if code, _ := call("POST", "/testsound", nil); code != http.StatusConflict {
		t.Errorf("while playing: %d, want 409", code)
	}
	if _, st := call("GET", "/status", nil); st["test_sound"].(map[string]any)["playing"] != true {
		t.Errorf("/status test_sound = %v", st["test_sound"])
	}
}

// With the test card on, the test sound plays even off standby.
func TestTestSoundWithTestCard(t *testing.T) {
	srv, _, call := pairedStandbyServer(t)
	mpvPath(srv, "/media/feature.mkv")
	call("POST", "/testcard", map[string]any{"on": true})
	if code, _ := call("POST", "/testsound", nil); code != http.StatusOK {
		t.Fatalf("with the test card: %d, want 200", code)
	}
}

func TestTestSoundNeedsMPV(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	onStandbyNow(t, srv)
	fp.mu.Lock()
	fp.connected = false
	fp.mu.Unlock()
	if code, _ := call("POST", "/testsound", nil); code != http.StatusServiceUnavailable {
		t.Errorf("mpv down: %d, want 503", code)
	}
}

// waitFrame waits for a frame containing want among those sent after n.
func waitFrame(t *testing.T, fp *fakePlayer, n int, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range fp.frames()[n:] {
			if strings.Contains(f, want) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no frame with %q in %v", want, fp.frames()[n:])
}

// The card lights the side mpv is playing, and the player returns to standby
// when the tone ends.
func TestTestSoundHighlightAndReturn(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	identPath := onStandbyNow(t, srv)
	call("POST", "/testcard", map[string]any{"on": true})
	call("POST", "/testsound", nil)

	sounding := func() string { return chosen(srv).(testCardScene).sounding }
	mpvPath(srv, "") // between the ident and the tone: not the end
	if !srv.tone.playing() {
		t.Fatal("the gap before the tone ended it")
	}
	mpvPath(srv, toneURL)
	if got := sounding(); got != "" {
		t.Errorf("before a position: %q, want none", got)
	}
	mpvTime(srv, "0.2")
	if got := sounding(); got != "left" {
		t.Errorf("at 0.2 s: %q, want left", got)
	}
	mpvTime(srv, "1.7")
	if got := sounding(); got != "right" {
		t.Errorf("at 1.7 s: %q, want right", got)
	}
	if _, st := call("GET", "/status", nil); st["test_sound"].(map[string]any)["channel"] != "right" {
		t.Errorf("/status test_sound = %v", st["test_sound"])
	}

	n := len(fp.frames())
	mpvPath(srv, "") // the tone ended; mpv is idle
	waitFrame(t, fp, n, `"loadfile","`+identPath)
	waitFrame(t, fp, n, `"unobserve_property",2000000004`)
	if srv.tone.playing() || sounding() != "" {
		t.Error("the test sound still playing after it ended")
	}
}

// Cinefin loading a file during the tone ends the test sound without standby.
func TestTestSoundInterrupted(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	onStandbyNow(t, srv)
	call("POST", "/testsound", nil)
	mpvPath(srv, toneURL)
	n := len(fp.frames())
	mpvPath(srv, "/media/trailer.mkv")
	waitFrame(t, fp, n, `"unobserve_property",2000000004`)
	for _, f := range fp.frames()[n:] {
		if strings.Contains(f, "loadfile") {
			t.Errorf("returned to standby over Cinefin's file: %s", f)
		}
	}
	if srv.tone.playing() {
		t.Error("still playing")
	}
}

// When mpv never reports the end, the timeout returns the player to standby.
func TestTestSoundTimeout(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	identPath := onStandbyNow(t, srv)
	call("POST", "/testsound", nil)
	mpvPath(srv, toneURL)
	n := len(fp.frames())
	srv.toneTimedOut(srv.tone.gen - 1) // a stale timeout does nothing
	if !srv.tone.playing() {
		t.Fatal("a stale timeout ended the test sound")
	}
	srv.toneTimedOut(srv.tone.gen)
	waitFrame(t, fp, n, `"loadfile","`+identPath)
	if srv.tone.playing() {
		t.Error("still playing after the timeout")
	}
}

// mpv restarting forgets the test sound.
func TestMPVRestartForgetsTheTestSound(t *testing.T) {
	srv, _, call := pairedStandbyServer(t)
	onStandbyNow(t, srv)
	call("POST", "/testsound", nil)
	srv.mpvConnected(true)
	if srv.tone.playing() {
		t.Error("test sound survived an mpv restart")
	}
}
