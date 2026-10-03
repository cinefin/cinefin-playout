package hostconfig

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// argsContain reports whether args contains exactly want (as a "--flag=value"
// or standalone flag).
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func argWithPrefix(args []string, prefix string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return a, true
		}
	}
	return "", false
}

func TestBuildMPVArgsEnforcedFlags(t *testing.T) {
	hc := Preset("linux")
	args := BuildMPVArgs(hc, "/tmp/scratch.sock", MPVConfig{})
	for _, must := range []string{
		"--input-ipc-server=/tmp/scratch.sock",
		"--idle=yes",
		"--force-window=yes",
	} {
		if !hasArg(args, must) {
			t.Errorf("missing enforced flag %q in %v", must, args)
		}
	}
}

func TestBuildMPVArgsOSC(t *testing.T) {
	hc := Preset("linux")
	hc.Graphics.OSC = true
	if !hasArg(BuildMPVArgs(hc, "/tmp/s.sock", MPVConfig{}), "--osc=yes") {
		t.Errorf("OSC-on preset should emit --osc=yes")
	}
	hc.Graphics.OSC = false
	args := BuildMPVArgs(hc, "/tmp/s.sock", MPVConfig{})
	if !hasArg(args, "--osc=no") {
		t.Errorf("OSC-off should emit --osc=no, got %v", args)
	}
	if hasArg(args, "--osc=yes") {
		t.Errorf("OSC-off should not emit --osc=yes")
	}
}

func TestBuildMPVArgsDesktopPreset(t *testing.T) {
	hc := Preset("linux")
	hc.Audio.Device = "alsa/hdmi:CARD=NVidia,DEV=0"
	hc.Graphics.Screen = 1
	args := BuildMPVArgs(hc, "/tmp/s.sock", MPVConfig{})

	want := map[string]string{
		"--vo=":             "--vo=gpu-next",
		"--hwdec=":          "--hwdec=auto",
		"--screen=":         "--screen=1",
		"--audio-device=":   "--audio-device=alsa/hdmi:CARD=NVidia,DEV=0",
		"--audio-channels=": "--audio-channels=auto",
		"--volume-max=":     "--volume-max=130",
	}
	for prefix, expect := range want {
		got, ok := argWithPrefix(args, prefix)
		if !ok || got != expect {
			t.Errorf("arg %q = %q (found=%v), want %q", prefix, got, ok, expect)
		}
	}
	if !hasArg(args, "--fullscreen") {
		t.Error("desktop preset should be fullscreen")
	}
	if !hasArg(args, "--target-colorspace-hint=yes") {
		t.Error("hdr_passthrough should emit --target-colorspace-hint=yes")
	}
	// Desktop mode must NOT emit drm-connector.
	if _, ok := argWithPrefix(args, "--drm-connector="); ok {
		t.Error("desktop mode should not emit --drm-connector")
	}
}

func TestBuildMPVArgsDRMPreset(t *testing.T) {
	hc := PresetDRM("HDMI-A-1")
	hc.Graphics.DRMMode = "1920x1080@60"
	args := BuildMPVArgs(hc, "/tmp/s.sock", MPVConfig{})

	if got, _ := argWithPrefix(args, "--gpu-api="); got != "--gpu-api=vulkan" {
		t.Errorf("drm gpu-api = %q, want vulkan", got)
	}
	if got, _ := argWithPrefix(args, "--gpu-context="); got != "--gpu-context=displayvk" {
		t.Errorf("drm gpu-context = %q, want displayvk", got)
	}
	// Connector and mode are separate options, never "connector@mode".
	if got, _ := argWithPrefix(args, "--drm-connector="); got != "--drm-connector=HDMI-A-1" {
		t.Errorf("drm-connector = %q, want --drm-connector=HDMI-A-1", got)
	}
	if got, _ := argWithPrefix(args, "--drm-mode="); got != "--drm-mode=1920x1080@60" {
		t.Errorf("drm-mode = %q, want --drm-mode=1920x1080@60", got)
	}
	// DRM mode must NOT emit --screen.
	if _, ok := argWithPrefix(args, "--screen="); ok {
		t.Error("drm mode should not emit --screen")
	}
}

func TestBuildMPVArgsWindowsPreset(t *testing.T) {
	hc := Preset("windows")
	args := BuildMPVArgs(hc, `\\.\pipe\mpv-x`, MPVConfig{})

	if got, _ := argWithPrefix(args, "--gpu-api="); got != "--gpu-api=d3d11" {
		t.Errorf("windows gpu-api = %q, want d3d11", got)
	}
	if got, _ := argWithPrefix(args, "--audio-device="); got != "--audio-device=wasapi" {
		t.Errorf("windows audio-device = %q, want wasapi", got)
	}
	if hc.Graphics.Mode != ModeDesktop {
		t.Error("windows preset must be desktop mode")
	}
}

