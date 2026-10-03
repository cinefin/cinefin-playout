package pairing

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Every line of the box is the same width, and it carries the formatted code,
// the name and the address.
func TestBanner(t *testing.T) {
	lines := Banner("a-rather-long-player-name-for-the-box", "482913", "http://10.0.0.5:8089")
	w := utf8.RuneCountInString(lines[0])
	for i, l := range lines {
		if n := utf8.RuneCountInString(l); n != w {
			t.Errorf("line %d is %d wide, want %d: %q", i, n, w, l)
		}
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"482 913", "a-rather-long-player-name-for-the-box", "http://10.0.0.5:8089"} {
		if !strings.Contains(all, want) {
			t.Errorf("banner missing %q:\n%s", want, all)
		}
	}
}
