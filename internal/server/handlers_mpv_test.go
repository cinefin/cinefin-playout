//go:build !windows

package server

import (
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"git.kef2.net/micky/cinefin-playout/internal/config"
	"git.kef2.net/micky/cinefin-playout/internal/hostconfig"
)

// TestMPVLifecycleEndpoints drives a real mpv on a scratch socket through the
// /mpv/start and /mpv/stop REST endpoints.
func TestMPVLifecycleEndpoints(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv binary not available")
	}
	socket := shortSocketPath(t, "scratch.sock")
	cfg := config.Default()
	cfg.IPCSocket = socket
	cfg.Token = "secret"
	cfg.StateDir = t.TempDir()

	// A headless-safe launch config so the manager's spawn is side-effect-free.
	hc := hostconfig.Preset("linux")
	hc.Autostart = false
	hc.Graphics.VO = "null"
	hc.Graphics.Fullscreen = false
	hc.Graphics.HWDec = ""
	hc.Graphics.HDRPassthrough = false
	hc.Graphics.Display = ""
	hc.Audio.Device = "null"
	hc.Audio.Channels = ""

	ts := newServerForTest(t, cfg, func() hostconfig.HostConfig { return hc })

	// Start (wait for socket).
	resp := do(t, "POST", ts.URL+"/mpv/start?wait=true", nil)
	var startRes struct {
		OK     bool `json:"ok"`
		Status struct {
			Mode string `json:"mode"`
			PID  int    `json:"pid"`
		} `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&startRes)
	resp.Body.Close()
	if !startRes.OK || startRes.Status.Mode != "child" || startRes.Status.PID == 0 {
		t.Fatalf("start result unexpected: %+v", startRes)
	}

	// Stop.
	resp = do(t, "POST", ts.URL+"/mpv/stop", nil)
	var stopRes struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp.Body).Decode(&stopRes)
	resp.Body.Close()
	if !stopRes.OK {
		t.Fatalf("stop failed: %+v", stopRes)
	}

	// Confirm gone via /status.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp := do(t, "GET", ts.URL+"/status", nil)
		var st struct {
			MPV struct {
				SocketResponding bool `json:"socket_responding"`
			} `json:"mpv"`
		}
		json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if !st.MPV.SocketResponding {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mpv still responding after stop")
}
