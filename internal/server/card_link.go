package server

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The offline notice. A paired player on standby whose control link to
// Cinefin has been down for linkGrace (counted from the agent's start when
// Cinefin has not connected yet) shows "Can't reach Cinefin" over the ident,
// unless the standby spec asks for the status line, which shows the same state
// in its own way (card_status.go). When Cinefin connects again after the
// notice was showing, a "back online" toast shows for linkToastFor. A player
// that is not on standby shows neither; if Cinefin is away for linkGrace and
// mpv has nothing loaded, the agent puts it on standby, and if mpv is looping
// a command hold, the agent ends the loop so the programme plays on.
const (
	linkGrace     = 30 * time.Second
	linkToastFor  = 3 * time.Second
	linkToastFade = 300 * time.Millisecond
	// linkStandbyRetry is how long the agent waits before trying standby
	// again, should mpv still have nothing loaded (the ident failed to load),
	// or before ending a hold again.
	linkStandbyRetry = 10 * time.Second
	// linkToastWindow is how recently the notice must have been on screen when
	// Cinefin attaches for the toast to show. The card redraws a static scene
	// every idleRedraw, so this leaves a margin over that.
	linkToastWindow = 2 * idleRedraw
)

// linkWatch is the notice's memory between redraws. Guarded by card.mu.
type linkWatch struct {
	noticeAt  time.Time // when the notice was last chosen
	toastAt   time.Time // when the "back online" toast started
	standbyAt time.Time // when standbyWhenAway last put mpv on standby
	holdAt    time.Time // when endHoldWhenAway last ended a hold
}

// choose picks the link scene from the control link's state at now.
func (w *linkWatch) choose(connected bool, since, now time.Time, address, detail string) scene {
	if !connected {
		if now.Sub(since) < linkGrace {
			return nil
		}
		w.noticeAt = now
		return linkNoticeScene{address: address, detail: detail}
	}
	// Connected since `since`: toast if the notice was up until then.
	if !w.noticeAt.IsZero() && since.Sub(w.noticeAt) <= linkToastWindow {
		w.toastAt = now
	}
	w.noticeAt = time.Time{}
	if !w.toastAt.IsZero() && now.Sub(w.toastAt) < linkToastFor {
		return linkToastScene{}
	}
	w.toastAt = time.Time{}
	return nil
}

// linkScene is the scene for a paired player on standby when the status line
// is off: the offline notice, the "back online" toast, or nothing. Caller
// holds c.mu.
func (c *card) linkScene() scene {
	connected, since := c.s.control.link()
	return c.link.choose(connected, since, time.Now(), c.s.state.CinefinAddress(), c.s.mismatchNotice(false))
}

// standbyWhenAway puts mpv on standby when it has nothing loaded and Cinefin
// has been away for linkGrace, trying again every linkStandbyRetry should the
// ident fail to load. Caller holds c.mu.
func (c *card) standbyWhenAway() {
	connected, since := c.s.control.link()
	now := time.Now()
	if connected || now.Sub(since) < linkGrace || !c.s.standby.idle() || now.Sub(c.link.standbyAt) < linkStandbyRetry {
		return
	}
	c.link.standbyAt = now
	c.s.enterStandby()
}

// endHoldWhenAway ends a command hold when Cinefin has been away for
// linkGrace. Cinefin holds on a black clip with loop-file=inf until the
// command finishes; with the loop off the clip plays out and mpv moves on
// through the programme by itself. Caller holds c.mu.
func (c *card) endHoldWhenAway() {
	connected, since := c.s.control.link()
	now := time.Now()
	if connected || now.Sub(since) < linkGrace || !c.s.standby.loopingForever() || now.Sub(c.link.holdAt) < linkStandbyRetry {
		return
	}
	c.link.holdAt = now
	if err := c.s.backend.Send(agentCommand("set_property", "loop-file", "no")); err != nil {
		c.s.log.Printf("link: end the hold: %v", err)
		return
	}
	c.s.log.Printf("link: Cinefin is away, ending the hold")
}

