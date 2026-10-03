package config

import (
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
