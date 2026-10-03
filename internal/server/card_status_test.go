//go:build !windows

package server

import (
	"strings"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/state"
)

// statusServer is a paired server on standby over a fake player, with a
// standby spec (showStatus sets show_status) and Cinefin's saved address.
// Cinefin's control link has been down since down ago.
func statusServer(t *testing.T, showStatus bool, down time.Duration) (*Server, *fakePlayer) {
	t.Helper()
	srv, _, fp, _ := newPairTestServer(t)
	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	if err := srv.state.SetCinefinAddress("10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if err := srv.state.SetStandby(state.Standby{CinemaName: "The Roxy", PlayerName: "Screen 1", ShowStatus: showStatus}); err != nil {
		t.Fatal(err)
	}
	srv.control.mu.Lock()
	srv.control.changed = time.Now().Add(-down)
	srv.control.mu.Unlock()
	putOnStandby(srv)
	return srv, fp
}

func attachClient(srv *Server) { srv.control.attach(func([]byte) {}, nil) }

// sceneKey is the key of the scene choose picks, "" for none.
func sceneKey(srv *Server) string {
	srv.card.mu.Lock()
	defer srv.card.mu.Unlock()
	if sc := srv.card.choose(); sc != nil {
		return sc.key()
	}
	return ""
}

// choose in every state: the pairing box and the confirmation come first; on
// standby, the status line or the link scenes; off standby, nothing.
func TestChooseScene(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) *Server
		want  string
	}{
		{"unpaired", func(t *testing.T) *Server {
			srv, _, _, _ := newPairTestServer(t)
			return srv
		}, "pairing"},
		{"unpaired on standby", func(t *testing.T) *Server {
			srv, _, _, _ := newPairTestServer(t)
			putOnStandby(srv)
			return srv
		}, "pairing"},
		{"confirming", func(t *testing.T) *Server {
			srv, _ := statusServer(t, true, time.Second)
			srv.card.startConfirm("10.0.0.5", "340957")
			return srv
		}, "confirm"},
		{"status line, connecting", func(t *testing.T) *Server {
			srv, _ := statusServer(t, true, time.Second)
			return srv
		}, "status"},
		{"status line, offline", func(t *testing.T) *Server {
			srv, _ := statusServer(t, true, time.Minute)
			return srv
		}, "status"},
		{"status line, ready", func(t *testing.T) *Server {
			srv, _ := statusServer(t, true, time.Minute)
			attachClient(srv)
			return srv
		}, "status"},
		{"no status line, within the grace", func(t *testing.T) *Server {
			srv, _ := statusServer(t, false, time.Second)
			return srv
		}, ""},
		{"no status line, offline", func(t *testing.T) *Server {
			srv, _ := statusServer(t, false, time.Minute)
			return srv
		}, "link-offline"},
		{"no spec, offline", func(t *testing.T) *Server {
			srv, _ := pairedLinkServer(t)
			putOnStandby(srv)
			return srv
		}, "link-offline"},
		{"playing, status line on", func(t *testing.T) *Server {
			srv, _ := statusServer(t, true, time.Minute)
			srv.standby.observed([]byte(`"/media/feature.mkv"`))
			return srv
		}, ""},
		{"playing, status line off", func(t *testing.T) *Server {
			srv, _ := statusServer(t, false, time.Minute)
			srv.standby.observed([]byte(`"/media/feature.mkv"`))
			return srv
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sceneKey(tc.setup(t)); got != tc.want {
				t.Errorf("scene = %q, want %q", got, tc.want)
			}
		})
	}
}

// The link state from the control link: ready when attached, connecting for
// the first linkGrace without it, offline after.
func TestLinkStateAt(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		connected bool
		ago       time.Duration
		want      linkState
	}{
		{true, time.Hour, linkReady},
		{false, 0, linkConnecting},
		{false, linkGrace - time.Millisecond, linkConnecting},
		{false, linkGrace, linkOffline},
	} {
		if got := linkStateAt(tc.connected, now.Add(-tc.ago), now); got != tc.want {
			t.Errorf("connected=%v for %v: %v, want %v", tc.connected, tc.ago, got, tc.want)
		}
	}
}

