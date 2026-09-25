//go:build linux

package hardware

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestDRMConnectorsMissingRoot(t *testing.T) {
	if got := drmConnectors(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Fatalf("missing root should yield empty, got %v", got)
	}
}
