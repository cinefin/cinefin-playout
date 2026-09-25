//go:build linux

package hardware

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDRMRoot is the sysfs DRM directory on Linux.
const DefaultDRMRoot = "/sys/class/drm"

// drmConnectors lists connectors under drmRoot (usually /sys/class/drm). Each
// entry is a directory like "card1-HDMI-A-1" containing a "status" file; the
// connector name is the part after the "cardN-" prefix. Order is stable
// (sorted) so the UI dropdown is deterministic. Returns all connectors
// regardless of connected/disconnected status.
func drmConnectors(drmRoot string) []string {
	statuses := DRMConnectorStatuses(drmRoot)
	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DRMConnectorStatuses maps connector name (e.g. "HDMI-A-1") → sysfs status
// string ("connected" / "disconnected" / "unknown"). drmRoot defaults to
// DefaultDRMRoot when empty. Used by both /hardware enumeration and the DRM
// hotplug watcher. Missing/unreadable root → empty map.
func DRMConnectorStatuses(drmRoot string) map[string]string {
	if drmRoot == "" {
		drmRoot = DefaultDRMRoot
	}
	out := map[string]string{}
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.Contains(name, "-") {
			continue
		}
		statusPath := filepath.Join(drmRoot, name, "status")
		data, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}
		connector := name
		if idx := strings.Index(name, "-"); idx >= 0 {
			connector = name[idx+1:]
		}
		out[connector] = strings.TrimSpace(string(data))
	}
	return out
}
