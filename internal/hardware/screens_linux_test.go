//go:build linux

package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseXrandr(t *testing.T) {
	out := `Screen 0: minimum 320 x 200, current 3840 x 2160, maximum 16384 x 16384
HDMI-1 connected primary 3840x2160+0+0 (normal left inverted right) 600mm x 340mm
   3840x2160     60.00*+  30.00
   1920x1080     60.00    59.94
DP-1 disconnected (normal left inverted right x axis y axis)
DP-2 connected 1920x1080+3840+0 (normal left inverted right) 510mm x 290mm
   1920x1080     59.94*+
`
	got := parseXrandr(out)
	if len(got) != 2 {
		t.Fatalf("want 2 connected screens, got %d: %+v", len(got), got)
	}
	if got[0] != (Screen{Index: 0, Name: "HDMI-1", W: 3840, H: 2160, Hz: 60.00}) {
		t.Errorf("screen 0 = %+v", got[0])
	}
	if got[1] != (Screen{Index: 1, Name: "DP-2", W: 1920, H: 1080, Hz: 59.94}) {
		t.Errorf("screen 1 = %+v", got[1])
	}
}

func TestParseWlrRandr(t *testing.T) {
	out := `HDMI-A-1 "Samsung Electric Company SAMSUNG (HDMI-A-1)"
  Physical size: 600x340 mm
  Enabled: yes
  Modes:
    3840x2160 px, 60.000000 Hz (preferred, current)
    1920x1080 px, 60.000000 Hz
DP-1 "Unknown"
  Enabled: no
`
	got := parseWlrRandr(out)
	if len(got) != 2 {
		t.Fatalf("want 2 outputs, got %d: %+v", len(got), got)
	}
	if got[0] != (Screen{Index: 0, Name: "HDMI-A-1", W: 3840, H: 2160, Hz: 60.0}) {
		t.Errorf("output 0 = %+v", got[0])
	}
	if got[1].Name != "DP-1" || got[1].W != 0 {
		t.Errorf("disabled output should have a name and no resolution: %+v", got[1])
	}
}

func TestSysfsScreens(t *testing.T) {
	root := t.TempDir()
	mk := func(dir, status, modes string) {
		d := filepath.Join(root, dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "status"), []byte(status+"\n"), 0o644)
		if modes != "" {
			os.WriteFile(filepath.Join(d, "modes"), []byte(modes), 0o644)
		}
	}
	mk("card0-HDMI-A-1", "connected", "3840x2160\n1920x1080\n")
	mk("card0-DP-1", "disconnected", "")

	got := sysfsScreens(root)
	if len(got) != 1 {
		t.Fatalf("want 1 connected screen, got %d: %+v", len(got), got)
	}
	if got[0].Name != "HDMI-A-1" || got[0].W != 3840 || got[0].H != 2160 {
		t.Errorf("sysfs screen = %+v", got[0])
	}
}
