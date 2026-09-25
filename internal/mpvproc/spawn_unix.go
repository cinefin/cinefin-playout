//go:build !windows

package mpvproc

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// spawn launches mpv. On unix it starts a new session (start_new_session in the
// Python agent). In DRM mode we try to give mpv a *controlling terminal* so its
// VT switcher initialises — that is what lets SIGUSR1/SIGUSR2 drive a re-modeset
// on display hotplug (see hotplug_linux.go).
//
// The controlling-terminal setup is BEST-EFFORT and must never stop mpv from
// launching. Making the opened /dev/tty the child's controlling terminal
// (TIOCSCTTY after setsid) only succeeds when that terminal is free to be
// claimed — a dedicated VT the agent owns (systemd TTYPath=/dev/ttyN +
// StandardInput=tty). Run from an interactive SSH shell, /dev/tty is the
// shell's own controlling terminal and cannot be stolen, so TIOCSCTTY fails
// with EPERM and Start() reports "fork/exec: operation not permitted". In that
// case we retry as a plain new session: mpv still runs (any DRM-master problem
// is then a real, logged mpv error rather than an opaque agent loop), and the
// hotplug watcher's guard (hasVTHandlers) keeps us from signalling a player
// whose VT switcher never came up.
//
// waitCh is closed once the process exits; the returned proc is the child.
func spawn(argv []string, env []string, logFile *os.File, drm bool) (*os.Process, chan struct{}, error) {
	start := func(withCtty bool) (*exec.Cmd, *os.File, error) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = env
		if logFile != nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}
		sysattr := &syscall.SysProcAttr{Setsid: true}
		var tty *os.File
		if withCtty {
			if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
				tty = f
				cmd.Stdin = f
				sysattr.Setctty = true
			}
		}
		cmd.SysProcAttr = sysattr
		return cmd, tty, cmd.Start()
	}

	cmd, tty, err := start(drm)
	if err != nil && tty != nil {
		// The controlling-VT claim was refused (typically EPERM from an SSH
		// shell whose pty is already another session's controlling terminal).
		// Retry without it — mpv launches; only the VT-switch re-modeset is
		// unavailable, which the hotplug guard already handles.
		tty.Close()
		cmd, tty, err = start(false)
	}
	if tty != nil {
		defer tty.Close()
	}
	if err != nil {
		return nil, nil, err
	}

	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
		procExit.store(cmd.Process.Pid, cmd.ProcessState)
	}()
	return cmd.Process, waitCh, nil
}

// terminateProcess sends SIGTERM to the whole process group.
func terminateProcess(p *os.Process) {
	// Negative pid → the process group created by Setsid.
	_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	_ = p.Signal(syscall.SIGTERM)
}

// signalProcess sends an arbitrary signal to the process (DRM re-modeset uses
// SIGUSR1/SIGUSR2).
func signalProcess(p *os.Process, sig os.Signal) error {
	return p.Signal(sig)
}

var sigUSR1 os.Signal = syscall.SIGUSR1
var sigUSR2 os.Signal = syscall.SIGUSR2

// socketExists reports whether the IPC endpoint path exists on disk (unix
// socket). On Windows named pipes have no filesystem presence, handled there.
func socketExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// exitCode extracts the child's exit code once waitCh is closed.
func exitCode(p *os.Process, waitCh <-chan struct{}) *int {
	select {
	case <-waitCh:
	case <-time.After(50 * time.Millisecond):
		return nil
	}
	if st, ok := procExit.load(p.Pid); ok {
		code := st.ExitCode()
		return &code
	}
	return nil
}
