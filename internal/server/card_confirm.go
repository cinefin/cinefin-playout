package server

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cinefin/cinefin-playout/internal/pairing"
)

// confirmScene is the "Paired" confirmation, played once over the ident right
// after a pairing succeeds. Its first frame is the pairing box exactly as the
// unpaired player showed it (pairingBox, drawn from the same parts). Then the
// code column turns green while the right column cross-fades from the pairing
// instructions to "Code accepted", it holds until Cinefin's control link is
// up, swaps the box to a green "Paired" panel whose check mark draws itself,
// holds it, and slides the box away. There is no full-screen background: the
// ident shows underneath.
//
// The scene lives on the card for the whole confirmation (card.confirm), so it
// can remember when the hold ended. choose refreshes connected on every pass;
// frame runs with the card's lock held, so the fields need no lock of their own.
type confirmScene struct {
	from, code string // Cinefin's address and the code that was entered ("340 957")
	connected  bool   // Cinefin's control WebSocket is attached

	// The pairing box's right column as it was when the code was accepted: the
	// player's address, its notice line and the code's remaining life (for the
	// countdown bar).
	address, notice string
	left            time.Duration

	holdEnd time.Duration // when "Code accepted" gave way; 0 until known
}

// newConfirmScene builds the confirmation for a pairing from address from with
// the code as it was entered (any spacing; only its digits are kept). address,
// notice and left are what the pairing box showed in its right column.
func newConfirmScene(from, code, address, notice string, left time.Duration) *confirmScene {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
	return &confirmScene{from: from, code: pairing.Format(digits), address: address, notice: notice, left: left}
}

func (*confirmScene) key() string { return "confirm" }

// The timeline, from the moment the scene starts.
const (
	confirmAccept     = 300 * time.Millisecond  // the code column turns green, the right column cross-fades
	confirmMinHold    = 600 * time.Millisecond  // "Code accepted" shows at least this long
	confirmMaxHold    = 2500 * time.Millisecond // and moves on by now even without Cinefin's link
	confirmSwap       = 350 * time.Millisecond  // the code fades out, the box turns green
	confirmRing       = 500 * time.Millisecond  // the circle draws itself
	confirmTick       = 300 * time.Millisecond  // then the tick
	confirmTextFade   = 350 * time.Millisecond  // "Paired" fades in as the circle starts
	confirmPairedHold = 2500 * time.Millisecond // the finished state stays up
	confirmExit       = 500 * time.Millisecond  // the box slides down and fades out
	confirmExitDrop   = 14.0                    // how far it slides, in canvas pixels
)

// Colours beyond the pairing box's own (see card_pairing.go).
const (
	confirmGreen      = "#25e88a"
	confirmDimGreen   = "#1f5a3b"
	confirmSubGrey    = "#c4c4ca"
	confirmPairedFill = "#091a11" // at boxFillOpacity, like the pairing box
)

// The check icon: a 24-unit viewbox drawn 96 px square, circle radius 10 and a
// tick, stroked 1.8 units wide.
const (
	confirmIconScale = 4.0
	confirmIconX     = boxX + 40.0
	confirmIconY     = boxY + (boxH-96)/2.0
	confirmStrokeW   = 1.8 * confirmIconScale
	confirmTextX     = confirmIconX + 96 + 30
)

var confirmTickPoints = [3][2]float64{{7.5, 12.5}, {10.5, 15.5}, {16.5, 9}}

