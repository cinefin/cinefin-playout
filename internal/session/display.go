package session

import (
	"regexp"
	"strings"
)

var (
	xDisplayRe = regexp.MustCompile(`^[A-Za-z0-9.\-]*:\d+(\.\d+)?$`) // ":0", "host:0.0"
	waylandRe  = regexp.MustCompile(`^wayland-\d+$`)                 // "wayland-1"
)

// IsServer reports whether a --display value names a display server (an X
// display or a Wayland socket) rather than a screen.
func IsServer(v string) bool {
	return xDisplayRe.MatchString(v) || waylandRe.MatchString(v) || strings.HasPrefix(v, "/")
}
