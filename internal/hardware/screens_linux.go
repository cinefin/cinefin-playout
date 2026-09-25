//go:build linux

package hardware

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// enumScreens lists connected displays, best-effort, in mpv --screen order.
// It prefers the live session's tools (xrandr on X11, wlr-randr on wlroots
// Wayland) and falls back to sysfs (which works headless, on a DRM/KMS console).
func enumScreens(ctx context.Context) []Screen {
	if os.Getenv("DISPLAY") != "" {
		if out := runTool(ctx, "xrandr", "--query"); out != "" {
			if s := parseXrandr(out); len(s) > 0 {
				return s
			}
		}
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if out := runTool(ctx, "wlr-randr"); out != "" {
			if s := parseWlrRandr(out); len(s) > 0 {
				return s
			}
		}
	}
	return sysfsScreens(DefaultDRMRoot)
}

// runTool runs an optional helper binary, returning its combined output or "" if
// the binary is missing or errors.
func runTool(ctx context.Context, name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(cctx, name, args...).CombinedOutput()
	return string(out)
}

// parseXrandr parses `xrandr --query`. Connected outputs look like:
//
//	HDMI-1 connected primary 3840x2160+0+0 (normal ...) 600mm x 340mm
//	   3840x2160     60.00*+  30.00
//
// The active mode's resolution comes from the geometry on the "connected" line
// (or the current "*" mode when the output is connected-but-off); the "*" mode
// line supplies the refresh rate.
func parseXrandr(out string) []Screen {
	var screens []Screen
	idx := 0
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// An output header line: "<name> connected|disconnected ...".
		if len(fields) >= 2 && (fields[1] == "connected" || fields[1] == "disconnected") {
			if fields[1] != "connected" {
				continue
			}
			s := Screen{Index: idx, Name: fields[0]}
			if w, h, ok := parseGeometry(fields); ok {
				s.W, s.H = w, h
			}
			screens = append(screens, s)
			idx++
			continue
		}
		// An indented mode line under the most recent output.
		if raw != line && len(screens) > 0 && startsWithMode(line) {
			cur := &screens[len(screens)-1]
			if strings.Contains(line, "*") {
				if cur.W == 0 || cur.H == 0 {
					if w, h, ok := parseWxH(fields[0]); ok {
						cur.W, cur.H = w, h
					}
				}
				cur.Hz = parseStarRate(fields)
			}
		}
	}
	return screens
}

// parseGeometry finds a "WxH+X+Y" token among the output-header fields.
func parseGeometry(fields []string) (w, h int, ok bool) {
	for _, f := range fields {
		if !strings.Contains(f, "+") || !strings.Contains(f, "x") {
			continue
		}
		res := f[:strings.Index(f, "+")]
		if w, h, ok := parseWxH(res); ok {
			return w, h, ok
		}
	}
	return 0, 0, false
}

func startsWithMode(line string) bool {
	_, _, ok := parseWxH(strings.Fields(line)[0])
	return ok
}

// parseWxH parses "3840x2160" → 3840, 2160.
func parseWxH(s string) (w, h int, ok bool) {
	i := strings.IndexByte(s, 'x')
	if i <= 0 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(s[:i])
	h, err2 := strconv.Atoi(s[i+1:])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return w, h, true
}

// parseStarRate returns the refresh of the "*"-marked mode field, e.g. "60.00*+".
func parseStarRate(fields []string) float64 {
	for _, f := range fields {
		if !strings.Contains(f, "*") {
			continue
		}
		num := strings.TrimRight(f, "*+")
		if hz, err := strconv.ParseFloat(num, 64); err == nil {
			return hz
		}
	}
	return 0
}

// parseWlrRandr parses `wlr-randr`. Output looks like:
//
//	HDMI-A-1 "Samsung Electric Company ..."
//	  Modes:
//	    3840x2160 px, 60.000000 Hz (preferred, current)
func parseWlrRandr(out string) []Screen {
	var screens []Screen
	idx := 0
	for _, raw := range strings.Split(out, "\n") {
		if raw == "" {
			continue
		}
		// A non-indented line beginning an output block: `<name> "<desc>"`.
		if raw[0] != ' ' && raw[0] != '\t' {
			fields := strings.Fields(raw)
			if len(fields) >= 1 {
				screens = append(screens, Screen{Index: idx, Name: fields[0]})
				idx++
			}
			continue
		}
		line := strings.TrimSpace(raw)
		if len(screens) > 0 && strings.Contains(line, "current") && startsWithMode(line) {
			cur := &screens[len(screens)-1]
			fields := strings.Fields(line)
			if w, h, ok := parseWxH(fields[0]); ok {
				cur.W, cur.H = w, h
			}
			cur.Hz = parseWlrHz(fields)
		}
	}
	return screens
}

// parseWlrHz pulls the number before "Hz" in a wlr-randr mode line.
func parseWlrHz(fields []string) float64 {
	for i, f := range fields {
		if f == "Hz" && i > 0 {
			if hz, err := strconv.ParseFloat(fields[i-1], 64); err == nil {
				return hz
			}
		}
	}
	return 0
}

// sysfsScreens lists connected connectors from /sys/class/drm and their max
// advertised resolution (first line of the connector's "modes" file). No refresh
// rate is available here. This is the headless/DRM-console fallback.
func sysfsScreens(drmRoot string) []Screen {
	var screens []Screen
	idx := 0
	for name, status := range DRMConnectorStatuses(drmRoot) {
		if status != "connected" {
			continue
		}
		s := Screen{Index: idx, Name: name}
		// The connector dir is "cardN-<name>"; find it to read its modes file.
		if matches, _ := filepath.Glob(filepath.Join(drmRoot, "*-"+name)); len(matches) > 0 {
			if data, err := os.ReadFile(filepath.Join(matches[0], "modes")); err == nil {
				first := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
				if w, h, ok := parseWxH(first); ok {
					s.W, s.H = w, h
				}
			}
		}
		screens = append(screens, s)
		idx++
	}
	return screens
}