func TestBuildMPVArgsSPDIF(t *testing.T) {
	hc := Preset("linux")
	hc.Audio.SPDIFPassthrough = []string{"ac3", "eac3", "dts"}
	args := BuildMPVArgs(hc, "/tmp/s.sock", MPVConfig{})

	if got, _ := argWithPrefix(args, "--audio-spdif="); got != "--audio-spdif=ac3,eac3,dts" {
		t.Errorf("audio-spdif = %q", got)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*HostConfig)
		wantErr bool
	}{
		{"valid desktop", func(hc *HostConfig) {}, false},
		{"bad mode", func(hc *HostConfig) { hc.Graphics.Mode = "wibble" }, true},
		// No connector is allowed: mpv then uses the first connected screen.
		{"drm without connector", func(hc *HostConfig) {
			hc.Graphics.Mode = ModeDRM
			hc.Graphics.DRMConnector = ""
		}, runtime.GOOS == "windows"},
		{"vulkan on the drm context", func(hc *HostConfig) {
			hc.Graphics.Mode = ModeDRM
			hc.Graphics.GPUAPI = "vulkan"
			hc.Graphics.GPUContext = "drm"
		}, true},
		{"negative max_volume", func(hc *HostConfig) { hc.Audio.MaxVolume = -5 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hc := Preset("linux")
			tc.mutate(&hc)
			err := hc.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// The pairing box needs a clean full screen with no on-screen controller, and
// it must start even if the stored config would not autostart.
func TestPairingLaunch(t *testing.T) {
	hc := Pairing()
	if !hc.Autostart || !hc.Graphics.Fullscreen || hc.Graphics.OSC {
		t.Errorf("pairing launch config = %+v", hc)
	}
	if hc.Graphics.Display != "" {
		t.Errorf("pairing launch must inherit DISPLAY, got %q", hc.Graphics.Display)
	}
}

func TestPickScreen(t *testing.T) {
	desktop := Preset("linux")
	if err := desktop.PickScreen("2", nil); err != nil || desktop.Graphics.Screen != 2 {
		t.Errorf("index: screen=%d err=%v", desktop.Graphics.Screen, err)
	}
	if err := desktop.PickScreen("HDMI-A-1", nil); err != nil || desktop.Graphics.ScreenName != "HDMI-A-1" {
		t.Fatalf("name: %+v err=%v", desktop.Graphics, err)
	}
	args := BuildMPVArgs(desktop, "/tmp/s", MPVConfig{})
	if !hasArg(args, "--screen-name=HDMI-A-1") || !hasArg(args, "--fs-screen-name=HDMI-A-1") {
		t.Errorf("screen name not in args: %v", args)
	}

	drm := PresetDRM("")
	if err := drm.PickScreen("1", []string{"DP-1", "HDMI-A-1"}); err != nil || drm.Graphics.DRMConnector != "HDMI-A-1" {
		t.Errorf("drm index: %q err=%v", drm.Graphics.DRMConnector, err)
	}
	if err := drm.PickScreen("DP-2", nil); err != nil || drm.Graphics.DRMConnector != "DP-2" {
		t.Errorf("drm name: %q err=%v", drm.Graphics.DRMConnector, err)
	}
	if err := drm.PickScreen("5", []string{"DP-1"}); err == nil {
		t.Error("drm index past the connected screens should fail")
	}
}

// The on-screen controller is for someone at the screen with a mouse; a cinema
// screen starts clean, so every preset leaves it off.
func TestPresetsLeaveOSCOff(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		if Preset(goos).Graphics.OSC {
			t.Errorf("Preset(%q) turns the OSC on", goos)
		}
	}
	if Detect().Graphics.OSC {
		t.Error("Detect turns the OSC on")
	}
}

func TestValidateDRMMode(t *testing.T) {
	for _, ok := range []string{
		"preferred", "highest", "0", "12",
		"3840x2160", "1920x1080@60", "1920x1080@59.94",
	} {
		if err := ValidateDRMMode(ok); err != nil {
			t.Errorf("ValidateDRMMode(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "-1", "best", "1920x", "x1080", "0x1080", "1920x0",
		"1920X1080", "1920x1080@", "1920x1080@abc", "1920x1080@0",
		"1920x1080@-60", "1920x1080@6e1", "1920x1080@inf", "1920x1080@60@60",
		" 1920x1080", "HDMI-A-1",
	} {
		if err := ValidateDRMMode(bad); err == nil {
			t.Errorf("ValidateDRMMode(%q) = nil, want an error", bad)
		}
	}
}

func TestPickMode(t *testing.T) {
	drm := PresetDRM("HDMI-A-1")
	if err := drm.PickMode("3840x2160@23.976"); err != nil || drm.Graphics.DRMMode != "3840x2160@23.976" {
		t.Fatalf("drm: mode=%q err=%v", drm.Graphics.DRMMode, err)
	}
	if !hasArg(BuildMPVArgs(drm, "/tmp/s", MPVConfig{}), "--drm-mode=3840x2160@23.976") {
		t.Error("the picked mode is not in the args")
	}

	desktop := Preset("linux")
	if err := desktop.PickMode("1920x1080"); !errors.Is(err, ErrModeIgnored) {
		t.Errorf("desktop: err=%v, want ErrModeIgnored", err)
	}
	if desktop.Graphics.DRMMode != "" {
		t.Errorf("desktop: DRMMode set to %q", desktop.Graphics.DRMMode)
	}
}

func TestBuildMPVArgsMPVConfig(t *testing.T) {
	hc := Preset("linux")
	args := BuildMPVArgs(hc, "/tmp/s", MPVConfig{Dir: "/state/mpv", Include: "/etc/cinefin-playout/mpv.conf"})
	// The config goes first: an --include is read where it stands, so every
	// option after it (the enforced ones, the launch config) wins.
	want := []string{
		"--config-dir=/state/mpv",
		"--load-scripts=no",
		"--include=/etc/cinefin-playout/mpv.conf",
		"--input-ipc-server=/tmp/s",
	}
	if !reflect.DeepEqual(args[:len(want)], want) {
		t.Errorf("args start %v, want %v", args[:len(want)], want)
	}

	// No folder or file: mpv's own default folder, still no user scripts.
	args = BuildMPVArgs(hc, "/tmp/s", MPVConfig{})
	if args[0] != "--load-scripts=no" {
		t.Errorf("args start %q, want --load-scripts=no", args[0])
	}
	if _, ok := argWithPrefix(args, "--config-dir"); ok {
		t.Error("--config-dir without a folder")
	}
	if _, ok := argWithPrefix(args, "--include"); ok {
		t.Error("--include without a file")
	}
}