func (sc *confirmScene) frame(t time.Duration) (string, time.Duration, bool) {
	hold := sc.hold(t)
	swapEnd := hold + confirmSwap
	ringEnd := swapEnd + confirmRing
	tickEnd := ringEnd + confirmTick
	exitStart := tickEnd + confirmPairedHold
	exitEnd := exitStart + confirmExit
	if t >= exitEnd {
		return "", 0, true
	}

	accept := confirmProgress(t, 0, confirmAccept) // code column turning green
	swap := confirmProgress(t, hold, swapEnd)      // code out, box green
	ring := confirmProgress(t, swapEnd, ringEnd)   // circle drawn so far
	tick := confirmProgress(t, ringEnd, tickEnd)   // tick drawn so far
	exit := confirmProgress(t, exitStart, exitEnd) // box leaving
	text := confirmProgress(t, swapEnd, swapEnd+confirmTextFade)
	opacity := 1 - exit
	dy := int(math.Round(confirmExitDrop * exit))

	// The box, in the pairing box's colours at first.
	events := boxPanel(dy, mix(boxFill, confirmPairedFill, swap), boxFillOpacity*opacity,
		mix(mix(boxLine, confirmDimGreen, accept), confirmGreen, swap), opacity)

	// The code column, turning green, and the right column cross-fading from
	// the pairing instructions to "Code accepted", until they fade out.
	if code := opacity * (1 - swap); code > 0 {
		events = append(events, codeColumn(sc.code, dy, mix(boxLine, confirmDimGreen, accept),
			mix(textLabel, confirmGreen, accept), mix(textBright, confirmGreen, accept), code)...)
		if help := code * (1 - accept); help > 0 {
			events = append(events, pairingHelp(sc.address, sc.notice, sc.left, dy, help)...)
		}
		if right := code * accept; right > 0 {
			events = append(events,
				textAt(1, rightX, boxMidY+dy, 24, confirmGreen, fade(right), "Code accepted"),
				textAt(7, rightX, boxMidY+8+dy, 15, textMuted, fade(right), "Connecting to Cinefin…"))
		}
	}

	// The "Paired" panel, once the box has turned green.
	if t >= swapEnd {
		if ring > 0 {
			events = append(events, confirmShape(confirmGreen, opacity, dy, confirmRingPath(ring)))
		}
		if tick > 0 {
			events = append(events, confirmShape(confirmGreen, opacity, dy, confirmTickPath(tick)))
		}
		f := opacity * text
		events = append(events,
			textAt(1, confirmTextX, boxY+106+dy, 52, textBright, `\b1`+fade(f), "Paired"),
			textAt(7, confirmTextX, boxY+116+dy, 19, confirmSubGrey, fade(f),
				"This player now plays for Cinefin at "+assText(sc.from)))
	}

	// Redraw at frame rate while anything moves or the hold waits for the
	// link; during the finished hold, not until the exit starts.
	next := frameRedraw
	if t >= tickEnd && t >= swapEnd+confirmTextFade && t < exitStart {
		next = exitStart - t
	}
	return strings.Join(events, "\n"), next, false
}

// hold returns when "Code accepted" gives way: as soon as Cinefin's control
// link is up, but not before confirmMinHold and no later than confirmMaxHold.
// Once decided it stays put, so the rest of the timeline runs from it.
func (sc *confirmScene) hold(t time.Duration) time.Duration {
	if sc.holdEnd == 0 {
		switch {
		case sc.connected:
			sc.holdEnd = min(max(t, confirmMinHold), confirmMaxHold)
		case t >= confirmMaxHold:
			sc.holdEnd = confirmMaxHold
		}
	}
	if sc.holdEnd == 0 {
		return confirmMaxHold + time.Hour // not yet known: later than anything drawn now
	}
	return sc.holdEnd
}

// confirmProgress is how far t is through [from, to), eased out: 0 before, 1
// after.
func confirmProgress(t, from, to time.Duration) float64 {
	if t <= from {
		return 0
	}
	if t >= to {
		return 1
	}
	return confirmEase(float64(t-from) / float64(to-from))
}

// confirmEase is a cubic ease-out.
func confirmEase(x float64) float64 {
	x = 1 - x
	return 1 - x*x*x
}

// confirmPathScale is the \p level of the icon's drawings: coordinates are in
// 1/4 px (\p3), so arcs and the tick land on quarter pixels.
const confirmPathScale = 4

// confirmShape fills an icon drawing given in canvas coordinates, dy pixels
// down. The drawing is placed at (0, dy), so its points are where they appear;
// an invisible corner point pins its bounding box to that origin.
func confirmShape(colour string, opacity float64, dy int, path string) string {
	return fmt.Sprintf(`{\an7\pos(0,%d)\bord0\shad0\1c%s\1a%s\p3}m 0 0 %s{\p0}`,
		dy, assColour(colour), assAlpha(opacity), path)
}

