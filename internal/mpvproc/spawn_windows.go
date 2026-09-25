//go:build windows

package mpvproc

import (
	"os"
	"os/exec"
	"time"
)

// spawn launches mpv on Windows. There is no DRM/VT concept, so the drm flag is
// ignored (Windows is desktop-only per the redesign).
func spawn(argv []string, env []string, logFile *os.File, _ bool) (*os.Process, chan struct{}, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		procExit.store(cmd.Process.Pid, cmd.ProcessState)
		close(waitCh)
	}()
	return cmd.Process, waitCh, nil
}

// terminateProcess kills the process (Windows has no SIGTERM; Kill is the
// graceful-fallback here, matching the redesign's os-tagged note).
func terminateProcess(p *os.Process) {
	_ = p.Kill()
}

// signalProcess is a no-op on Windows (no SIGUSR signals; DRM mode is unavailable).
func signalProcess(_ *os.Process, _ os.Signal) error {
	return nil
}

var sigUSR1 os.Signal = os.Interrupt
var sigUSR2 os.Signal = os.Interrupt

// socketExists: Windows named pipes have no filesystem stat; report false and
// rely on socketResponding (an IPC probe) for liveness.
func socketExists(_ string) bool {
	return false
}

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
