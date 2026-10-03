package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Listen != "0.0.0.0" || c.Port != 8089 || c.StateDir == "" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(c.IPCSocket, `\\.\pipe\`) {
		t.Errorf("windows IPC endpoint must be a named pipe, got %q", c.IPCSocket)
	}
}

func TestResolveMPVBinary(t *testing.T) {
	// An explicitly configured binary is honoured verbatim.
	if got := (Config{MPVBinary: "/opt/mpv/mpv"}).ResolveMPVBinary(); got != "/opt/mpv/mpv" {
		t.Errorf("explicit binary = %q, want /opt/mpv/mpv", got)
	}
	// With no configured binary and no sibling next to the test executable, it
	// falls back to the bare name resolved on PATH.
	if got := (Config{}).ResolveMPVBinary(); got != "mpv" {
		t.Errorf("default resolve = %q, want mpv", got)
	}
}

func TestMPVConfigDir(t *testing.T) {
	dir := t.TempDir()
	c := Config{StateDir: dir}
	if got, want := c.MPVConfigDir(), filepath.Join(dir, "mpv"); got != want {
		t.Errorf("MPVConfigDir = %q, want %q", got, want)
	}
	if c.HasMPVConf() {
		t.Error("HasMPVConf with no folder = true")
	}
	if err := os.MkdirAll(c.MPVConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.MPVConfPath(), []byte("volume=37\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !c.HasMPVConf() {
		t.Error("HasMPVConf with an mpv.conf = false")
	}
}

func TestLegacyConfig(t *testing.T) {
	paths := LegacyConfigPaths()
	if len(paths) != 3 || paths[0] != "config.toml" || paths[2] != "/etc/cinefin-playout/config.toml" {
		t.Errorf("LegacyConfigPaths = %v", paths)
	}

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.toml")
	found := filepath.Join(dir, "config.toml")
	if got := FindLegacyConfig([]string{missing, found}); got != "" {
		t.Errorf("no files: FindLegacyConfig = %q, want \"\"", got)
	}
	// A directory of that name is not a config file.
	if err := os.Mkdir(found, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindLegacyConfig([]string{found}); got != "" {
		t.Errorf("a directory: FindLegacyConfig = %q, want \"\"", got)
	}
	file := filepath.Join(dir, "etc-config.toml")
	if err := os.WriteFile(file, []byte("[mpv]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindLegacyConfig([]string{missing, found, file}); got != file {
		t.Errorf("FindLegacyConfig = %q, want %q", got, file)
	}
}
