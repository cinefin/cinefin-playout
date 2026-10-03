package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cinefin/cinefin-playout/internal/hardware"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// The test card. Cinefin's "Add a player" wizard turns it on (POST /testcard)
// so the user can check that the picture is on the right screen and not
// cropped: corner marks at the edges, the safe area, the player's name and
// output, colour bars and a grey ramp, and a Left and a Right speaker box that
// light up while the test sound (testsound.go) plays on that side. It covers
// the whole screen, and takes precedence over every scene but the pairing box.
// It turns itself off after testCardFor, and on unpair.

// testCardFor is how long the test card stays on when nothing turns it off.
const testCardFor = 5 * time.Minute

// Layout of the test card on the 1280x720 canvas.
const (
	cornerInset, cornerArm, cornerThick = 16, 56, 3
	safeX, safeY                        = 64, 36 // the safe area's inset from the left/right and top/bottom

	tcTitleY  = 150 // "TEST CARD"
	tcNameY   = 174 // the player's name, 88 px
	tcOutputY = 290 // the output line, 18 px

	tcBarsX, tcBarsW = 200, 880
	tcBarsY, tcBarsH = 420, 44
	tcRampY, tcRampH = 470, 36

	tcSpeakerY, tcSpeakerW, tcSpeakerH = 560, 200, 56
	tcSpeakerInset                     = 96 // from the left and right edges

	tcNameSize, tcNameMinSize = 88, 40
	tcNameMaxRunes            = 48
	tcNameMaxW                = 1100 // widest the name may be, canvas pixels
)

// The test card's colours.
const (
	tcSafeLine    = "#34343b"
	tcTitle       = "#8e8e96"
	tcOutput      = "#a9a9b1"
	tcSpeakerLine = "#2e2e33"
	tcSpeakerText = "#6a6a72"
	tcSpeakerOn   = "#3a7bff"
)

// tcBars are the 75% colour bars, left to right.
var tcBars = []string{"#bfbfbf", "#bfbf00", "#00bfbf", "#00bf00", "#bf00bf", "#bf0000", "#0000bf"}

// testCardScene is the test card for a player named name on the output
// described by output ("" when unknown); sounding is the channel the test
// sound is playing ("left", "right" or "").
type testCardScene struct {
	name, output, sounding string
	until                  time.Time // when the card turns itself off
}

func (testCardScene) key() string { return "testcard" }

func (sc testCardScene) frame(time.Duration) (string, time.Duration, bool) {
	return testCardText(sc.name, sc.output, sc.sounding), min(idleRedraw, time.Until(sc.until)), false
}

// testCardText lays out the test card as ASS events.
func testCardText(name, output, sounding string) string {
	ev := []string{rect(0, 0, 1280, 720, "#000000", 1)}
	ev = append(ev, cornerMarks()...)
	ev = append(ev,
		border(safeX, safeY, 1280-2*safeX, 720-2*safeY, tcSafeLine, 1),
		textAt(8, 640, tcTitleY, 15, tcTitle, `\fsp4`, "TEST CARD"),
	)
	name, size := fitName(assText(name))
	ev = append(ev, textAt(8, 640, tcNameY+(tcNameSize-size)/2, size, textBright, `\b300`, name))
	if output != "" {
		ev = append(ev, textAt(8, 640, tcOutputY, 18, tcOutput, "", assText(output)))
	}
	for i, c := range tcBars {
		x0, x1 := tcBarsX+i*tcBarsW/len(tcBars), tcBarsX+(i+1)*tcBarsW/len(tcBars)
		ev = append(ev, rect(x0, tcBarsY, x1-x0, tcBarsH, c, 1))
	}
	const steps = 11
	for i := range steps {
		x0, x1 := tcBarsX+i*tcBarsW/steps, tcBarsX+(i+1)*tcBarsW/steps
		v := i * 255 / (steps - 1)
		ev = append(ev, rect(x0, tcRampY, x1-x0, tcRampH, fmt.Sprintf("#%02x%02x%02x", v, v, v), 1))
	}
	// The ramp's first step is black like the background, so a hairline
	// outline shows where the ramp starts, in line with the bars.
	ev = append(ev, border(tcBarsX, tcRampY, tcBarsW, tcRampH, tcSafeLine, 1))
	ev = append(ev, speakerBox(tcSpeakerInset, "Left", sounding == "left")...)
	ev = append(ev, speakerBox(1280-tcSpeakerInset-tcSpeakerW, "Right", sounding == "right")...)
	return strings.Join(ev, "\n")
}

