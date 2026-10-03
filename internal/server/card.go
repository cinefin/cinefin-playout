package server

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cinefin/cinefin-playout/internal/pairing"
)

// The agent's reserved ids on mpv's IPC. agentRequestID tags its own commands
// (the overlay, standby and the test sound); pathObserverID and loopObserverID
// are its observers of mpv's path and loop-file (and timeObserverID, in
// testsound.go, of the test sound's position).
// Frames carrying them are not forwarded to the control client (see
// control.fromMPV).
const (
	agentRequestID = 2_000_000_001
	pathObserverID = 2_000_000_003
	loopObserverID = 2_000_000_005
)

// card owns the agent's overlay on the player's screen: the pairing box while
// unpaired, the test card, the paired confirmation, and over standby either
// the status line or the offline notice and toast. It is an mpv OSD overlay
// drawn over IPC, so it needs no image files and works on a desktop window and
// on DRM alike.
//
// What is on screen is a scene, chosen from the player's state on every pass
// (see choose). A scene renders itself for the time since it started and says
// how soon it wants redrawing, so the pairing box redraws every few seconds
// (for its countdown bar) and an animation at frame rate. Only one goroutine
// (run) draws; state changes kick it to redraw at once. forget makes the next
// pass redraw, for a freshly started mpv that has no overlay.
type card struct {
	s    *Server
	kick chan struct{}

	mu    sync.Mutex
	shown string    // text currently on screen, "" when none
	key   string    // key of the scene on screen, "" when none
	since time.Time // when that scene started
	link  linkWatch // the offline notice's state (card_link.go)

	confirm     *confirmScene // set while the paired confirmation plays
	codeExpires time.Time     // when the code on the pairing box expires, for the confirmation's first frame

	testUntil  time.Time // when the test card turns itself off; zero while it is off
	testOutput string    // the output line on the test card
}

func newCard(s *Server) *card { return &card{s: s, kick: make(chan struct{}, 1)} }

// scene is one thing the overlay can show.
type scene interface {
	// key names the scene; a different key restarts the scene clock.
	key() string
	// frame renders the scene t after it started: the ASS events, how soon it
	// wants drawing again (0 = idleRedraw), and whether it has finished (a
	// finished scene is cleared from the screen).
	frame(t time.Duration) (text string, next time.Duration, done bool)
}

const (
	idleRedraw  = time.Second      // how often the state is checked when a scene sets no time
	frameRedraw = time.Second / 30 // the fastest an animated scene is redrawn
)

func (c *card) run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-c.kick:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		next := c.refresh()
		if next <= 0 {
			next = idleRedraw
		}
		timer.Reset(max(next, frameRedraw))
	}
}

// wake asks the loop to redraw now.
func (c *card) wake() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// forget marks the screen as blank, so the next refresh draws the card again.
func (c *card) forget() {
	c.mu.Lock()
	c.shown = ""
	c.mu.Unlock()
	c.wake()
}

// startConfirm plays the paired confirmation; from is Cinefin's address and
// code the pairing code it entered. It starts from the pairing box as shown,
// with the countdown of the code just used.
func (c *card) startConfirm(from, code string) {
	c.mu.Lock()
	c.confirm = newConfirmScene(from, code, c.s.pairingAddress(), c.pairingNotice(), time.Until(c.codeExpires))
	c.mu.Unlock()
	c.wake()
}

// choose picks the scene for the player's current state, or nil for none.
// The first case that holds wins. Caller holds c.mu.
func (c *card) choose() scene {
	spec := c.s.state.Standby()
	test := c.testCardSceneLocked()
	switch {
	case !c.s.state.Paired():
		return c.pairingScene()
	case test != nil:
		return test
	case c.confirm != nil:
		c.confirm.connected = c.s.control.connected()
		return c.confirm
	case !c.s.standby.onStandby(): // Cinefin is showing something: keep off it
		c.standbyWhenAway()
		c.endHoldWhenAway()
		return nil
	case spec != nil && spec.ShowStatus:
		return c.statusScene(spec)
	default:
		return c.linkScene()
	}
}

// pairingScene is the pairing box for the code currently accepted. Caller
// holds c.mu.
func (c *card) pairingScene() scene {
	code, expires := c.s.codes.Current()
	c.codeExpires = expires
	loaded, intro := c.s.standby.intro()
	return pairingScene{code: pairing.Format(code), address: c.s.pairingAddress(), notice: c.pairingNotice(),
		expires: expires, reveal: loaded.Add(intro)}
}

// pairingNotice is the pairing box's extra line: that a Cinefin was refused
// for its protocol (see protocol.go), else on a player upgraded from a 0.1
// release with a config.toml, that the file is no longer used.
func (c *card) pairingNotice() string {
	if n := c.s.mismatchNotice(true); n != "" {
		return n
	}
	if c.s.cfg.LegacyConfig == "" {
		return ""
	}
	return legacyConfigNotice
}

// refresh brings the screen in line with the current state and returns how
// soon it wants to run again.
func (c *card) refresh() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.s.backend
	if !b.Connected() {
		c.shown = ""
		return idleRedraw
	}
	now := time.Now()
	for {
		sc := c.choose()
		if sc == nil {
			c.key = ""
			c.clearLocked()
			return idleRedraw
		}
		if sc.key() != c.key {
			c.key, c.since = sc.key(), now
		}
		text, next, done := sc.frame(now.Sub(c.since))
		if done {
			// Hand over to the next scene in this same pass, so the screen
			// is not blank for a frame between the two. A scene just
			// started is never done, so this ends.
			c.finishedLocked(sc)
			c.key = ""
			continue
		}
		if text != c.shown && b.Send(overlayFrame("ass-events", text)) == nil {
			c.shown = text
		}
		return next
	}
}

// finishedLocked records that a scene ran to its end. Caller holds c.mu.
func (c *card) finishedLocked(sc scene) {
	if _, ok := sc.(*confirmScene); ok {
		c.confirm = nil
	}
}

// clearLocked removes whatever is on screen. Caller holds c.mu.
func (c *card) clearLocked() {
	if c.shown != "" && c.s.backend.Send(overlayFrame("none", "")) == nil {
		c.shown = ""
	}
}

// minutesLeft rounds a code's remaining life up to whole minutes, for the
// countdown ("new code in N min"); it never says less than 1.
func minutesLeft(left time.Duration) int {
	return max(1, int(math.Ceil(left.Minutes())))
}

// assText strips the characters ASS treats as markup, so a hostname cannot
// break the card's layout.
func assText(s string) string {
	return strings.NewReplacer(`{`, ``, `}`, ``, `\`, ``).Replace(s)
}

// overlayFrame builds mpv's osd-overlay command (named-argument form) for
// overlay id 1. format "none" removes it.
func overlayFrame(format, data string) []byte {
	frame, _ := json.Marshal(map[string]any{
		"command": map[string]any{
			"name":   "osd-overlay",
			"id":     1,
			"format": format,
			"data":   data,
			"res_x":  1280,
			"res_y":  720,
		},
		"request_id": agentRequestID,
	})
	return frame
}
