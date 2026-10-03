package server

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/pairing"
)

const ms = time.Millisecond

func confirmAt(t *testing.T, sc *confirmScene, at time.Duration) (string, time.Duration) {
	t.Helper()
	text, next, done := sc.frame(at)
	if done {
		t.Fatalf("frame(%v): done too early", at)
	}
	return text, next
}

func mustContain(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("frame lacks %q:\n%s", w, text)
		}
	}
}

func mustLack(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(text, w) {
			t.Errorf("frame has %q:\n%s", w, text)
		}
	}
}

// eventWith returns the first event (line) containing s.
func eventWith(text, s string) string {
	for _, ev := range strings.Split(text, "\n") {
		if strings.Contains(ev, s) {
			return ev
		}
	}
	return ""
}

// newTestConfirm is a confirmation for code 340957 from 10.0.0.2, accepted
// with half the code's life left on the pairing box.
func newTestConfirm(from string) *confirmScene {
	return newConfirmScene(from, "340957", "http://10.0.0.5:8089", "", pairing.CodeLifetime/2)
}

func TestConfirmCodeIsFormatted(t *testing.T) {
	for in, want := range map[string]string{"340957": "340 957", "340 957": "340 957", "340-957": "340 957"} {
		if got := newConfirmScene("10.0.0.2", in, "a", "", 0).code; got != want {
			t.Errorf("code %q = %q, want %q", in, got, want)
		}
	}
}

// The first frame is the pairing box the unpaired player showed for that code,
// event for event: same fill, border, divider, label, code and right column.
func TestConfirmFirstFrameIsThePairingBox(t *testing.T) {
	sc := newTestConfirm("10.0.0.2")
	text, next := confirmAt(t, sc, 0)
	if want := pairingBox("340 957", "http://10.0.0.5:8089", "", pairing.CodeLifetime/2, 1); text != want {
		t.Errorf("first frame differs from the pairing box:\n got %s\nwant %s", text, want)
	}
	mustLack(t, text, "Code accepted", "Paired")
	if next != frameRedraw {
		t.Errorf("next = %v, want frame rate while animating", next)
	}
}

// While the code turns green, the pairing instructions fade out as "Code
// accepted" fades in; once accepted, only the latter is left.
func TestConfirmCrossFade(t *testing.T) {
	sc := newTestConfirm("10.0.0.2")
	text, _ := confirmAt(t, sc, confirmAccept/2)
	for _, s := range []string{"Settings › Playout", "new code in", "Code accepted"} {
		ev := eventWith(text, s)
		if !strings.Contains(ev, `\alpha&H`) || strings.Contains(ev, `\alpha&HFF&`) {
			t.Errorf("%q should be part transparent mid-fade: %s", s, ev)
		}
	}

	text, _ = confirmAt(t, sc, confirmAccept)
	mustContain(t, text, "Code accepted", "Connecting to Cinefin…",
		`\1c&H8AE825&`, // #25e88a
		`\1c&H3B5A1F&`, // border and divider #1f5a3b
	)
	mustLack(t, text, "Settings › Playout", "new code in")
	for _, s := range []string{"}340 957", "PAIRING CODE", "Code accepted"} {
		if ev := eventWith(text, s); !strings.Contains(ev, `\1c&H8AE825&`) || strings.Contains(ev, `\alpha`) {
			t.Errorf("%q not solid green: %s", s, ev)
		}
	}
}

func TestConfirmHoldsUntilConnected(t *testing.T) {
	sc := newTestConfirm("10.0.0.2")
	// Not connected: "Code accepted" stays up, polled at frame rate.
	for _, at := range []time.Duration{700 * ms, 1500 * ms, 2400 * ms} {
		text, next := confirmAt(t, sc, at)
		mustContain(t, text, "Code accepted", "340 957")
		mustLack(t, text, "Paired")
		if next != frameRedraw {
			t.Errorf("at %v next = %v, want frame rate to notice the link", at, next)
		}
	}
	sc.connected = true
	confirmAt(t, sc, 2450*ms)
	if sc.holdEnd != 2450*ms {
		t.Errorf("holdEnd = %v, want the moment the link was seen", sc.holdEnd)
	}
}

func TestConfirmHoldBounds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		connectAt time.Duration // -1: never
		want      time.Duration
	}{
		{"connected before the minimum", 100 * ms, confirmMinHold},
		{"connected mid-hold", 1200 * ms, 1200 * ms},
		{"never connected", -1, confirmMaxHold},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := newTestConfirm("10.0.0.2")
			for at := time.Duration(0); at <= 3*time.Second; at += frameRedraw {
				if tc.connectAt >= 0 && at >= tc.connectAt {
					sc.connected = true
				}
				sc.frame(at)
			}
			// The frame loop steps by 1/30 s, so allow one step of lateness.
			if d := sc.holdEnd - tc.want; d < 0 || d > frameRedraw {
				t.Errorf("holdEnd = %v, want %v", sc.holdEnd, tc.want)
			}
		})
	}
}

