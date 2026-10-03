//go:build !windows && !darwin

package session

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// listen creates a unix socket at path, owned by the test's user.
func listen(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
}

// A shell inside tmux (or ssh) has no display variables; the sockets the
// session left behind are found and exported instead.
func TestAdoptSockets(t *testing.T) {
	run, x11 := t.TempDir(), t.TempDir()
	listen(t, filepath.Join(run, "wayland-1"))
	if err := os.WriteFile(filepath.Join(run, "wayland-1.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	listen(t, filepath.Join(x11, "X0"))

	oldRun, oldX := runtimeDir, x11Dir
	runtimeDir, x11Dir = func() string { return run }, x11
	t.Cleanup(func() { runtimeDir, x11Dir = oldRun, oldX })
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

	if !adoptSockets() {
		t.Fatal("adoptSockets found no display")
	}
	if got := os.Getenv("WAYLAND_DISPLAY"); got != "wayland-1" {
		t.Errorf("WAYLAND_DISPLAY = %q", got)
	}
	if got := os.Getenv("DISPLAY"); got != ":0" {
		t.Errorf("DISPLAY = %q", got)
	}
	if got := os.Getenv("XDG_RUNTIME_DIR"); got != run {
		t.Errorf("XDG_RUNTIME_DIR = %q, want %q", got, run)
	}
}

// No sockets (a headless box): nothing is exported.
func TestAdoptSocketsHeadless(t *testing.T) {
	oldRun, oldX := runtimeDir, x11Dir
	empty := t.TempDir()
	runtimeDir, x11Dir = func() string { return empty }, empty
	t.Cleanup(func() { runtimeDir, x11Dir = oldRun, oldX })
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")

	if adoptSockets() || os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != "" {
		t.Error("headless box reported a display")
	}
}
