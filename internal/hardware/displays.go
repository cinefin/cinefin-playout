package hardware

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Connector is one DRM connector (Linux): its name, sysfs status and the
// resolutions the screen on it offers, preferred first.
type Connector struct {
	Name   string
	Status string
	Modes  []string
}

// Screens lists the desktop session's screens (xrandr / wlr-randr / sysfs on
// Linux, the display APIs on Windows), in mpv --screen order. Nil when none
// are found.
func Screens(ctx context.Context) []Screen { return enumScreens(ctx) }

// WriteDisplays prints what --list-displays shows: the DRM connectors (Linux)
// with their status and modes, then the desktop session's screens. Either
// list may be empty.
func WriteDisplays(w io.Writer, connectors []Connector, screens []Screen) {
	if len(connectors) == 0 && len(screens) == 0 {
		fmt.Fprintln(w, "No displays found.")
		return
	}
	if len(connectors) > 0 {
		fmt.Fprintln(w, "Connectors (--display NAME; --mode WxH[@Hz] when mpv draws straight to the screen):")
		width := 0
		for _, c := range connectors {
			width = max(width, len(c.Name))
		}
		for _, c := range connectors {
			fmt.Fprintf(w, "  %-*s  %s\n", width, c.Name, c.Status)
			if len(c.Modes) > 0 {
				modes := append([]string{c.Modes[0] + " (preferred)"}, c.Modes[1:]...)
				fmt.Fprintln(w, wrap(modes, "      ", 80))
			}
		}
	}
	if len(screens) > 0 {
		if len(connectors) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "Screens (--display INDEX or NAME on a desktop):")
		for _, s := range screens {
			line := fmt.Sprintf("  %d  %s", s.Index, s.Name)
			if s.W > 0 && s.H > 0 {
				line += fmt.Sprintf("  %dx%d", s.W, s.H)
			}
			if s.Hz > 0 {
				line += " @ " + strconv.FormatFloat(s.Hz, 'f', 2, 64) + " Hz"
			}
			fmt.Fprintln(w, line)
		}
	}
}

// wrap joins items with ", " into lines of at most width columns, each
// starting with indent.
func wrap(items []string, indent string, width int) string {
	var lines []string
	cur := indent
	for i, it := range items {
		if i < len(items)-1 {
			it += ","
		}
		if cur != indent && len(cur)+1+len(it) > width {
			lines = append(lines, cur)
			cur = indent
		}
		if cur != indent {
			cur += " "
		}
		cur += it
	}
	return strings.Join(append(lines, cur), "\n")
}
