//go:build linux

package hardware

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeDRM builds a /sys/class/drm-style tree: connectors are "cardN-<name>"
// dirs each with a status file.
func fakeDRM(t *testing.T, statuses map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// A non-connector entry (card1, renderD128, version file) must be ignored.
	if err := os.Mkdir(filepath.Join(root, "card1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version"), []byte("drm 1.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, status := range statuses {
		dir := filepath.Join(root, "card1-"+name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDRMConnectors(t *testing.T) {
	root := fakeDRM(t, map[string]string{
		"HDMI-A-1": "disconnected",
		"DP-1":     "connected",
		"DP-2":     "connected",
	})
	got := drmConnectors(root)
	want := []string{"DP-1", "DP-2", "HDMI-A-1"} // sorted, prefix stripped
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drmConnectors = %v, want %v", got, want)
	}
}

func TestDRMConnectorStatuses(t *testing.T) {
	root := fakeDRM(t, map[string]string{
		"HDMI-A-1": "disconnected",
		"DP-2":     "connected",
	})
	got := DRMConnectorStatuses(root)
	if got["HDMI-A-1"] != "disconnected" || got["DP-2"] != "connected" {
		t.Fatalf("statuses = %v", got)
	}
	if _, ok := got["card1"]; ok {
		t.Error("non-connector 'card1' must not appear")
	}
}

func TestDRMConnectorList(t *testing.T) {
	root := fakeDRM(t, map[string]string{
		"HDMI-A-1": "connected",
		"DP-1":     "disconnected",
	})
	// The kernel repeats a resolution once per refresh rate; the list keeps
	// the first of each, preferred first.
	modes := "3840x2160\n3840x2160\n1920x1080\n3840x2160\n1280x720\n"
	if err := os.WriteFile(filepath.Join(root, "card1-HDMI-A-1", "modes"), []byte(modes), 0o644); err != nil {
		t.Fatal(err)
	}
	// A disconnected connector has an empty modes file.
	if err := os.WriteFile(filepath.Join(root, "card1-DP-1", "modes"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := DRMConnectorList(root)
	want := []Connector{
		{Name: "DP-1", Status: "disconnected"},
		{Name: "HDMI-A-1", Status: "connected", Modes: []string{"3840x2160", "1920x1080", "1280x720"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DRMConnectorList = %+v, want %+v", got, want)
	}

	var b strings.Builder
	WriteDisplays(&b, got, nil)
	wantOut := "Connectors (--display NAME; --mode WxH[@Hz] when mpv draws straight to the screen):\n" +
		"  DP-1      disconnected\n" +
		"  HDMI-A-1  connected\n" +
		"      3840x2160 (preferred), 1920x1080, 1280x720\n"
	if b.String() != wantOut {
		t.Errorf("WriteDisplays =\n%s\nwant\n%s", b.String(), wantOut)
	}
}

func TestDRMConnectorsMissingRoot(t *testing.T) {
	if got := drmConnectors(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Fatalf("missing root should yield empty, got %v", got)
	}
}
