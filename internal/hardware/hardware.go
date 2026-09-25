// Package hardware enumerates the playout host's real devices on demand.
//
// There is no caching or background polling: each call re-runs the underlying
// probes (a Cinefin "rescan hardware" is simply another GET /hardware). Audio
// devices and mpv features come from the mpv binary itself (`--audio-device=help`,
// `--vo=help`, `--gpu-api=help`); DRM connectors come from /sys/class/drm
// (Linux only); screens/monitors come from the live session (xrandr / wlr-randr
// / sysfs on Linux, the GDI display APIs on Windows — see screens_*.go). A
// missing mpv binary or absent tool degrades to empty lists plus a Note — never
// an error/500.
package hardware

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// AudioDevice is one mpv audio sink.
type AudioDevice struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Screen is a best-effort display descriptor. Name is the OS-level output name
// (e.g. "HDMI-1" on X11, "\\.\DISPLAY1" on Windows) when known. Index is the mpv
// --screen ordinal.
type Screen struct {
	Index int     `json:"index"`
	Name  string  `json:"name,omitempty"`
	W     int     `json:"w"`
	H     int     `json:"h"`
	Hz    float64 `json:"hz"`
}

// MPVInfo carries the mpv version and the features we surface as dropdowns.
type MPVInfo struct {
	Version string   `json:"version"`
	VO      []string `json:"vo"`
	GPUAPIs []string `json:"gpu_apis"`
}

// Report is the /hardware payload.
type Report struct {
	AudioDevices  []AudioDevice `json:"audio_devices"`
	DRMConnectors []string      `json:"drm_connectors"`
	Screens       []Screen      `json:"screens"`
	MPV           MPVInfo       `json:"mpv"`
	Note          string        `json:"note,omitempty"`
}

// Enumerate probes the host. mpvBinary is the mpv executable (e.g. "mpv");
// drmRoot is the sysfs DRM directory (usually "/sys/class/drm", overridable in
// tests). It never returns an error: any failure is reported via Report.Note.
func Enumerate(ctx context.Context, mpvBinary, drmRoot string) Report {
	rep := Report{
		AudioDevices:  []AudioDevice{},
		DRMConnectors: []string{},
		Screens:       []Screen{},
		MPV:           MPVInfo{VO: []string{}, GPUAPIs: []string{}},
	}

	if _, err := exec.LookPath(mpvBinary); err != nil {
		rep.Note = "mpv binary not found (" + mpvBinary + "): audio devices and mpv features unavailable"
	} else {
		rep.AudioDevices = ParseAudioDevices(runHelp(ctx, mpvBinary, "--audio-device=help"))
		rep.MPV.Version = mpvVersion(ctx, mpvBinary)
		rep.MPV.VO = ParseVOList(runHelp(ctx, mpvBinary, "--vo=help"))
		rep.MPV.GPUAPIs = ParseGPUAPIs(runHelp(ctx, mpvBinary, "--gpu-api=help"))
	}

	rep.DRMConnectors = drmConnectors(drmRoot)
	// enumScreens may return nil (no display/DRM, e.g. a headless CI box); keep
	// the initialised empty slice so the JSON stays [] rather than null.
	if s := enumScreens(ctx); s != nil {
		rep.Screens = s
	}

	return rep
}

// runHelp runs `mpv <flag>` and returns combined stdout+stderr, empty on error.
func runHelp(ctx context.Context, mpvBinary, flag string) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, mpvBinary, flag, "--no-config")
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func mpvVersion(ctx context.Context, mpvBinary string) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, mpvBinary, "--version", "--no-config").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return ParseVersion(string(out))
}

// ParseVersion pulls "0.41" from an mpv --version banner line like
// "mpv v0.41.0 Copyright ...".
func ParseVersion(out string) string {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "mpv ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ""
		}
		ver := strings.TrimPrefix(fields[1], "v")
		// keep major.minor
		parts := strings.SplitN(ver, ".", 3)
		if len(parts) >= 2 {
			return parts[0] + "." + parts[1]
		}
		return ver
	}
	return ""
}

// ParseAudioDevices parses `mpv --audio-device=help` output. Lines look like:
//
//	'alsa/hdmi:CARD=NVidia,DEV=0' (HDA NVidia, HDMI Audio Output)
func ParseAudioDevices(out string) []AudioDevice {
	devices := []AudioDevice{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "'") {
			continue
		}
		end := strings.Index(line[1:], "'")
		if end < 0 {
			continue
		}
		name := line[1 : 1+end]
		desc := ""
		rest := strings.TrimSpace(line[1+end+1:])
		if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
			desc = rest[1 : len(rest)-1]
		}
		devices = append(devices, AudioDevice{Name: name, Description: desc})
	}
	return devices
}

// ParseVOList parses `mpv --vo=help`. Lines look like "  gpu-next  Video output…";
// the first whitespace-delimited token per indented line is the vo name. The
// "Available video outputs:" header and blank lines are skipped.
func ParseVOList(out string) []string {
	return parseFirstTokens(out, "Available video outputs")
}

// ParseGPUAPIs parses `mpv --gpu-api=help`. The relevant section is the plain
// "Available GPU APIs:" list (not the "APIs and contexts" table). Duplicates
// are removed while preserving order.
func ParseGPUAPIs(out string) []string {
	apis := []string{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	inSection := false
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Available GPU APIs and contexts") {
			break // the pairs table; stop before it
		}
		if strings.HasPrefix(line, "Available GPU APIs") {
			inSection = true
			continue
		}
		if !inSection || line == "" {
			continue
		}
		name := strings.Fields(line)[0]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		apis = append(apis, name)
	}
	return apis
}

func parseFirstTokens(out, header string) []string {
	items := []string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	started := false
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, header) {
			started = true
			continue
		}
		if !started || line == "" {
			continue
		}
		// Section list lines are indented; a non-indented line ends the list.
		if raw == line {
			break
		}
		items = append(items, strings.Fields(line)[0])
	}
	return items
}
