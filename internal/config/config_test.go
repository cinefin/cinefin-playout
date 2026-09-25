package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// An explicit non-existent path should error.
	if _, err := Load("/no/such/config.toml"); err == nil {
		t.Fatalf("expected error for missing explicit config path")
	}

	// No path, no env, no default files present in this cwd → defaults.
	t.Setenv(EnvConfigPath, "")
	dir := t.TempDir()
	oldwd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldwd) })

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Default()
	if cfg.Host != def.Host || cfg.Port != def.Port || cfg.IPCSocket != def.IPCSocket {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
}

func TestLoadTOMLAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[server]
host = "127.0.0.1"
port = 9000
token = "sekret"

[mpv]
binary = "/usr/bin/mpv"
ipc_socket = "/tmp/other.sock"

[mpv.extra_env]
FOO = "bar"

[agent]
state_dir = "/var/lib/cinefin-playout"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Via env var.
	t.Setenv(EnvConfigPath, path)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 9000 || cfg.Token != "sekret" {
		t.Fatalf("server section not applied: %+v", cfg)
	}
	if cfg.IPCSocket != "/tmp/other.sock" {
		t.Fatalf("ipc_socket = %q", cfg.IPCSocket)
	}
	if cfg.MPVBinary != "/usr/bin/mpv" {
		t.Fatalf("mpv binary = %q", cfg.MPVBinary)
	}
	if cfg.ExtraEnv["FOO"] != "bar" {
		t.Fatalf("extra_env = %v", cfg.ExtraEnv)
	}
	if cfg.StateDir != "/var/lib/cinefin-playout" {
		t.Fatalf("state_dir = %q", cfg.StateDir)
	}

	// Explicit --config path takes precedence and also works.
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("Load explicit: %v", err)
	}
	if cfg2.Token != "sekret" {
		t.Fatalf("explicit load token = %q", cfg2.Token)
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

func TestEnsureToken(t *testing.T) {
	// A configured token is left untouched and nothing is written.
	cfg := Config{Token: "explicit", StateDir: t.TempDir()}
	gen, err := EnsureToken(&cfg)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if gen || cfg.Token != "explicit" {
		t.Fatalf("configured token should be kept as-is, got gen=%v token=%q", gen, cfg.Token)
	}
	if _, err := os.Stat(TokenPath(cfg.StateDir)); !os.IsNotExist(err) {
		t.Fatalf("no token file should be written when a token is configured")
	}

	// An empty token generates, persists and returns generated=true.
	dir := t.TempDir()
	c1 := Config{StateDir: dir}
	gen, err = EnsureToken(&c1)
	if err != nil {
		t.Fatalf("EnsureToken generate: %v", err)
	}
	if !gen || c1.Token == "" {
		t.Fatalf("expected a generated token, got gen=%v token=%q", gen, c1.Token)
	}
	data, err := os.ReadFile(TokenPath(dir))
	if err != nil {
		t.Fatalf("token file not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != c1.Token {
		t.Fatalf("persisted token %q != returned %q", strings.TrimSpace(string(data)), c1.Token)
	}

	// A second run in the same state dir reuses the persisted token (gen=false).
	c2 := Config{StateDir: dir}
	gen, err = EnsureToken(&c2)
	if err != nil {
		t.Fatalf("EnsureToken reuse: %v", err)
	}
	if gen || c2.Token != c1.Token {
		t.Fatalf("expected persisted token reuse, got gen=%v token=%q (want %q)", gen, c2.Token, c1.Token)
	}
}
