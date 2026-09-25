package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadGraphicsAudio(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[mpv]
autostart = false

[mpv.graphics]
mode = "drm"
drm_connector = "HDMI-A-1"
gpu_context = "displayvk"

[mpv.audio]
device = "hdmi"
spdif_passthrough = ["ac3", "eac3"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
	if cfg.Autostart {
		t.Error("autostart should be false")
	}
	if cfg.Graphics.Mode != "drm" || cfg.Graphics.DRMConnector != "HDMI-A-1" || cfg.Graphics.GPUContext != "displayvk" {
		t.Errorf("graphics not applied: %+v", cfg.Graphics)
	}
	// Omitted graphics keys keep the preset default.
	if cfg.Graphics.VO != Default().Graphics.VO {
		t.Errorf("omitted vo should keep default %q, got %q", Default().Graphics.VO, cfg.Graphics.VO)
	}
	if cfg.Audio.Device != "hdmi" || !reflect.DeepEqual(cfg.Audio.SPDIFPassthrough, []string{"ac3", "eac3"}) {
		t.Errorf("audio not applied: %+v", cfg.Audio)
	}
	// Omitted audio key keeps the preset default.
	if cfg.Audio.MaxVolume != Default().Audio.MaxVolume {
		t.Errorf("omitted max_volume should keep default %d, got %d", Default().Audio.MaxVolume, cfg.Audio.MaxVolume)
	}
}

func TestWriteLaunchConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := Default()
	c.Autostart = false
	c.Graphics.Mode = "drm"
	c.Graphics.DRMConnector = "DP-1"
	c.Graphics.IdleMedia = "/x/ident.png"
	c.Audio.SPDIFPassthrough = []string{"dts", "truehd"}
	c.Audio.MaxVolume = 100

	if err := WriteLaunchConfig(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Graphics, c.Graphics) {
		t.Errorf("graphics round-trip:\n want %+v\n got  %+v", c.Graphics, got.Graphics)
	}
	if !reflect.DeepEqual(got.Audio, c.Audio) {
		t.Errorf("audio round-trip:\n want %+v\n got  %+v", c.Audio, got.Audio)
	}
	if got.Autostart != c.Autostart {
		t.Errorf("autostart round-trip = %v, want %v", got.Autostart, c.Autostart)
	}
}

func TestWriteLaunchConfigPreservesOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `[server]
host = "127.0.0.1"
port = 9000

[mpv]
binary = "/usr/bin/mpv"
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Graphics.VO = "gpu"
	if err := WriteLaunchConfig(path, c); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	text := string(data)
	// autostart landed inside a single [mpv], not as a duplicate table.
	if strings.Count(text, "[mpv]\n") != 1 || !strings.Contains(text, "autostart = ") {
		t.Errorf("autostart not set within a single [mpv]:\n%s", text)
	}
	// The token was absent from the file and must stay absent (omitempty), so a
	// rewrite never leaks a generated token into config.toml.
	if strings.Contains(text, "token") {
		t.Errorf("rewrite introduced a token key:\n%s", text)
	}

	// Values in the other sections are preserved by value across the rewrite.
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "127.0.0.1" || got.Port != 9000 || got.MPVBinary != "/usr/bin/mpv" || got.Graphics.VO != "gpu" {
		t.Errorf("reload lost values: %+v", got)
	}
}

func TestWriteLaunchConfigReplacesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `[mpv.graphics]
vo = "old"
mode = "desktop"

[mpv.audio]
device = "old"
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Graphics.VO = "new"
	c.Audio.Device = "new"
	if err := WriteLaunchConfig(path, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if strings.Count(text, "[mpv.graphics]") != 1 || strings.Count(text, "[mpv.audio]") != 1 {
		t.Errorf("tables duplicated:\n%s", text)
	}
	if strings.Contains(text, `vo = "old"`) || strings.Contains(text, `device = "old"`) {
		t.Errorf("stale values not replaced:\n%s", text)
	}
	got, _ := Load(path)
	if got.Graphics.VO != "new" || got.Audio.Device != "new" {
		t.Errorf("reload = %+v", got)
	}
}
