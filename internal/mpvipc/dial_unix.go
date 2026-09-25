//go:build !windows

package mpvipc

import (
	"context"
	"net"
)

// dial connects to mpv's local IPC unix socket.
func dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
