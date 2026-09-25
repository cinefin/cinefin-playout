//go:build windows

package mpvipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// dial connects to mpv's local IPC named pipe (e.g. \\.\pipe\mpv-…).
func dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}
