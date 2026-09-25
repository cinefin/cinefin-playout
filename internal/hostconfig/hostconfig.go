// Package hostconfig defines the playout host's graphics + audio launch config
// and turns it into an mpv command line.
//
// The values themselves are loaded from and saved to config.toml by the config
// package (the [graphics]/[audio] sections); this package no longer does any
// file I/O. It owns the launch-config *types*, the shipped per-OS presets, the
// validation rules, and BuildMPVArgs — the single place that decides the actual
// mpv command line (the responsibility that used to live in Cinefin's pushed
// MPVLaunchConfig / build_mpv_args; see docs/ARCHITECTURE.md, "Config
// ownership").
package hostconfig

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Graphics mode values.
const (
	ModeDesktop = "desktop"
	ModeDRM     = "drm"
)

// Graphics is the host's video-output configuration.
type Graphics struct {
	Mode           string `json:"mode"`            // "desktop" | "drm"
	VO             string `json:"vo"`              // e.g. "gpu-next"
	GPUAPI         string `json:"gpu_api"`         // "" = mpv default
	GPUContext     string `json:"gpu_context"`     // e.g. "displayvk" / "drm"
	HWDec          string `json:"hwdec"`           // e.g. "auto"
	Screen         int    `json:"screen"`          // desktop mode
	DRMConnector   string `json:"drm_connector"`   // drm mode, e.g. "HDMI-A-1"
	DRMMode        string `json:"drm_mode"`        // optional pinned mode
	Fullscreen     bool   `json:"fullscreen"`      //
	HDRPassthrough bool   `json:"hdr_passthrough"` //
	OSC            bool   `json:"osc"`             // mpv on-screen controller (seek bar / controls on mouse-over)
	Display        string `json:"display"`         // X display (desktop); "" for drm
	IdleMedia      string `json:"idle_media"`      // ident/boot image shown paused before Cinefin connects
}

// Audio is the host's audio-output configuration.
type Audio struct {
	Device           string   `json:"device"`
	Channels         string   `json:"channels"`          // e.g. "auto"
	SPDIFPassthrough []string `json:"spdif_passthrough"` // codecs, e.g. ["ac3","eac3",...]
	MaxVolume        int      `json:"max_volume"`        // volume-max
}

// HostConfig is the full host-owned config.
type HostConfig struct {
	// Autostart launches mpv from this config when the agent boots. It is a
	// host launch behaviour (not a graphics/audio knob), so it lives here
	// rather than in the agent's transport config — editable through the
	// Cinefin Playout page like the rest of the launch config.
	Autostart bool     `json:"autostart"`
	Graphics  Graphics `json:"graphics"`
	Audio     Audio    `json:"audio"`
}

// Default returns a HostConfig with sensible per-OS defaults (a preset for the
// current GOOS). Callers wanting a specific platform's preset use Preset.
func Default() HostConfig {
	return Preset(runtime.GOOS)
}

// Preset returns the shipped default HostConfig for the named OS. Unknown OS
// falls back to a conservative desktop gpu-next config.
func Preset(goos string) HostConfig {
	switch goos {
	case "windows":
		// Windows / NVIDIA: gpu-next + d3d11, wasapi audio, desktop only.
		return HostConfig{
			Autostart: true,
			Graphics: Graphics{
				Mode:           ModeDesktop,
				VO:             "gpu-next",
				GPUAPI:         "d3d11",
				GPUContext:     "",
				HWDec:          "auto",
				Screen:         0,
				Fullscreen:     true,
				HDRPassthrough: true,
				OSC:            true,
				Display:        "",
			},
			Audio: Audio{
				Device:           "wasapi",
				Channels:         "auto",
				SPDIFPassthrough: []string{},
				MaxVolume:        130,
			},
		}
	default:
		// Linux / NVIDIA desktop: gpu-next (X/Wayland session present).
		return HostConfig{
			Autostart: true,
			Graphics: Graphics{
				Mode:           ModeDesktop,
				VO:             "gpu-next",
				GPUAPI:         "",
				GPUContext:     "",
				HWDec:          "auto",
				Screen:         0,
				Fullscreen:     true,
				HDRPassthrough: true,
				OSC:            true,
				Display:        ":0",
			},
			Audio: Audio{
				Device:           "",
				Channels:         "auto",
				SPDIFPassthrough: []string{},
				MaxVolume:        130,
			},
		}
	}
}