// linkBox is where a notice box sits on the 1280x720 canvas. The content area
// is height tall; the box adds padY above and below it. Moving a box (to a
// corner, say) is a change to left and top here.
type linkBox struct {
	left, top, width int
	padX, padY       int
	content          int // content height
	icon, gap        int // icon size, and the space between it and the text
}

func (b linkBox) height() int { return b.content + 2*b.padY }

// textX is where the text starts, right of the icon.
func (b linkBox) textX() int { return b.left + b.padX + b.icon + b.gap }

var (
	linkNoticeBox = linkBox{left: 290, top: 490, width: 700, padX: 28, padY: 22, content: 52, icon: 44, gap: 22}
	linkToastBox  = linkBox{left: 400, top: 500, width: 480, padX: 22, padY: 16, content: 32, icon: 32, gap: 16}
)

// Colours (&HBBGGRR&) and base alphas (00 opaque, FF invisible) of the boxes.
const (
	linkWhite      = "F2F0F0" // #f2f0f0
	linkNoticeFill = "08161C" // rgb(28,22,8)
	linkNoticeLine = "14495C" // #5c4914
	linkAmber      = "3BB9E8" // #e8b93b
	linkNoticeSub  = "94B8C4" // #c4b894
	linkToastFill  = "111A09" // rgb(9,26,17)
	linkGreen      = "8AE825" // #25e88a
	linkFillAlpha  = 0x14     // 92% opaque
)

// linkNoticeScene is the offline notice. address is Cinefin's last known
// address, "" when none is known; detail, when set, says why (a protocol
// mismatch) in place of "Retrying every few seconds.".
type linkNoticeScene struct{ address, detail string }

func (linkNoticeScene) key() string { return "link-offline" }

func (n linkNoticeScene) frame(time.Duration) (string, time.Duration, bool) {
	return linkNoticeText(n.address, n.detail), idleRedraw, false
}

// linkNoticeText lays out the offline notice as ASS events.
func linkNoticeText(address, detail string) string {
	b := linkNoticeBox
	title := "Can't reach Cinefin"
	if address != "" {
		title += " at " + assText(address)
	}
	sub := "Retrying every few seconds."
	if detail != "" {
		sub = assText(detail)
	}
	top := b.top + b.padY
	ev := linkPanel(b, linkNoticeFill, linkNoticeLine, 1)
	ev = append(ev, linkWarningIcon(b.left+b.padX, top+(b.content-b.icon)/2, 1)...)
	ev = append(ev,
		linkTextEvent(b.textX(), top+13, 22, `\b500`, linkWhite, 0, title),
		linkTextEvent(b.textX(), top+b.content-10, 16, "", linkNoticeSub, 0, sub),
	)
	return strings.Join(ev, "\n")
}

// linkToastScene is the "back online" toast. It fades in and out over
// linkToastFade and finishes after linkToastFor.
type linkToastScene struct{}

func (linkToastScene) key() string { return "link-online" }

func (linkToastScene) frame(t time.Duration) (string, time.Duration, bool) {
	if t >= linkToastFor {
		return "", 0, true
	}
	var k float64
	next := frameRedraw
	switch {
	case t < linkToastFade:
		k = float64(t) / float64(linkToastFade)
	case t > linkToastFor-linkToastFade:
		k = float64(linkToastFor-t) / float64(linkToastFade)
	default:
		k = 1
		next = linkToastFor - linkToastFade - t
	}
	return linkToastText(k), next, false
}

// linkToastText lays out the toast as ASS events at opacity k (0 to 1).
func linkToastText(k float64) string {
	b := linkToastBox
	top := b.top + b.padY
	ev := linkPanel(b, linkToastFill, linkGreen, k)
	ev = append(ev, linkCheckIcon(b.left+b.padX, top+(b.content-b.icon)/2, k)...)
	ev = append(ev, linkTextEvent(b.textX(), top+b.content/2, 20, `\b500`, linkWhite, linkAlpha(0, k), "Connected to Cinefin again"))
	return strings.Join(ev, "\n")
}

