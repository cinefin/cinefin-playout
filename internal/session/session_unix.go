//go:build !windows && !darwin

package session

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Desktop reports whether a Wayland or X11 display is available to this user.
//
// The environment is not enough on its own: a shell inside tmux keeps the
// environment the tmux server started with, and an agent started from ssh or a
// service manager has no display variables either, even while the user's
// display is up. So when WAYLAND_DISPLAY/DISPLAY are missing, the agent looks
// for the sockets the session leaves behind (a Wayland socket in the user's
// runtime directory, an X socket in /tmp/.X11-unix owned by the user) and
// exports what it finds, so the tray and mpv, which inherit the agent's
// environment, connect to it.
func Desktop() bool { return detect() }

// UseServer points the agent (and so the tray and mpv) at a display server
// given on the command line: an X display (":0") or a Wayland socket
// ("wayland-1", or a path). Call it before Desktop.
func UseServer(v string) error {
	if xDisplayRe.MatchString(v) {
		return os.Setenv("DISPLAY", v)
	}
	if !strings.HasPrefix(v, "/") && os.Getenv("XDG_RUNTIME_DIR") == "" {
		_ = os.Setenv("XDG_RUNTIME_DIR", runtimeDir())
	}
	return os.Setenv("WAYLAND_DISPLAY", v)
}

// Displays describes the display variables in effect, for the startup log.
func Displays() string {
	var parts []string
	for _, k := range []string{"WAYLAND_DISPLAY", "DISPLAY"} {
		if v := os.Getenv(k); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

var detect = sync.OnceValue(func() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != "" {
		return true
	}
	return adoptSockets()
})

// adoptSockets exports the display variables for any display sockets found,
// and reports whether it found one.
func adoptSockets() bool {
	found := false
	if dir, name := waylandSocket(); name != "" {
		if os.Getenv("XDG_RUNTIME_DIR") == "" {
			_ = os.Setenv("XDG_RUNTIME_DIR", dir)
		}
		_ = os.Setenv("WAYLAND_DISPLAY", name)
		found = true
	}
	if d := xDisplay(); d != "" {
		_ = os.Setenv("DISPLAY", d)
		found = true
	}
	return found
}

// Overridable in tests.
var (
	runtimeDir = func() string {
		if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
			return d
		}
		return "/run/user/" + strconv.Itoa(os.Getuid())
	}
	x11Dir = "/tmp/.X11-unix"
)

// waylandSocket returns the runtime directory and the name of the first
// Wayland socket in it (e.g. "wayland-1"), or "" when there is none.
func waylandSocket() (dir, name string) {
	dir = runtimeDir()
	matches, _ := filepath.Glob(filepath.Join(dir, "wayland-*"))
	sort.Strings(matches)
	for _, m := range matches {
		if strings.HasSuffix(m, ".lock") || !isOwnSocket(m) {
			continue
		}
		return dir, filepath.Base(m)
	}
	return dir, ""
}

// xDisplay returns the display (e.g. ":0") of the first X server socket owned
// by this user (Xwayland, or an Xorg the user started), or "".
func xDisplay() string {
	matches, _ := filepath.Glob(filepath.Join(x11Dir, "X*"))
	sort.Strings(matches)
	for _, m := range matches {
		n := strings.TrimPrefix(filepath.Base(m), "X")
		if _, err := strconv.Atoi(n); err != nil || !isOwnSocket(m) {
			continue
		}
		return ":" + n
	}
	return ""
}

// isOwnSocket reports whether path is a unix socket owned by this user.
func isOwnSocket(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
