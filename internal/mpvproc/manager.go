// Package mpvproc owns mpv as a supervised child process.
//
// It spawns mpv from the host config, supervises it in a monitor goroutine with
// exponential backoff, and stops it gracefully (quit over IPC, then
// terminate/kill). The agent owns mpv exclusively on this socket. Launch args
// come from the host-owned hostconfig (BuildMPVArgs), not a Cinefin push.
//
// On Linux in DRM mode it additionally re-modesets mpv on display hotplug so a
// projector power-cycle recovers without a restart (see hotplug_linux.go).
package mpvproc

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"git.kef2.net/micky/cinefin-playout/internal/config"
	"git.kef2.net/micky/cinefin-playout/internal/hostconfig"
	"git.kef2.net/micky/cinefin-playout/internal/mpvipc"
)

// Backoff and grace constants for mpv supervision.
const (
	backoffInitial  = 1 * time.Second
	backoffMax      = 30 * time.Second
	quitGrace       = 8 * time.Second
	healthyReset    = 60 * time.Second
	socketWaitTotal = 15 * time.Second
	maxMPVLogBytes  = 5 << 20 // rotate mpv-stdout.log past 5 MiB
)

// Status is the process status reported to /status.
type Status struct {
	Running        bool    `json:"running"`
	Mode           string  `json:"mode"` // "child" | "stopped"
	PID            int     `json:"pid,omitempty"`
	StartedAt      float64 `json:"started_at,omitempty"`
	UptimeSeconds  float64 `json:"uptime_seconds,omitempty"`
	Restarts       int     `json:"restarts"`
	LastExitCode   *int    `json:"last_exit_code"`
	SocketPresent  bool    `json:"socket_present"`
	DesiredRunning bool    `json:"desired_running"`
}

// HostConfigProvider yields the current launch config at spawn time. main wires
// this to a fresh config.Load of config.toml so an edit to the [graphics]/[audio]
// sections takes effect on the next (re)start without the manager caching a
// stale copy.
type HostConfigProvider func() hostconfig.HostConfig

// Manager supervises the mpv child process.
type Manager struct {
	cfg     config.Config
	hcOf    HostConfigProvider
	logger  *log.Logger
	ipcPath string

	mu             sync.Mutex
	proc           *os.Process
	waitCh         chan struct{} // closed when the current proc exits
	desiredRunning bool
	startedAt      time.Time
	restarts       int
	lastExitCode   *int
	backoff        time.Duration
	nextRestartAt  time.Time
	drmControlled  bool // true if the running child was spawned with a controlling VT (DRM mode)

	stopMonitor chan struct{}
	monitorDone chan struct{}
}

// New builds a Manager. hcOf provides the current host config at spawn time.
func New(cfg config.Config, hcOf HostConfigProvider, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{
		cfg:         cfg,
		hcOf:        hcOf,
		logger:      logger,
		ipcPath:     cfg.IPCSocket,
		backoff:     backoffInitial,
		stopMonitor: make(chan struct{}),
	}
}

// probe reports whether a responding mpv already owns the IPC socket.
func (m *Manager) probe() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, err := mpvipc.Probe(ctx, m.ipcPath)
	return err == nil
}

// StartMonitor launches the supervision goroutine. Idempotent-ish: call once.
func (m *Manager) StartMonitor() {
	m.monitorDone = make(chan struct{})
	go m.monitorLoop()
}

// Shutdown stops the monitor goroutine (does not stop mpv).
func (m *Manager) Shutdown() {
	select {
	case <-m.stopMonitor:
	default:
		close(m.stopMonitor)
	}
	if m.monitorDone != nil {
		<-m.monitorDone
	}
}

// Start ensures mpv is running. wait blocks until the IPC socket is ready.
func (m *Manager) Start(wait bool) (bool, string) {
	m.mu.Lock()
	if m.childAlive() {
		m.desiredRunning = true
		m.mu.Unlock()
		return true, "mpv already running"
	}
	m.desiredRunning = true
	m.backoff = backoffInitial
	ok, msg := m.spawnLocked()
	m.mu.Unlock()

	if ok && wait {
		if m.waitForSocket(socketWaitTotal) {
			return true, "mpv started, IPC socket ready"
		}
		return false, "mpv started but IPC socket did not appear within 15s"
	}
	return ok, msg
}

// Stop gracefully stops mpv: quit over IPC, wait grace, then terminate/kill.
func (m *Manager) Stop() (bool, string) {
	m.mu.Lock()
	m.desiredRunning = false
	proc := m.proc
	waitCh := m.waitCh
	m.mu.Unlock()

	if proc == nil {
		return true, "mpv not running"
	}

	sendQuit(m.ipcPath)

	// Wait for graceful exit, else terminate then kill (os-tagged).
	if !waitFor(waitCh, quitGrace) {
		terminateProcess(proc)
		if !waitFor(waitCh, 3*time.Second) {
			_ = proc.Kill()
			waitFor(waitCh, 2*time.Second)
		}
	}

	m.mu.Lock()
	m.proc = nil
	m.startedAt = time.Time{}
	m.drmControlled = false
	m.mu.Unlock()
	return true, "mpv stopped"
}

// Restart stops then starts.
func (m *Manager) Restart(wait bool) (bool, string) {
	m.Stop()
	return m.Start(wait)
}

// Autostart spawns mpv at boot if the host config asks for it and none is
// running. The autostart flag is host-owned (hostconfig.Autostart), so it is
// read fresh here rather than cached from the agent's transport config.
func (m *Manager) Autostart() {
	if !m.hcOf().Autostart {
		return
	}
	ok, msg := m.Start(false)
	m.logger.Printf("autostart: %s (ok=%v)", msg, ok)
}