// confirmPath writes points as one closed contour.
func confirmPath(pts [][2]float64) string {
	var b strings.Builder
	for i, p := range pts {
		switch i {
		case 0:
			b.WriteString("m ")
		case 1:
			b.WriteString(" l ")
		default:
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d %d", int(math.Round(p[0]*confirmPathScale)), int(math.Round(p[1]*confirmPathScale)))
	}
	return b.String()
}

// confirmArc appends points along a circle of radius r around (cx, cy) from
// angle a0 to a1 (radians; 0 is 3 o'clock, positive is clockwise on screen).
func confirmArc(pts [][2]float64, cx, cy, r, a0, a1 float64) [][2]float64 {
	n := max(2, int(math.Ceil(math.Abs(a1-a0)/(math.Pi/36)))+1) // a point every 5 degrees or less
	for i := range n {
		a := a0 + (a1-a0)*float64(i)/float64(n-1)
		pts = append(pts, [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return pts
}

// confirmRingPath is the check icon's circle drawn p (0 to 1) of the way round,
// clockwise from 12 o'clock, as a stroke: the outer edge forward, a round cap,
// the inner edge back and a round cap. ASS drawings are filled with the
// nonzero rule, so where the caps overlap near a full circle it stays solid.
func confirmRingPath(p float64) string {
	if p <= 0 {
		return ""
	}
	s := confirmIconScale
	cx, cy, r, h := confirmIconX+12*s, confirmIconY+12*s, 10*s, confirmStrokeW/2
	a0 := -math.Pi / 2
	a1 := a0 + 2*math.Pi*math.Min(p, 1)
	end := [2]float64{cx + r*math.Cos(a1), cy + r*math.Sin(a1)}
	start := [2]float64{cx + r*math.Cos(a0), cy + r*math.Sin(a0)}
	pts := confirmArc(nil, cx, cy, r+h, a0, a1)
	pts = confirmArc(pts, end[0], end[1], h, a1, a1+math.Pi)
	pts = confirmArc(pts, cx, cy, r-h, a1, a0)
	pts = confirmArc(pts, start[0], start[1], h, a0+math.Pi, a0+2*math.Pi)
	return confirmPath(pts)
}

// confirmTickPath is the tick drawn p (0 to 1) of its length, as one
// round-capped capsule per segment (they overlap at the joint, which the
// nonzero fill keeps solid).
func confirmTickPath(p float64) string {
	if p <= 0 {
		return ""
	}
	s := confirmIconScale
	var pts [3][2]float64
	for i, q := range confirmTickPoints {
		pts[i] = [2]float64{confirmIconX + q[0]*s, confirmIconY + q[1]*s}
	}
	l1 := math.Hypot(pts[1][0]-pts[0][0], pts[1][1]-pts[0][1])
	l2 := math.Hypot(pts[2][0]-pts[1][0], pts[2][1]-pts[1][1])
	left := (l1 + l2) * math.Min(p, 1)
	var contours []string
	for i := range 2 {
		a, b := pts[i], pts[i+1]
		l := math.Hypot(b[0]-a[0], b[1]-a[1])
		if left <= 0 {
			break
		}
		if left < l {
			f := left / l
			b = [2]float64{a[0] + (b[0]-a[0])*f, a[1] + (b[1]-a[1])*f}
		}
		left -= l
		contours = append(contours, confirmCapsule(a, b, confirmStrokeW/2))
	}
	return strings.Join(contours, " ")
}

// confirmCapsule is the segment a-b stroked h either side with round caps.
func confirmCapsule(a, b [2]float64, h float64) string {
	dir := math.Atan2(b[1]-a[1], b[0]-a[0])
	pts := confirmArc(nil, b[0], b[1], h, dir-math.Pi/2, dir+math.Pi/2)
	pts = confirmArc(pts, a[0], a[1], h, dir+math.Pi/2, dir+3*math.Pi/2)
	return confirmPath(pts)
}
