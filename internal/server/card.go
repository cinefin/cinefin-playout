package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cinefin/cinefin-playout/internal/pairing"
)

// cardRequestID tags the agent's own mpv commands (the pairing card). Replies
// carrying it are not forwarded to the control client (see control.fromMPV).
const cardRequestID = 2_000_000_001

// card draws the pairing card on the player's screen while it is unpaired: the
// player's name, the pairing code in large type, and its address for adding it
// by hand. It uses an mpv OSD overlay over IPC, so it needs no image files and
// works on a desktop window and on DRM alike.
//
// A 1 s loop keeps the screen in step with the state: it draws when mpv is
// reachable and the text changed (a new code, a new address), and removes the
// card once the player is paired. forget makes the next pass redraw, for a
// freshly started mpv that has no overlay.
type card struct {
	s *Server

	mu    sync.Mutex
	shown string // text currently on screen, "" when none
}

func (c *card) run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		c.refresh()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// forget marks the screen as blank, so the next refresh draws the card again.
func (c *card) forget() {
	c.mu.Lock()
	c.shown = ""
	c.mu.Unlock()
}

// refresh brings the screen in line with the current state.
func (c *card) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.s.backend
	if !b.Connected() {
		c.shown = ""
		return
	}
	if c.s.state.Paired() {
		if c.shown != "" && b.Send(overlayFrame("none", "")) == nil {
			c.shown = ""
		}
		return
	}
	code, _ := c.s.codes.Current()
	text := cardText(c.s.id.Name, pairing.Format(code), c.s.pairingAddress())
	if text != c.shown && b.Send(overlayFrame("ass-events", text)) == nil {
		c.shown = text
	}
}

// cardText lays the card out as ASS events (one per line) on a 1280x720 canvas
// that mpv scales to the screen: the Cinefin logo at the top, then the text,
// centred. Colours are ASS &HBBGGRR&.
func cardText(name, code, address string) string {
	var b strings.Builder
	for _, ev := range cardLogo(559, 128) {
		b.WriteString(ev + "\n")
	}
	b.WriteString(`{\an5\pos(640,400)\bord0\shad0\1c&HF2F0F0&\fs34}Pair this player with Cinefin\N`)
	b.WriteString(`{\fs21\1c&HACA6A6&}In Cinefin, open Settings › Playout, choose `)
	b.WriteString(assText(name))
	b.WriteString(` and enter this code\N\N`)
	b.WriteString(`{\fs120\fsp6\1c&HF2F0F0&}` + code + `\N\N`)
	b.WriteString(`{\fsp0\fs22\1c&HACA6A6&}Not in the list? Add it by address: ` + assText(address) + `\N`)
	b.WriteString(`{\fs19\1c&H7A7171&}The code changes every few minutes.`)
	return b.String()
}

// logoScale sizes the mark: its 36x48 drawing units become 45x60 on the canvas.
const logoScale = 125

// cardLogo draws the Cinefin logo with its top-left corner at (x, y): the mark
// (a film frame with sprocket holes and three colour exposures, the same
// geometry as the status page's SVG) as ASS vector drawings, and the wordmark
// beside it. No image file is needed.
func cardLogo(x, y int) []string {
	draw := func(colour, path string) string {
		return fmt.Sprintf(`{\an7\pos(%d,%d)\bord0\shad0\fscx%d\fscy%d\1c&H%s&\p1}%s{\p0}`,
			x, y, logoScale, logoScale, colour, path)
	}
	// The frame: an outer rectangle with the inner one wound the other way,
	// which cuts it out, plus the eight sprocket holes.
	frame := "m 0 0 l 36 0 36 48 0 48 m 3 3 l 3 45 33 45 33 3"
	for _, hx := range []int{6, 26} {
		for _, hy := range []int{6, 16, 26, 36} {
			frame += fmt.Sprintf(" m %d %d l %d %d %d %d %d %d", hx, hy, hx+4, hy, hx+4, hy+6, hx, hy+6)
		}
	}
	square := func(sy int) string {
		return fmt.Sprintf("m 13 %d l 23 %d 23 %d 13 %d", sy, sy, sy+10, sy+10)
	}
	wordX := x + 36*logoScale/100 + 16
	wordY := y + 48*logoScale/100/2
	return []string{
		draw("ACA6A6", frame),
		draw("4D2FFF", square(6)),  // #FF2F4D
		draw("8AE825", square(19)), // #25E88A
		draw("FF7B3A", square(32)), // #3A7BFF
		fmt.Sprintf(`{\an4\pos(%d,%d)\bord0\shad0\b1\fs44\1c&HF2F0F0&}Cinefin`, wordX, wordY),
	}
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
		"request_id": cardRequestID,
	})
	return frame
}
