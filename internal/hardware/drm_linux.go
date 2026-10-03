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

// walkConnectors calls fn for each connector under drmRoot (DefaultDRMRoot
// when empty) with its name (e.g. "HDMI-A-1"), its sysfs directory and its
// status ("connected" / "disconnected" / "unknown"). Entries without a
// readable status file (card1, renderD128, version) are skipped, as is a
// missing or unreadable root.
func walkConnectors(drmRoot string, fn func(name, dir, status string)) {
	if drmRoot == "" {
		drmRoot = DefaultDRMRoot
	}
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		_, connector, ok := strings.Cut(e.Name(), "-")
		if !ok {
			continue
		}
		dir := filepath.Join(drmRoot, e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "status"))
		if err != nil {
			continue
		}
		fn(connector, dir, strings.TrimSpace(string(data)))
	}
}

// DRMConnectorStatuses maps connector name (e.g. "HDMI-A-1") → sysfs status
// string ("connected" / "disconnected" / "unknown"). drmRoot defaults to
// DefaultDRMRoot when empty. Used by both /hardware enumeration and the DRM
// hotplug watcher. Missing/unreadable root → empty map.
func DRMConnectorStatuses(drmRoot string) map[string]string {
	out := map[string]string{}
	walkConnectors(drmRoot, func(name, _, status string) { out[name] = status })
	return out
}

// DRMConnectorList lists the connectors under drmRoot (DefaultDRMRoot when
// empty), sorted by name, each with its status and the modes from its sysfs
// "modes" file. The kernel lists the preferred mode first and repeats a
// resolution once per refresh rate (which sysfs does not show); the repeats
// are dropped here.
func DRMConnectorList(drmRoot string) []Connector {
	var out []Connector
	walkConnectors(drmRoot, func(name, dir, status string) {
		c := Connector{Name: name, Status: status}
		if data, err := os.ReadFile(filepath.Join(dir, "modes")); err == nil {
			seen := map[string]bool{}
			for _, m := range strings.Fields(string(data)) {
				if !seen[m] {
					seen[m] = true
					c.Modes = append(c.Modes, m)
				}
			}
		}
		out = append(out, c)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ConnectedDRMConnectors lists the connectors with a screen attached, sorted,
// so "the first screen" is stable between runs.
func ConnectedDRMConnectors() []string {
	var out []string
	for name, status := range DRMConnectorStatuses("") {
		if status == "connected" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
