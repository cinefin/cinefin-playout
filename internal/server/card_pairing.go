package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/cinefin/cinefin-playout/internal/pairing"
)

// pairingScene is the pairing box shown while the player is unpaired, over
// the System Ident on standby (the Cinefin mark centred on the screen, roughly
// y 300-420 on the canvas), so the scene is only a box below it: the code on
// the left, and on the right what to do with it, the address for adding the
// player by hand, and how long the code has left. notice, when set, is one
// more line under the countdown (see legacyConfigNotice).
//
// The box stays off the screen while the ident's intro animates the mark in,
// then fades in over pairingFade. reveal is when the fade starts: when standby
// was loaded plus the ident's intro (see standby.intro). It is timed from that
// load, not from when the scene started, so a new code or a redraw never fades
// it again, and a box first drawn long after the load is simply there.
type pairingScene struct {
	code, address, notice string
	expires               time.Time
	reveal                time.Time
}

// legacyConfigNotice is the pairing box's extra line on a player upgraded from
// a 0.1 release that was started with, or left behind, a config.toml.
const legacyConfigNotice = "Updated from an older version: config.toml is no longer used."

// pairingRedraw is how often the pairing box is redrawn, for its countdown bar.
const pairingRedraw = 5 * time.Second

// pairingFade is how long the box takes to fade in once the ident's intro is
// over.
const pairingFade = 800 * time.Millisecond

// The box, in canvas pixels.
const (
	boxX, boxY, boxW, boxH = 260, 468, 760, 196
	boxPadX, boxPadY       = 32, 26
	codeColW               = 300 // the left column, holding the code
	colGap                 = 32  // between the divider and the right column
	boxMidY                = boxY + boxH/2

	leftX    = boxX + boxPadX
	dividerX = leftX + codeColW
	rightX   = dividerX + 1 + colGap
	rightW   = boxX + boxW - boxPadX - rightX // 363

	barW = 220 // the countdown bar; its label follows it
)

// boxFillOpacity is the opacity of the box's fill: translucent, so the
// ident's orbiting light shows through a little behind the text.
const boxFillOpacity = 0.75

// Colours of the box.
const (
	boxFill    = "#101012"
	boxLine    = "#2e2e33"
	textBright = "#f2f0f0"
	textLabel  = "#a6a6ac"
	textMuted  = "#8e8e96"
	barTrack   = "#2a2a2f"
	barFill    = "#3a7bff"
)

func (pairingScene) key() string { return "pairing" }

func (p pairingScene) frame(time.Duration) (string, time.Duration, bool) {
	text, next := p.at(time.Now())
	return text, next, false
}

// at renders the scene at now: nothing until reveal, then the box fading in,
// then the box. It sleeps until reveal, redraws at frame rate during the fade,
// and after that every pairingRedraw for the countdown and as the code
// expires.
func (p pairingScene) at(now time.Time) (string, time.Duration) {
	since := now.Sub(p.reveal)
	if since < 0 {
		return "", -since
	}
	left := p.expires.Sub(now)
	opacity := confirmProgress(since, 0, pairingFade)
	next := min(pairingRedraw, left+50*time.Millisecond) // redraw as the code expires
	switch {
	case opacity < 1:
		next = frameRedraw
	case next <= 0:
		next = idleRedraw
	}
	return pairingBox(p.code, p.address, p.notice, left, opacity), next
}

// pairingBox lays out the box as ASS events, one per line; left is the code's
// remaining life, notice an optional last line in the right column, and
// opacity scales the whole box (fill, border and text) for its fade-in. It is
// drawn in three parts (boxPanel, codeColumn and pairingHelp) so the paired
// confirmation can start from exactly this box and animate each part.
func pairingBox(code, address, notice string, left time.Duration, opacity float64) string {
	events := boxPanel(0, boxFill, boxFillOpacity*opacity, boxLine, opacity)
	events = append(events, codeColumn(code, 0, boxLine, textLabel, textBright, opacity)...)
	events = append(events, pairingHelp(address, notice, left, 0, opacity)...)
	return strings.Join(events, "\n")
}

// Text tops, so each column is centred on the box's middle. libass sets a line
// of size N about N pixels tall.
const (
	labelY  = boxMidY - 38 // 14 px label, 8 px gap, 68 px code (its line box has room below the digits)
	codeY   = labelY + 22
	instrY  = boxMidY - 56 // two 22 px lines, 12, two 15 px lines, 12, 14 px row
	addrY   = instrY + 44 + 12
	countY  = addrY + 30 + 12
	barY    = countY + 5
	countTx = rightX + barW + 12

	// A notice adds a 14 px row 12 px under the countdown, and the right
	// column moves up by half of that to stay centred (138 of the 144 px
	// inside the padding).
	noticeRow = 12 + 14
	noticeY   = countY + 14 + 12
)

// boxPanel is the box itself, dy pixels below its place: the fill and its
// hairline border.
func boxPanel(dy int, fill string, fillOpacity float64, line string, opacity float64) []string {
	return []string{
		rect(boxX, boxY+dy, boxW, boxH, fill, fillOpacity),
		border(boxX, boxY+dy, boxW, boxH, line, opacity),
	}
}

// codeColumn is the left column, dy pixels down: the divider, the label and
// the code, in the given colours and opacity.
func codeColumn(code string, dy int, divider, label, digits string, opacity float64) []string {
	return []string{
		vline(dividerX, boxY+boxPadY+dy, boxH-2*boxPadY, divider, opacity),
		textAt(7, leftX, labelY+dy, 14, label, `\fsp2`+fade(opacity), "PAIRING CODE"),
		textAt(7, leftX, codeY+dy, 68, digits, fade(opacity), code),
	}
}

// pairingHelp is the right column, dy pixels down: where to enter the code,
// the address for adding the player by hand, the countdown bar for the code's
// remaining life (left), and the notice, if any.
func pairingHelp(address, notice string, left time.Duration, dy int, opacity float64) []string {
	if notice != "" {
		dy -= noticeRow / 2
	}
	fill := int(float64(barW) * left.Seconds() / pairing.CodeLifetime.Seconds())
	fill = min(barW, max(0, fill))
	events := []string{
		textAt(7, rightX, instrY+dy, 22, textBright, fade(opacity),
			`In Cinefin, go to {\b1}Settings › Playout{\b0}\Nand enter this code`),
		textAt(7, rightX, addrY+dy, 15, textMuted, fade(opacity), `Not listed? Add it by address\N`+assText(address)),
		rect(rightX, barY+dy, barW, 4, barTrack, opacity),
	}
	if fill > 0 {
		events = append(events, rect(rightX, barY+dy, fill, 4, barFill, opacity))
	}
	events = append(events,
		textAt(7, countTx, countY+dy, 14, textMuted, fade(opacity), fmt.Sprintf("new code in %d min", minutesLeft(left))))
	if notice != "" {
		events = append(events, textAt(7, rightX, noticeY+dy, 14, textMuted, fade(opacity), assText(notice)))
	}
	return events
}