// PresetDRM returns the Linux Direct/DRM preset for the headless booth box:
// Vulkan displayvk on a chosen connector. Linux only (Windows is desktop-only).
func PresetDRM(connector string) HostConfig {
	hc := Preset("linux")
	hc.Graphics.Mode = ModeDRM
	hc.Graphics.GPUAPI = "vulkan"
	hc.Graphics.GPUContext = "displayvk"
	hc.Graphics.DRMConnector = connector
	hc.Graphics.Display = ""
	return hc
}

// Validate checks the config for internal consistency, returning a message on
// the first problem. Used by PUT /hostconfig before persisting.
func (hc HostConfig) Validate() error {
	switch hc.Graphics.Mode {
	case ModeDesktop, ModeDRM:
	default:
		return fmt.Errorf("graphics.mode must be %q or %q, got %q", ModeDesktop, ModeDRM, hc.Graphics.Mode)
	}
	if hc.Graphics.Mode == ModeDRM && runtime.GOOS == "windows" {
		return fmt.Errorf("graphics.mode=drm is not supported on windows (desktop only)")
	}
	if hc.Graphics.Mode == ModeDRM && strings.TrimSpace(hc.Graphics.DRMConnector) == "" {
		return fmt.Errorf("graphics.drm_connector is required in drm mode")
	}
	// gpu_context "drm" is an OpenGL/EGL context; it cannot serve the Vulkan API.
	// Vulkan on a DRM/KMS console wants gpu_context "displayvk" (or left blank to
	// let mpv pick). This mismatch otherwise fails at init with a blank screen.
	if hc.Graphics.GPUAPI == "vulkan" && hc.Graphics.GPUContext == "drm" {
		return fmt.Errorf("graphics.gpu_context=drm cannot serve gpu_api=vulkan; use gpu_context=displayvk (or leave it blank)")
	}
	if hc.Audio.MaxVolume < 0 {
		return fmt.Errorf("audio.max_volume must be >= 0, got %d", hc.Audio.MaxVolume)
	}
	return nil
}

// BuildMPVArgs turns the host config into an mpv command line (excluding the
// binary itself — the caller prepends that). The IPC socket, idle and
// force-window flags are always enforced: they are what Cinefin relies on to
// control playback, so config can't disable them.
func BuildMPVArgs(hc HostConfig, ipcSocket string) []string {
	g := hc.Graphics
	a := hc.Audio

	args := []string{
		"--input-ipc-server=" + ipcSocket,
		"--idle=yes",
		"--force-window=yes",
	}

	if g.Fullscreen {
		args = append(args, "--fullscreen")
	}
	if g.VO != "" {
		args = append(args, "--vo="+g.VO)
	}
	if g.GPUAPI != "" {
		args = append(args, "--gpu-api="+g.GPUAPI)
	}
	if g.GPUContext != "" {
		args = append(args, "--gpu-context="+g.GPUContext)
	}
	if g.HWDec != "" {
		args = append(args, "--hwdec="+g.HWDec)
	}

	if g.Mode == ModeDRM {
		// mpv takes the connector and the mode as separate options — the mode is
		// NOT an "@mode" suffix on the connector.
		if g.DRMConnector != "" {
			args = append(args, "--drm-connector="+g.DRMConnector)
		}
		if g.DRMMode != "" {
			args = append(args, "--drm-mode="+g.DRMMode)
		}
	} else {
		// Desktop mode: --screen applies.
		args = append(args, "--screen="+strconv.Itoa(g.Screen))
	}

	// HDR passthrough: tell mpv to target the display's native colorspace so an
	// HDR source is sent through as HDR rather than tone-mapped to SDR.
	if g.HDRPassthrough {
		args = append(args, "--target-colorspace-hint=yes")
	}

	// On-screen controller: mpv's built-in seek bar / controls on mouse-over,
	// for a local operator. Off gives a clean cinema screen with no chrome.
	if g.OSC {
		args = append(args, "--osc=yes", "--input-default-bindings=yes", "--cursor-autohide=1000")
	} else {
		args = append(args, "--osc=no")
	}

	// Audio.
	if a.Device != "" {
		args = append(args, "--audio-device="+a.Device)
	}
	if a.Channels != "" {
		args = append(args, "--audio-channels="+a.Channels)
	}
	if len(a.SPDIFPassthrough) > 0 {
		args = append(args, "--audio-spdif="+strings.Join(a.SPDIFPassthrough, ","))
	}
	if a.MaxVolume > 0 {
		args = append(args, "--volume-max="+strconv.Itoa(a.MaxVolume))
	}

	// Idle/ident media, shown paused before Cinefin takes over. Always last so
	// it is the initial playlist entry.
	if g.IdleMedia != "" {
		args = append(args, "--pause", g.IdleMedia)
	}

	return args
}