// cornerMarks are the white L-shaped marks in the four corners of the canvas.
func cornerMarks() []string {
	var ev []string
	for _, c := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		right, bottom := c[0], c[1]
		x, y := cornerInset, cornerInset
		if right {
			x = 1280 - cornerInset - cornerArm
		}
		if bottom {
			y = 720 - cornerInset - cornerThick
		}
		ev = append(ev, rect(x, y, cornerArm, cornerThick, "#ffffff", 1)) // across
		x, y = cornerInset, cornerInset
		if right {
			x = 1280 - cornerInset - cornerThick
		}
		if bottom {
			y = 720 - cornerInset - cornerArm
		}
		ev = append(ev, rect(x, y, cornerThick, cornerArm, "#ffffff", 1)) // down
	}
	return ev
}

// speakerPath is a small loudspeaker, 12 x 20 drawing units.
const speakerPath = "m 0 6 l 5 6 12 0 12 20 5 14 0 14"

// speakerBox is one speaker box with its left edge at x: highlighted (blue
// border, light fill, bright text) while its side is sounding. The icon and
// label are centred together in the box.
func speakerBox(x int, label string, on bool) []string {
	const size, iconW, gap = 22, 12, 14
	line, text := tcSpeakerLine, tcSpeakerText
	var ev []string
	if on {
		line, text = tcSpeakerOn, textBright
		ev = append(ev,
			rect(x, tcSpeakerY, tcSpeakerW, tcSpeakerH, tcSpeakerOn, 0.18),
			border(x+1, tcSpeakerY+1, tcSpeakerW-2, tcSpeakerH-2, line, 1))
	}
	labelW := int(textEms(label) * size * 0.85) // the regular weight is a little narrower than textEms
	left := x + (tcSpeakerW-iconW-gap-labelW)/2
	mid := tcSpeakerY + tcSpeakerH/2
	return append(ev,
		border(x, tcSpeakerY, tcSpeakerW, tcSpeakerH, line, 1),
		shape(left, mid-10, text, 1, speakerPath),
		textAt(4, left+iconW+gap, mid, size, text, "", label))
}

// fitName shortens a long name and picks a font size that fits it across the
// canvas, from tcNameSize down to tcNameMinSize.
func fitName(name string) (string, int) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > tcNameMaxRunes {
		name = strings.TrimSpace(string([]rune(name)[:tcNameMaxRunes-1])) + "…"
	}
	size := int(float64(tcNameMaxW) / max(0.01, textEms(name)))
	return name, max(tcNameMinSize, min(tcNameSize, size))
}

// textEms estimates how wide text is in ems: there is no measuring from here,
// so it takes generous widths for a sans (wider than the light weight drawn),
// erring towards a smaller size over running off the screen.
func textEms(text string) float64 {
	var w float64
	for _, r := range text {
		switch {
		case r == ' ':
			w += 0.25
		case unicode.IsLower(r):
			w += 0.45
		case unicode.IsDigit(r):
			w += 0.5
		default: // capitals, marks, other scripts
			w += 0.6
		}
	}
	return w
}

