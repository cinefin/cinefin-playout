package hostconfig

import (
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
	args := BuildMPVArgs(hc, "/tmp/scratch.sock")
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
	hc := Preset("linux") // preset defaults OSC on
	if !hasArg(BuildMPVArgs(hc, "/tmp/s.sock"), "--osc=yes") {
		t.Errorf("OSC-on preset should emit --osc=yes")
	}
	hc.Graphics.OSC = false
	args := BuildMPVArgs(hc, "/tmp/s.sock")
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
	args := BuildMPVArgs(hc, "/tmp/s.sock")

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
	args := BuildMPVArgs(hc, "/tmp/s.sock")

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
	args := BuildMPVArgs(hc, `\\.\pipe\mpv-x`)

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

func TestBuildMPVArgsSPDIFAndIdle(t *testing.T) {
	hc := Preset("linux")
	hc.Audio.SPDIFPassthrough = []string{"ac3", "eac3", "dts"}
	hc.Graphics.IdleMedia = "/media/ident.mp4"
	args := BuildMPVArgs(hc, "/tmp/s.sock")

	if got, _ := argWithPrefix(args, "--audio-spdif="); got != "--audio-spdif=ac3,eac3,dts" {
		t.Errorf("audio-spdif = %q", got)
	}
	// idle media appended with --pause, and as the last positional.
	if args[len(args)-1] != "/media/ident.mp4" {
		t.Errorf("idle media should be last arg, got %q", args[len(args)-1])
	}
	if !hasArg(args, "--pause") {
		t.Error("idle media should be preceded by --pause")
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
		{"drm without connector", func(hc *HostConfig) {
			hc.Graphics.Mode = ModeDRM
			hc.Graphics.DRMConnector = ""
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
