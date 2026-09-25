//go:build !windows

package mpvproc

import (
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

func hasMPV(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv binary not available")
	}
}

// shortSocketPath returns a socket path that fits the ~104-byte sun_path limit
// on macOS/BSD — t.TempDir() embeds the test name, which can push it over.
func shortSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cp3-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// newManager builds a Manager pointed at a scratch socket in a temp dir. The
// host config is desktop mode with no video so mpv stays headless-friendly.
func newManager(t *testing.T, socket string) *Manager {
	t.Helper()
	cfg := config.Default()
	cfg.IPCSocket = socket
	cfg.StateDir = t.TempDir()
	// A minimal, side-effect-free launch: null vo, no fullscreen, no audio.
	hcOf := func() hostconfig.HostConfig {
		hc := hostconfig.Preset("linux")
		hc.Autostart = false
		hc.Graphics.Mode = hostconfig.ModeDesktop
		hc.Graphics.VO = "null"
		hc.Graphics.Fullscreen = false
		hc.Graphics.HWDec = ""
		hc.Graphics.HDRPassthrough = false
		hc.Graphics.Display = ""
		hc.Audio.Device = "null"
		hc.Audio.Channels = ""
		return hc
	}
	return New(cfg, hcOf, log.New(io.Discard, "", 0))
}

func TestLifecycleSpawnStop(t *testing.T) {
	hasMPV(t)
	socket := shortSocketPath(t, "scratch.sock")
	m := newManager(t, socket)

	ok, msg := m.Start(true)
	if !ok {
		t.Fatalf("start failed: %s", msg)
	}
	st := m.Status()
	if st.Mode != "child" || st.PID == 0 {
		t.Fatalf("expected child mode with pid, got %+v", st)
	}
	if !st.Running || !m.probe() {
		t.Fatalf("mpv should be running & responding: %+v", st)
	}
	if !st.DesiredRunning {
		t.Fatalf("desired_running should be true after start")
	}

	ok, msg = m.Stop()
	if !ok {
		t.Fatalf("stop failed: %s", msg)
	}
	// After stop the socket should stop responding.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !m.probe() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	st = m.Status()
	if st.Running || st.Mode != "stopped" {
		t.Fatalf("expected stopped after Stop(), got %+v", st)
	}
	if st.DesiredRunning {
		t.Fatalf("desired_running should be false after Stop()")
	}
}