// linkAlpha fades a base ASS alpha (00 opaque, FF invisible) to opacity k.
func linkAlpha(base int, k float64) int {
	k = min(max(k, 0), 1)
	return 255 - int(math.Round(float64(255-base)*k))
}

// linkShape is one ASS vector drawing with its top-left at (x, y).
func linkShape(x, y int, colour string, alpha int, path string) string {
	return fmt.Sprintf(`{\an7\pos(%d,%d)\bord0\shad0\1c&H%s&\1a&H%02X&\p1}%s{\p0}`, x, y, colour, alpha, path)
}

// linkTextEvent is one line of text, left-aligned at x and vertically centred
// on y.
func linkTextEvent(x, y, size int, style, colour string, alpha int, text string) string {
	return fmt.Sprintf(`{\an4\pos(%d,%d)\bord0\shad0\q2\fs%d%s\1c&H%s&\1a&H%02X&}%s`, x, y, size, style, colour, alpha, text)
}

// linkPanel is a box's translucent fill and its 1 px border, at opacity k. The
// border is a ring (the inner rectangle wound the other way cuts it out), so
// it does not show through the fill.
func linkPanel(b linkBox, fill, line string, k float64) []string {
	w, h := b.width, b.height()
	ring := fmt.Sprintf("m 0 0 l %d 0 %d %d 0 %d m 1 1 l 1 %d %d %d %d 1", w, w, h, h, h-1, w-1, h-1, w-1)
	inner := fmt.Sprintf("m 0 0 l %d 0 %d %d 0 %d", w-2, w-2, h-2, h-2)
	return []string{
		linkShape(b.left+1, b.top+1, fill, linkAlpha(linkFillAlpha, k), inner),
		linkShape(b.left, b.top, line, linkAlpha(0, k), ring),
	}
}

// linkWarningIcon is a 44 px warning triangle outline with an exclamation
// mark, top-left at (x, y).
func linkWarningIcon(x, y int, k float64) []string {
	a := linkAlpha(0, k)
	triangle := "m 22 4 l 41 38 3 38 m 22 10.1 l 8.1 35 35.9 35"
	mark := "m 20.6 15 l 23.4 15 23.4 27 20.6 27 " + linkCircle(22, 31.6, 1.7, false)
	return []string{linkShape(x, y, linkAmber, a, triangle), linkShape(x, y, linkAmber, a, mark)}
}

// linkCheckIcon is a 32 px circle outline with a check mark, top-left at (x, y).
func linkCheckIcon(x, y int, k float64) []string {
	a := linkAlpha(0, k)
	ring := linkCircle(16, 16, 15, false) + " " + linkCircle(16, 16, 12.6, true)
	check := "m 8.6 17.4 l 14.1 22.9 23.5 12.4 21.5 10.6 13.9 19.1 10.4 15.6"
	return []string{linkShape(x, y, linkGreen, a, ring), linkShape(x, y, linkGreen, a, check)}
}

// linkCircle is a circle as four cubic Béziers, clockwise on screen, or the
// other way when reverse is set (to cut a hole in a shape drawn clockwise).
func linkCircle(cx, cy, r float64, reverse bool) string {
	const k = 0.5523 // control point distance for a quarter circle, per unit radius
	c := r * k
	// Clockwise on screen (y down): top, right, bottom, left.
	pts := [][2]float64{
		{cx, cy - r},
		{cx + c, cy - r}, {cx + r, cy - c}, {cx + r, cy},
		{cx + r, cy + c}, {cx + c, cy + r}, {cx, cy + r},
		{cx - c, cy + r}, {cx - r, cy + c}, {cx - r, cy},
		{cx - r, cy - c}, {cx - c, cy - r}, {cx, cy - r},
	}
	if reverse {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	f := func(p [2]float64) string { return fmt.Sprintf("%.1f %.1f", p[0], p[1]) }
	var s strings.Builder
	s.WriteString("m " + f(pts[0]))
	for i := 1; i < len(pts); i += 3 {
		s.WriteString(" b " + f(pts[i]) + " " + f(pts[i+1]) + " " + f(pts[i+2]))
	}
	return s.String()
}
