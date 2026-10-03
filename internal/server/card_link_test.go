//go:build !windows

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/cinefin/cinefin-playout/internal/ident"
)

// The link scene over time: nothing for the first 30 s without Cinefin, then
// the notice; a reconnect after the notice shows the toast for 3 s; a brief
// drop shows nothing.
func TestLinkWatchTransitions(t *testing.T) {
	var w linkWatch
	start := time.Now()
	at := func(d time.Duration) time.Time { return start.Add(d) }

	// Never connected since the agent started.
	if sc := w.choose(false, start, at(29*time.Second), "10.0.0.5", ""); sc != nil {
		t.Fatalf("at 29 s: %v, want nothing", sc)
	}
	sc := w.choose(false, start, at(30*time.Second), "10.0.0.5", "")
	if n, ok := sc.(linkNoticeScene); !ok || n.address != "10.0.0.5" {
		t.Fatalf("at 30 s: %v, want the notice", sc)
	}
	w.choose(false, start, at(40*time.Second), "10.0.0.5", "")

	// Cinefin attaches at 40.5 s: the toast, until 3 s later.
	attach := at(40500 * time.Millisecond)
	if sc := w.choose(true, attach, attach, "", ""); sc == nil || sc.key() != "link-online" {
		t.Fatalf("on attach: %v, want the toast", sc)
	}
	if sc := w.choose(true, attach, attach.Add(2*time.Second), "", ""); sc == nil || sc.key() != "link-online" {
		t.Fatalf("2 s after attach: %v, want the toast", sc)
	}
	if sc := w.choose(true, attach, attach.Add(3*time.Second), "", ""); sc != nil {
		t.Fatalf("3 s after attach: %v, want nothing", sc)
	}

	// A drop shorter than 30 s shows nothing, and so does the reconnect.
	drop := at(60 * time.Second)
	if sc := w.choose(false, drop, drop.Add(10*time.Second), "", ""); sc != nil {
		t.Fatalf("10 s into a drop: %v, want nothing", sc)
	}
	back := drop.Add(12 * time.Second)
	if sc := w.choose(true, back, back, "", ""); sc != nil {
		t.Fatalf("reconnect after a short drop: %v, want nothing", sc)
	}
}

// A notice that stopped long before Cinefin attached (the player was unpaired
// meanwhile, say) does not earn a toast.
func TestLinkNoToastAfterStaleNotice(t *testing.T) {
	var w linkWatch
	start := time.Now()
	w.choose(false, start, start.Add(31*time.Second), "", "")
	attach := start.Add(5 * time.Minute)
	if sc := w.choose(true, attach, attach, "", ""); sc != nil {
		t.Fatalf("toast after a stale notice: %v", sc)
	}
}

func TestLinkNoticeText(t *testing.T) {
	got := linkNoticeText("192.168.1.20", "")
	for _, want := range []string{
		"Can't reach Cinefin at 192.168.1.20",
		"Retrying every few seconds.",
		`\1c&H08161C&\1a&H14&`, // the fill
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notice lacks %q:\n%s", want, got)
		}
	}
	if got := linkNoticeText("", ""); !strings.Contains(got, "}Can't reach Cinefin\n") {
		t.Errorf("notice without an address:\n%s", got)
	}
}

func TestLinkToastFrames(t *testing.T) {
	sc := linkToastScene{}
	text, next, done := sc.frame(0)
	if done || !strings.Contains(text, "Connected to Cinefin again") || !strings.Contains(text, `\1a&HFF&`) {
		t.Errorf("t=0: done=%v text=%s", done, text)
	}
	if next != frameRedraw {
		t.Errorf("t=0: next = %v, want frame rate while fading", next)
	}
	text, next, _ = sc.frame(time.Second)
	if strings.Contains(text, `\1a&HFF&`) || next != 1700*time.Millisecond {
		t.Errorf("t=1s: next=%v text=%s", next, text)
	}
	if _, _, done := sc.frame(linkToastFor); !done {
		t.Error("toast not done after 3 s")
	}
}