// outputLine describes the output the player draws on, best effort: the
// connector (DRM) or screen name, and its resolution and refresh rate when
// known, from the launch config and the displays found (screens), with
// connected the connected DRM connectors. Unknown parts are left out.
func outputLine(hc hostconfig.HostConfig, screens []hardware.Screen, connected []string) string {
	g := hc.Graphics
	var name string
	var scr *hardware.Screen
	byName := func(n string) *hardware.Screen {
		for i := range screens {
			if screens[i].Name == n {
				return &screens[i]
			}
		}
		return nil
	}
	if g.Mode == hostconfig.ModeDRM {
		name = g.DRMConnector
		if name == "" && len(connected) > 0 {
			name = connected[0]
		}
		scr = byName(name)
		if w, h, hz, ok := parseDRMMode(g.DRMMode); ok {
			scr = &hardware.Screen{W: w, H: h, Hz: hz}
		}
	} else if g.ScreenName != "" {
		name, scr = g.ScreenName, byName(g.ScreenName)
	} else {
		for i := range screens {
			if screens[i].Index == g.Screen {
				scr = &screens[i]
			}
		}
		name = "Screen " + strconv.Itoa(g.Screen+1)
		if scr != nil && scr.Name != "" {
			name = scr.Name
		}
	}
	parts := []string{strings.TrimPrefix(name, `\\.\`)} // Windows: \\.\DISPLAY1
	if scr != nil && scr.W > 0 && scr.H > 0 {
		parts = append(parts, fmt.Sprintf("%d × %d", scr.W, scr.H))
	}
	if scr != nil && scr.Hz > 0 {
		parts = append(parts, strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", scr.Hz), "0"), ".")+" Hz")
	}
	if parts[0] == "" {
		parts = parts[1:]
	}
	return strings.Join(parts, " · ")
}

// parseDRMMode reads mpv's --drm-mode when it names a mode ("1920x1080" or
// "1920x1080@60"); "preferred", "highest" and indexes say nothing.
func parseDRMMode(mode string) (w, h int, hz float64, ok bool) {
	res, rate, _ := strings.Cut(mode, "@")
	ws, hs, found := strings.Cut(res, "x")
	if !found {
		return 0, 0, 0, false
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, 0, false
	}
	hz, _ = strconv.ParseFloat(rate, 64)
	return w, h, hz, true
}

// playerName is the name the test card shows: the standby spec's player name,
// else the agent's own.
func (s *Server) playerName() string {
	if spec := s.state.Standby(); spec != nil && spec.PlayerName != "" {
		return spec.PlayerName
	}
	return s.id.Name
}

// setTestCard turns the test card on (for testCardFor from now) or off. On,
// it looks up the output in the background (the display tools can take a
// moment) and redraws when it has it.
func (c *card) setTestCard(on bool) {
	c.mu.Lock()
	c.testUntil = time.Time{}
	if on {
		c.testUntil = time.Now().Add(testCardFor)
	}
	c.mu.Unlock()
	c.wake()
	if on {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := outputLine(c.s.state.Launch(), hardware.Screens(ctx), hardware.ConnectedDRMConnectors())
			c.mu.Lock()
			c.testOutput = out
			c.mu.Unlock()
			c.wake()
		}()
	}
}

// testCardLeft is how long the test card has left, 0 when it is off.
func (c *card) testCardLeft() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return max(0, time.Until(c.testUntil))
}

// testCardSceneLocked is the test card while it is on, else nil. Caller holds
// c.mu.
func (c *card) testCardSceneLocked() scene {
	if c.testUntil.IsZero() {
		return nil
	}
	if !time.Now().Before(c.testUntil) {
		c.testUntil = time.Time{}
		return nil
	}
	return testCardScene{name: c.s.playerName(), output: c.testOutput, sounding: c.s.tone.channel(), until: c.testUntil}
}

// testCardStatus describes the test card for /status and POST /testcard.
func (s *Server) testCardStatus() map[string]any {
	left := s.card.testCardLeft()
	return map[string]any{"on": left > 0, "off_in_s": int(left.Round(time.Second).Seconds())}
}

// handleTestCard turns the test card on or off: {"on": true|false}.
func (s *Server) handleTestCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil || body.On == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `body must be {"on": true|false}`})
		return
	}
	s.card.setTestCard(*body.On)
	writeJSON(w, http.StatusOK, s.testCardStatus())
}
