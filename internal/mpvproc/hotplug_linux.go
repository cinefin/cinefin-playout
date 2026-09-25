//go:build linux

package mpvproc

import (
	"context"
	"os"
	"time"

	"github.com/cinefin/cinefin-playout/internal/hardware"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// hasVTHandlers reports whether the current process environment could give a
// spawned mpv the controlling VT it needs for the SIGUSR1/SIGUSR2 re-modeset to
// work. We approximate: a controlling tty exists. If it doesn't, we won't
// signal (the guard in the watcher). This keeps us from sending signals to an
// mpv that has no VT handlers installed and would just die.
func hasVTHandlers() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// WatchDRM runs the display-hotplug re-modeset loop. When graphics.mode==drm on
// Linux, it polls /sys/class/drm for the configured connector and, on a
// disconnected→connected edge, issues mpv's SIGUSR1 (release VT) then SIGUSR2
// (re-acquire VT) — the pair validated in cinefin PR #235 that makes mpv
// re-modeset when a display returns. This retires that PR's shell watcher: the
// Go agent owns the process and performs the re-modeset itself.
//
// It only signals when the running child was spawned with a controlling VT
// (m.drmControlled), so a non-DRM or headless-without-tty mpv is never poked.
// drmRoot defaults to hardware.DefaultDRMRoot when empty (test override).
func (m *Manager) WatchDRM(ctx context.Context, drmRoot string) {
	interval := 2 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var prev map[string]string
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopMonitor:
			return
		case <-ticker.C:
		}

		hc := m.hcOf()
		if hc.Graphics.Mode != hostconfig.ModeDRM || hc.Graphics.DRMConnector == "" {
			prev = nil
			continue
		}
		cur := hardware.DRMConnectorStatuses(drmRoot)
		if connectorEdge(prev, cur, hc.Graphics.DRMConnector) {
			m.remodeset()
		}
		prev = cur
	}
}

// remodeset sends the SIGUSR1→SIGUSR2 pair to the child mpv, guarded by the
// controlling-VT check.
func (m *Manager) remodeset() {
	m.mu.Lock()
	proc := m.proc
	controlled := m.drmControlled && m.childAlive()
	m.mu.Unlock()
	if proc == nil || !controlled {
		return
	}
	m.logger.Printf("drm hotplug: re-modeset (SIGUSR1→SIGUSR2) pid=%d", proc.Pid)
	_ = signalProcess(proc, sigUSR1)
	time.Sleep(150 * time.Millisecond)
	_ = signalProcess(proc, sigUSR2)
}