// pairedLinkServer is a paired server over a fake player whose control link
// has been down since 31 s ago.
func pairedLinkServer(t *testing.T) (*Server, *fakePlayer) {
	t.Helper()
	srv, _, fp, _ := newPairTestServer(t)
	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	if err := srv.state.SetCinefinAddress("10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	srv.control.mu.Lock()
	srv.control.changed = time.Now().Add(-31 * time.Second)
	srv.control.mu.Unlock()
	return srv, fp
}

// putOnStandby makes the server believe mpv is showing the standby ident.
func putOnStandby(srv *Server) {
	srv.standby.mu.Lock()
	srv.standby.loaded = "/state/ident.mp4"
	srv.standby.mu.Unlock()
	srv.standby.observed(json.RawMessage(`"/state/ident.mp4"`))
}

// With Cinefin away for 30 s a player that is not on standby shows nothing:
// the notice is not drawn over a file mpv is playing, and when mpv has nothing
// loaded the card puts it on standby, retrying only after linkStandbyRetry.
// Once on standby, the notice shows.
func TestLinkNoticeEntersStandbyWhenIdle(t *testing.T) {
	srv, fp := pairedLinkServer(t)
	srv.standby.observed(json.RawMessage(`"/media/feature.mkv"`))
	srv.card.refresh()
	if frames := fp.frames(); len(frames) != 0 {
		t.Fatalf("drew over a busy player: %v", frames)
	}

	// Nothing loaded: standby, with the bundled ident as there is no spec.
	srv.standby.observed(nil)
	srv.card.refresh()
	path := filepath.Join(srv.cfg.StateDir, ident.FileName)
	want := string(agentCommand("loadfile", path, "replace", -1, titled(ident.Options, identTitle)))
	if got := fp.frames(); len(got) != 2 || got[0] != want {
		t.Fatalf("frames after idle: %v\nwant %s then unpause", got, want)
	}

	// Still idle a moment later (the load failed): no retry yet.
	n := len(fp.frames())
	srv.card.refresh()
	if got := fp.frames()[n:]; len(got) != 0 {
		t.Fatalf("retried at once: %v", got)
	}

	// The ident is on screen: the notice.
	srv.standby.observed(json.RawMessage(`"` + path + `"`))
	srv.card.refresh()
	if last := fp.frames()[len(fp.frames())-1]; !strings.Contains(last, "Can't reach Cinefin at 10.0.0.5") {
		t.Fatalf("no notice on standby: %s", last)
	}
}

// endHoldFrame is the agent's command that ends a command hold.
var endHoldFrame = string(agentCommand("set_property", "loop-file", "no"))

// endHolds counts the hold endings among the frames sent from n on.
func endHolds(fp *fakePlayer, n int) int {
	c := 0
	for _, f := range fp.frames()[n:] {
		if f == endHoldFrame {
			c++
		}
	}
	return c
}

// holdServer is pairedLinkServer with mpv playing a hold's black clip, its
// loop-file reported as loop, and the link down for down.
func holdServer(t *testing.T, loop string, down time.Duration) (*Server, *fakePlayer) {
	t.Helper()
	srv, fp := pairedLinkServer(t)
	srv.control.mu.Lock()
	srv.control.changed = time.Now().Add(-down)
	srv.control.mu.Unlock()
	srv.standby.observed(json.RawMessage(`"http://cinefin/stream/system/black/"`))
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000005,"name":"loop-file","data":` + loop + `}`))
	return srv, fp
}

// With Cinefin away for 30 s the agent ends a command hold once, and tries
// again only after linkStandbyRetry while mpv still reports the loop.
func TestEndHoldWhenAway(t *testing.T) {
	srv, fp := holdServer(t, `"inf"`, 31*time.Second)
	srv.card.refresh()
	if n := endHolds(fp, 0); n != 1 {
		t.Fatalf("hold endings = %d, want 1", n)
	}
	n := len(fp.frames())
	srv.card.refresh()
	if got := endHolds(fp, n); got != 0 {
		t.Fatalf("ended the hold again at once")
	}
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000005,"name":"loop-file","data":false}`))
	if srv.standby.loopingForever() {
		t.Error("still looping after loop-file=no")
	}
}

// The hold is left alone while Cinefin is attached, before the grace, and
// when loop-file is not inf.
func TestEndHoldLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		name, loop string
		down       time.Duration
		attach     bool
	}{
		{"attached", `"inf"`, 31 * time.Second, true},
		{"before the grace", `"inf"`, 29 * time.Second, false},
		{"loop-file no", `false`, 31 * time.Second, false},
		{"loop-file count", `3`, 31 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, fp := holdServer(t, tc.loop, tc.down)
			if tc.attach {
				srv.control.attach(func([]byte) {}, nil)
				srv.control.mu.Lock()
				srv.control.changed = time.Now().Add(-tc.down)
				srv.control.mu.Unlock()
			}
			srv.card.refresh()
			if n := endHolds(fp, 0); n != 0 {
				t.Fatalf("hold endings = %d, want 0", n)
			}
		})
	}
}

// Once Cinefin is back the notice gives way to the toast, and the agent's
// path observations never reach Cinefin.
func TestLinkReconnectShowsToast(t *testing.T) {
	srv, fp := pairedLinkServer(t)
	putOnStandby(srv)
	srv.card.refresh()

	var got []string
	srv.control.attach(func(b []byte) { got = append(got, string(b)) }, nil)
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000003,"name":"path","data":"/state/ident.mp4"}`))
	if len(got) != 0 || !srv.standby.onStandby() {
		t.Fatalf("path event: forwarded %v, on standby=%v", got, srv.standby.onStandby())
	}

	srv.card.refresh()
	frames := fp.frames()
	if last := frames[len(frames)-1]; !strings.Contains(last, "Connected to Cinefin again") {
		t.Fatalf("no toast after reconnect: %s", last)
	}
}

// The agent pings the control client and drops one that stops answering. A
// coder/websocket client only answers pings while it reads, so one that never
// reads plays a dead link; one that reads stays connected.
func TestControlPingDropsSilentClient(t *testing.T) {
	oldInterval, oldTimeout := pingInterval, pingTimeout
	pingInterval, pingTimeout = 50*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { pingInterval, pingTimeout = oldInterval, oldTimeout })

	srv, ts, _, _ := newPairTestServer(t)
	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	dial := func() *websocket.Conn {
		conn, _, err := websocket.Dial(context.Background(), wsURL(ts.URL), &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer tok"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// A reading client answers the pings and stays.
	live := dial()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, _, err := live.Read(ctx); err != nil {
				return
			}
		}
	}()
	waitFor("attach", srv.control.connected)
	if got := srv.state.CinefinAddress(); got != "127.0.0.1" {
		t.Errorf("Cinefin's address = %q, want 127.0.0.1", got)
	}
	time.Sleep(400 * time.Millisecond)
	if !srv.control.connected() {
		t.Fatal("a client answering pings was dropped")
	}
	cancel()
	live.CloseNow()
	waitFor("detach", func() bool { return !srv.control.connected() })

	// A silent client is dropped after a missed pong.
	dial()
	waitFor("attach", srv.control.connected)
	waitFor("the silent client to be dropped", func() bool { return !srv.control.connected() })
}
