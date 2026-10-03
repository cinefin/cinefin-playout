package pairing

import (
	"strings"
	"unicode/utf8"
)

// Banner lays out the pairing code for the log as a box, so it stands out from
// the startup lines on a headless box where the log is the only screen besides
// the TV. It returns one string per line.
func Banner(name, code, address string) []string {
	body := []string{
		"PAIR THIS PLAYER WITH CINEFIN",
		"",
		"Code:     " + Format(code),
		"Player:   " + name,
		"Address:  " + address,
		"",
		"In Cinefin, open Settings > Playout, choose this player",
		"and enter the code. If it is not listed, add it by its",
		"address. The code changes every few minutes.",
	}
	width := 0
	for _, l := range body {
		width = max(width, utf8.RuneCountInString(l))
	}
	out := make([]string, 0, len(body)+2)
	out = append(out, "┌"+strings.Repeat("─", width+4)+"┐")
	for _, l := range body {
		out = append(out, "│  "+l+strings.Repeat(" ", width-utf8.RuneCountInString(l))+"  │")
	}
	out = append(out, "└"+strings.Repeat("─", width+4)+"┘")
	return out
}