// -- internals -------------------------------------------------------------

func (m *Manager) childAlive() bool {
	if m.proc == nil {
		return false
	}
	select {
	case <-m.waitCh:
		return false
	default:
		return true
	}
}

// spawnLocked launches mpv. Caller holds m.mu.
func (m *Manager) spawnLocked() (bool, string) {
	hc := m.hcOf()
	args := hostconfig.BuildMPVArgs(hc, m.ipcPath)
	full := append([]string{m.cfg.ResolveMPVBinary()}, args...)

	logPath := filepath.Join(m.cfg.StateDir, "mpv-stdout.log")
	_ = os.MkdirAll(m.cfg.StateDir, 0o755)
	// Bound the log: on a box that runs for months an append-only mpv log grows
	// without limit. Rotate once (→ .1) when it crosses the threshold; each mpv
	// (re)spawn is a natural rotation point.
	rotateIfLarge(logPath, maxMPVLogBytes)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		m.logger.Printf("failed to open mpv log %s: %v", logPath, err)
	}

	drm := isDRMMode(hc)
	proc, waitCh, err := spawn(full, m.spawnEnv(hc), logFile, drm)
	if logFile != nil {
		_ = logFile.Close()
	}
	if err != nil {
		m.logger.Printf("failed to launch mpv: %v", err)
		return false, "failed to launch mpv: " + err.Error()
	}

	m.proc = proc
	m.waitCh = waitCh
	m.startedAt = time.Now()
	m.drmControlled = drm && hasVTHandlers()
	m.logger.Printf("launched mpv pid=%d: %v", proc.Pid, full)

	return true, "mpv launched (pid " + strconv.Itoa(proc.Pid) + ")"
}

func (m *Manager) spawnEnv(hc hostconfig.HostConfig) []string {
	env := os.Environ()
	// Desktop mode may carry an explicit X display from the host config; when it
	// is blank mpv inherits whatever DISPLAY the agent's own environment has
	// (e.g. the systemd unit's Environment=DISPLAY=:0). DRM mode uses no display.
	if hc.Graphics.Mode == hostconfig.ModeDesktop && hc.Graphics.Display != "" {
		env = append(env, "DISPLAY="+hc.Graphics.Display)
	}
	for k, v := range m.cfg.ExtraEnv {
		env = append(env, k+"="+v)
	}
	return env
}

func (m *Manager) waitForSocket(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.probe() {
			return true
		}
		m.mu.Lock()
		alive := m.childAlive()
		m.mu.Unlock()
		if !alive {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

func (m *Manager) monitorLoop() {
	defer close(m.monitorDone)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopMonitor:
			return
		case <-ticker.C:
		}

		m.mu.Lock()
		if !m.desiredRunning {
			m.mu.Unlock()
			continue
		}
		if m.childAlive() {
			if !m.startedAt.IsZero() && time.Since(m.startedAt) > healthyReset {
				m.backoff = backoffInitial
			}
			m.mu.Unlock()
			continue
		}
		if m.proc != nil {
			// Child exited unexpectedly.
			code := exitCode(m.proc, m.waitCh)
			m.lastExitCode = code
			m.logger.Printf("mpv exited unexpectedly (code %v); restarting in %.0fs", derefInt(code), m.backoff.Seconds())
			m.proc = nil
			m.startedAt = time.Time{}
			m.drmControlled = false
			m.nextRestartAt = time.Now().Add(m.backoff)
			m.backoff = min(m.backoff*2, backoffMax)
			m.mu.Unlock()
			continue
		}
		// proc is nil, desired running: a restart is pending.
		due := time.Now().After(m.nextRestartAt) || m.nextRestartAt.IsZero()
		if due {
			m.restarts++
			if ok, _ := m.spawnLocked(); !ok {
				// Launch itself failed (bad mpv binary, VT refused, …). Back
				// off like a crash-exit rather than hammering exec every tick.
				m.nextRestartAt = time.Now().Add(m.backoff)
				m.backoff = min(m.backoff*2, backoffMax)
			}
		}
		m.mu.Unlock()
	}
}

// Status returns the current process status. It reports process facts only and
// does not dial the IPC socket — socket reachability is a Probe concern, surfaced
// by /status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	child := m.childAlive()

	mode := "stopped"
	if child {
		mode = "child"
	}

	st := Status{
		Running:        child,
		Mode:           mode,
		Restarts:       m.restarts,
		LastExitCode:   m.lastExitCode,
		SocketPresent:  socketExists(m.ipcPath),
		DesiredRunning: m.desiredRunning,
	}
	if child {
		st.PID = m.proc.Pid
		st.StartedAt = float64(m.startedAt.Unix())
		st.UptimeSeconds = time.Since(m.startedAt).Seconds()
	}
	return st
}

// waitFor blocks until ch closes or timeout elapses; true if ch closed.
func waitFor(ch <-chan struct{}, timeout time.Duration) bool {
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

func isDRMMode(hc hostconfig.HostConfig) bool {
	return hc.Graphics.Mode == hostconfig.ModeDRM
}

// rotateIfLarge renames path → path+".1" when it exceeds limit bytes, replacing
// any previous ".1". Best-effort: any error leaves the log as-is.
func rotateIfLarge(path string, limit int64) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < limit {
		return
	}
	_ = os.Rename(path, path+".1")
}

func derefInt(p *int) any {
	if p == nil {
		return "unknown"
	}
	return *p
}
