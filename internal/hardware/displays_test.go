package hardware

import (
	"strings"
	"testing"
)

func TestWriteDisplaysScreens(t *testing.T) {
	var b strings.Builder
	WriteDisplays(&b, nil, []Screen{
		{Index: 0, Name: "DP-2", W: 1920, H: 1080, Hz: 59.96},
		{Index: 1, Name: `\\.\DISPLAY2`},
	})
	want := "Screens (--display INDEX or NAME on a desktop):\n" +
		"  0  DP-2  1920x1080 @ 59.96 Hz\n" +
		"  1  \\\\.\\DISPLAY2\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestWriteDisplaysNone(t *testing.T) {
	var b strings.Builder
	WriteDisplays(&b, nil, nil)
	if b.String() != "No displays found.\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestWriteDisplaysWrapsModes(t *testing.T) {
	modes := strings.Fields("3840x2160 2560x1440 1920x1200 1920x1080 1680x1050 1600x900 1280x1024 1280x800 1280x720 1024x768 800x600 640x480")
	var b strings.Builder
	WriteDisplays(&b, []Connector{{Name: "HDMI-A-1", Status: "connected", Modes: modes}}, nil)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header, the connector and two lines of modes, got:\n%s", b.String())
	}
	for _, l := range lines[2:] {
		if len(l) > 80 || !strings.HasPrefix(l, "      ") {
			t.Errorf("mode line %q: over 80 columns or not indented", l)
		}
	}
	if !strings.HasPrefix(lines[2], "      3840x2160 (preferred), 2560x1440,") || !strings.HasSuffix(lines[3], "640x480") {
		t.Errorf("mode lines:\n%s", strings.Join(lines[2:], "\n"))
	}
}
