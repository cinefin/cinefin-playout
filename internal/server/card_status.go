package server

import (
	"strings"
	"time"

	"github.com/cinefin/cinefin-playout/internal/state"
)

// statusScene is the status line over standby, when the standby spec asks for
// it (show_status): one line along the bottom of the screen with no panel
// behind it. The cinema's name is on the left; on the right a coloured dot and
// a word for the link to Cinefin, then the player's name and Cinefin's
// address. It takes the place of the offline notice and the "back online"
// toast: the link state is the dot and the word.
type statusScene struct {
	cinema, player, address string // "" when there is none (the cinema and the address are left out)
	link                    linkState
}

// linkState is the control link to Cinefin as the status line shows it.
type linkState int

const (
	linkConnecting linkState = iota // not attached, for less than linkGrace
	linkReady                       // attached
	linkOffline                     // not attached for linkGrace or more
	linkCinefinOld                  // not attached; Cinefin was refused as too old
	linkPlayerOld                   // not attached; Cinefin was refused as too new
)

// linkStateAt is the link state at now for a link that has been connected (or
// not) since since.
func linkStateAt(connected bool, since, now time.Time) linkState {
	switch {
	case connected:
		return linkReady
	case now.Sub(since) < linkGrace:
		return linkConnecting
	default:
		return linkOffline
	}
}

// linkStateLocked is linkStateAt, except that a link that is down because
// Cinefin was refused for its protocol says which side needs updating.
func (c *card) linkStateLocked(connected bool, since time.Time) linkState {
	if !connected {
		if _, cinefinOld, ok := c.s.mismatch.recent(); ok {
			if cinefinOld {
				return linkCinefinOld
			}
			return linkPlayerOld
		}
	}
	return linkStateAt(connected, since, time.Now())
}

// statusScene is the status line for spec and the link's state now. The
// player's name falls back to the agent's own. Caller holds c.mu.
func (c *card) statusScene(spec *state.Standby) scene {
	connected, since := c.s.control.link()
	player := spec.PlayerName
	if player == "" {
		player = c.s.id.Name
	}
	return statusScene{
		cinema:  spec.CinemaName,
		player:  player,
		address: c.s.state.CinefinAddress(),
		link:    c.linkStateLocked(connected, since),
	}
}

func (statusScene) key() string { return "status" }

// frame redraws at the idle cadence; the card sends the text to mpv only when
// it has changed, so a steady line costs nothing.
func (s statusScene) frame(time.Duration) (string, time.Duration, bool) {
	return statusLine(s.cinema, s.player, s.address, s.link), idleRedraw, false
}

// The line, in canvas pixels: its baseline 40 px above the bottom edge,
// statusMargin in from each side.
const (
	statusBaseline = 720 - 40
	statusMargin   = 56
	// libass sits a line of text on the bottom of its line box, which is
	// deeper for the 22 px name than for the 15 px text on the right; this
	// lowers the name so both share a baseline.
	statusNameDrop = 3
)

// Longest names shown, in characters, so the two ends of the line cannot
// meet (the name takes at most about 430 px, the right end about 670 px).
const (
	statusCinemaMax  = 36
	statusPlayerMax  = 24
	statusAddressMax = 32
)

// Colours of the line.
const (
	statusName   = "#e8e8ec"
	statusWord   = "#d6d6dc"
	statusDetail = "#a6a6ac" // holds 4.5:1 over the brightest orbit light
	statusRule   = "#45454a" // the separator
	statusGreen  = "#25e88a"
	statusAmber  = "#e8b93b"
)

// statusWords is each link state's dot colour and word.
var statusWords = map[linkState][2]string{
	linkReady:      {statusGreen, "Ready"},
	linkConnecting: {statusDetail, "Connecting to Cinefin"},
	linkOffline:    {statusAmber, "Can't reach Cinefin"},
	linkCinefinOld: {statusAmber, "Cinefin needs updating"},
	linkPlayerOld:  {statusAmber, "This player needs updating"},
}

// statusLine lays out the status line as ASS events. The right end is one
// right-aligned event: the dot and the separator are drawings inline with the
// text, so they sit wherever the text's width puts them.
func statusLine(cinema, player, address string, link linkState) string {
	var events []string
	if cinema != "" {
		events = append(events, textAt(1, statusMargin, statusBaseline+statusNameDrop, 22, statusName, `\b500`,
			assText(clip(cinema, statusCinemaMax))))
	}
	detail := assText(clip(player, statusPlayerMax))
	if address != "" {
		detail += " · Cinefin " + assText(clip(address, statusAddressMax))
	}
	w := statusWords[link]
	gap := `\h\h`
	right := inlineShape(w[0], "", rectPath(8, 8)) + colourTag(statusWord) + gap + w[1] + gap + `\h` +
		// A hairline: 1 unit wide at the 1080p scale (see hairScale), 12 px
		// tall, lowered to centre on the text.
		inlineShape(statusRule, hairScale+`\pbo3`, rectPath(1, 18)) + `{\fscx100\fscy100}` +
		colourTag(statusDetail) + gap + `\h` + detail
	events = append(events, textAt(3, 1280-statusMargin, statusBaseline, 15, statusWord, "", right))
	return strings.Join(events, "\n")
}

// inlineShape is a drawing in the flow of a line of text, in colour, with
// extra override tags.
func inlineShape(colour, tags, path string) string {
	return `{\1c` + assColour(colour) + tags + `\p1}` + path + `{\p0}`
}

// colourTag switches the text colour.
func colourTag(colour string) string { return `{\1c` + assColour(colour) + `}` }

// clip shortens s to at most n characters, ending it with an ellipsis when it
// was cut.
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