func TestConfirmTimeline(t *testing.T) {
	sc := newTestConfirm("10.0.0.2 {\\an5}")
	sc.connected = true
	confirmAt(t, sc, 0) // the link is already up: the hold ends at the minimum
	hold := confirmMinHold
	swapEnd := hold + confirmSwap
	ringEnd := swapEnd + confirmRing
	tickEnd := ringEnd + confirmTick
	exitStart := tickEnd + confirmPairedHold
	exitEnd := exitStart + confirmExit
	const icon = `\1c&H8AE825&\1a&H00&\p3` // the ring and tick, solid green

	// Mid-swap: the code fading, the box going green, no "Paired" yet.
	text, _ := confirmAt(t, sc, hold+confirmSwap/2)
	mustContain(t, text, "340 957")
	mustLack(t, text, "Paired")

	// The circle part drawn, no tick yet.
	text, _ = confirmAt(t, sc, swapEnd+confirmRing/4)
	mustContain(t, text, "Paired", "This player now plays for Cinefin at 10.0.0.2 an5",
		hairScale+`\1c&H8AE825&\1a&H00&`, // the border, green
		`\1c&H111A09&\1a&H40&`,           // fill rgb(9,26,17) at 75%
	)
	mustLack(t, text, "340 957", "Code accepted")
	if n := strings.Count(text, icon); n != 1 {
		t.Errorf("want the ring only, got %d icon drawings:\n%s", n, text)
	}

	// Finished: ring and tick, then a long wait until the exit.
	text, next := confirmAt(t, sc, tickEnd+time.Second)
	if n := strings.Count(text, icon); n != 2 {
		t.Errorf("want ring and tick, got %d:\n%s", n, text)
	}
	if ev := eventWith(text, "}Paired"); strings.Contains(ev, `\alpha`) {
		t.Errorf("Paired not fully shown: %s", ev)
	}
	if want := exitStart - (tickEnd + time.Second); next != want {
		t.Errorf("next during the finished hold = %v, want %v", next, want)
	}

	// Mid-exit: slid down, icon included, and partly transparent.
	text, next = confirmAt(t, sc, exitStart+confirmExit/2)
	if next != frameRedraw {
		t.Errorf("next during exit = %v", next)
	}
	ev := eventWith(text, "}Paired")
	if !strings.Contains(ev, `\alpha&H`) || strings.Contains(ev, `\alpha&HFF&`) {
		t.Errorf("Paired mid-exit should be part transparent: %s", ev)
	}
	if strings.Contains(text, `\pos(260,468)`) || strings.Contains(text, `\pos(0,0)`) {
		t.Errorf("box or icon has not moved mid-exit:\n%s", text)
	}

	if _, _, done := sc.frame(exitEnd); !done {
		t.Error("not done after the exit")
	}
}

// pathPoints parses a \p3 drawing (quarter pixels) back into canvas points.
func pathPoints(t *testing.T, path string) [][2]float64 {
	t.Helper()
	var nums []float64
	for _, f := range strings.Fields(path) {
		if f == "m" || f == "l" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("bad path token %q in %q", f, path)
		}
		nums = append(nums, float64(n)/confirmPathScale)
	}
	if len(nums)%2 != 0 {
		t.Fatalf("odd coordinate count in %q", path)
	}
	var pts [][2]float64
	for i := 0; i < len(nums); i += 2 {
		pts = append(pts, [2]float64{nums[i], nums[i+1]})
	}
	return pts
}

func TestConfirmRingPath(t *testing.T) {
	if p := confirmRingPath(0); p != "" {
		t.Errorf("0%% ring = %q, want nothing", p)
	}
	cx, cy := confirmIconX+48, confirmIconY+48
	r, h := 40.0, confirmStrokeW/2
	check := func(p float64) (minX, maxX float64) {
		pts := pathPoints(t, confirmRingPath(p))
		minX, maxX = math.Inf(1), math.Inf(-1)
		for _, q := range pts {
			// Every point is on the stroke: within h of the circle (caps included).
			if d := math.Hypot(q[0]-cx, q[1]-cy); d < r-h-0.5 || d > r+h+0.5 {
				t.Errorf("p=%v: point %v is %v from the centre, want %v±%v", p, q, d, r, h)
			}
			minX, maxX = min(minX, q[0]), max(maxX, q[0])
		}
		return minX, maxX
	}
	// Half way: clockwise from 12 o'clock to 6, so the right half only
	// (the round caps reach h past the centre line).
	if minX, maxX := check(0.5); minX < cx-h-0.5 || maxX < cx+r {
		t.Errorf("half ring spans x %v..%v, want the right half of %v±%v", minX, maxX, cx, r)
	}
	// Full: both sides.
	if minX, maxX := check(1); minX > cx-r || maxX < cx+r {
		t.Errorf("full ring spans x %v..%v", minX, maxX)
	}
}

func TestConfirmTickPath(t *testing.T) {
	if p := confirmTickPath(0); p != "" {
		t.Errorf("0%% tick = %q", p)
	}
	if n := strings.Count(confirmTickPath(0.2), "m "); n != 1 {
		t.Errorf("short tick has %d strokes, want the first only", n)
	}
	full := confirmTickPath(1)
	if n := strings.Count(full, "m "); n != 2 {
		t.Errorf("full tick has %d strokes, want 2", n)
	}
	// The tick ends at (16.5, 9) in the icon's viewbox, plus its round cap.
	maxX := 0.0
	for _, q := range pathPoints(t, full) {
		maxX = max(maxX, q[0])
	}
	if want := confirmIconX + 16.5*confirmIconScale + confirmStrokeW/2; math.Abs(maxX-want) > 0.5 {
		t.Errorf("tick reaches x %v, want %v", maxX, want)
	}
}

func BenchmarkConfirmFrame(b *testing.B) {
	sc := newConfirmScene("192.168.1.20", "340957", "http://10.0.0.5:8089", "", time.Minute)
	sc.connected = true
	sc.frame(0)
	at := confirmMinHold + confirmSwap + confirmRing/2
	for b.Loop() {
		sc.frame(at)
	}
}