// Each link state's dot colour and word, and the rest of the line.
func TestStatusLineText(t *testing.T) {
	for _, tc := range []struct {
		link       linkState
		dot, word  string
		notContain string
	}{
		{linkReady, `\1c&H8AE825&\p1}m 0 0 l 8 0 8 8 0 8`, "Ready", "Cinefin\\h"},
		{linkConnecting, `\1c&HACA6A6&\p1}m 0 0 l 8 0 8 8 0 8`, "Connecting to Cinefin", "Ready"},
		{linkOffline, `\1c&H3BB9E8&\p1}m 0 0 l 8 0 8 8 0 8`, "Can't reach Cinefin", "Ready"},
	} {
		got := statusLine("The Roxy", "Screen 1", "10.0.0.5", tc.link)
		lines := strings.Split(got, "\n")
		if len(lines) != 2 {
			t.Fatalf("%v: %d events, want 2:\n%s", tc.link, len(lines), got)
		}
		name, right := lines[0], lines[1]
		if want := `{\an1\pos(56,683)\q2\bord0\shad0\fs22\1c&HECE8E8&\b500}The Roxy`; name != want {
			t.Errorf("name event:\n got %s\nwant %s", name, want)
		}
		for _, want := range []string{
			`{\an3\pos(1224,680)\q2\bord0\shad0\fs15\1c&HDCD6D6&}`, // right-aligned, 15 px
			tc.dot,
			`{\1c&HDCD6D6&}\h\h` + tc.word + `\h\h\h`,
			`{\1c&HACA6A6&}\h\h\hScreen 1 · Cinefin 10.0.0.5`,
		} {
			if !strings.Contains(right, want) {
				t.Errorf("%v: right end lacks %q:\n%s", tc.link, want, right)
			}
		}
		if strings.Contains(right, tc.notContain) {
			t.Errorf("%v: right end has %q:\n%s", tc.link, tc.notContain, right)
		}
	}
}

// Missing names fall back: no cinema name, no event on the left; no player
// name, the agent's own; no address, no "Cinefin <address>".
func TestStatusFallbacks(t *testing.T) {
	got := statusLine("", "Booth", "", linkConnecting)
	if strings.Contains(got, "\n") || strings.Contains(got, `\fs22`) {
		t.Errorf("cinema name drawn when there is none:\n%s", got)
	}
	if !strings.HasSuffix(got, `\h\h\hBooth`) || strings.Contains(got, "· Cinefin") {
		t.Errorf("right end without an address:\n%s", got)
	}

	srv, _ := statusServer(t, true, time.Second)
	if err := srv.state.SetStandby(state.Standby{ShowStatus: true}); err != nil {
		t.Fatal(err)
	}
	srv.card.mu.Lock()
	sc := srv.card.statusScene(srv.state.Standby()).(statusScene)
	srv.card.mu.Unlock()
	if sc.cinema != "" || sc.player != "Booth" || sc.address != "10.0.0.5" || sc.link != linkConnecting {
		t.Errorf("scene = %+v, want no cinema, player Booth, address 10.0.0.5, connecting", sc)
	}
}

// Long names are cut short with an ellipsis, and ASS markup is stripped.
func TestStatusClipsNames(t *testing.T) {
	got := statusLine(strings.Repeat("c", 50), strings.Repeat("p", 30), strings.Repeat("a", 40)+`{\b1}`, linkReady)
	for _, want := range []string{
		strings.Repeat("c", statusCinemaMax-1) + "…",
		strings.Repeat("p", statusPlayerMax-1) + "…",
		"Cinefin " + strings.Repeat("a", statusAddressMax-1) + "…",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("line lacks %q:\n%s", want, got)
		}
	}
	if got := clip("  Screen 1  ", 24); got != "Screen 1" {
		t.Errorf("clip trims: %q", got)
	}
	if got := statusLine("", `Screen {\an5}1`, "", linkReady); !strings.HasSuffix(got, `Screen an51`) {
		t.Errorf("markup not stripped:\n%s", got)
	}
}

// The status line is sent to mpv only when its text changes, and it takes the
// place of the offline notice and the toast.
func TestStatusRedrawsOnlyOnChange(t *testing.T) {
	srv, fp := statusServer(t, true, time.Minute)
	srv.card.refresh()
	srv.card.refresh()
	frames := fp.frames()
	if len(frames) != 1 || !strings.Contains(frames[0], "Can't reach Cinefin") || strings.Contains(frames[0], "Can't reach Cinefin at") {
		t.Fatalf("frames when offline: %v, want one status line", frames)
	}

	attachClient(srv)
	srv.card.refresh()
	srv.card.refresh()
	frames = fp.frames()[1:]
	if len(frames) != 1 || !strings.Contains(frames[0], "Ready") {
		t.Fatalf("frames once Cinefin is back: %v, want one status line, no toast", frames)
	}
}

// When the confirmation finishes, the status line replaces it in the same
// pass, with no blank frame between them.
func TestConfirmHandsOverToStatus(t *testing.T) {
	srv, fp := statusServer(t, true, time.Minute)
	attachClient(srv)
	srv.card.startConfirm("10.0.0.5", "340957")
	srv.card.refresh() // the confirmation's first frame
	srv.card.mu.Lock()
	srv.card.since = time.Now().Add(-time.Minute) // long finished
	srv.card.mu.Unlock()
	n := len(fp.frames())
	srv.card.refresh()
	got := fp.frames()[n:]
	if len(got) != 1 || !strings.Contains(got[0], "Ready") {
		t.Fatalf("frames after the confirmation: %v, want the status line alone", got)
	}
	if srv.card.confirm != nil {
		t.Error("confirmation still set")
	}
}
