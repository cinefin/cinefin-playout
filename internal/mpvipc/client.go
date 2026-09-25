// Package mpvipc is a client for mpv's local JSON-IPC endpoint.
//
// On Linux/macOS this is a unix socket; on Windows a named pipe (selected by
// os-tagged dial_*.go files). The client holds one persistent connection and
// speaks line-delimited JSON in both directions: raw frames are written with
// Send and every inbound line is delivered verbatim to an OnMessage callback.
// The relay owns request_id remapping and reply routing; this client stays a
// dumb byte-level conduit so mpv's own vocabulary passes through untranslated.
package mpvipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// ErrNotConnected is returned by Send when there is no live mpv connection.
var ErrNotConnected = errors.New("mpv ipc not connected")

// Client is a persistent line-delimited JSON connection to mpv's IPC endpoint.
type Client struct {
	path string

	mu     sync.Mutex
	conn   net.Conn
	closed bool

	// onMessage is invoked (from the read goroutine) with each raw inbound
	// JSON line, newline stripped. Set via OnMessage before Connect.
	onMessage func([]byte)
	// onConnState is invoked when the connection goes up (true) or down (false).
	onConnState func(up bool)
}

// New creates a Client for the given socket/pipe path. It does not connect yet.
func New(path string) *Client {
	return &Client{path: path}
}

// OnMessage registers the handler for inbound JSON lines. Not concurrency-safe
// with a running read loop; call before Connect.
func (c *Client) OnMessage(fn func([]byte)) { c.onMessage = fn }

// OnConnState registers a handler for connection up/down transitions.
func (c *Client) OnConnState(fn func(up bool)) { c.onConnState = fn }

// Connect dials mpv once. On success it starts the read loop and returns nil.
func (c *Client) Connect(ctx context.Context) error {
	conn, err := dial(ctx, c.path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return errors.New("mpv ipc client closed")
	}
	c.conn = conn
	c.mu.Unlock()

	if c.onConnState != nil {
		c.onConnState(true)
	}
	go c.readLoop(conn)
	return nil
}

// Run maintains the connection: it dials, serves, and on disconnect retries
// with a short backoff until ctx is cancelled. Intended to run in a goroutine.
func (c *Client) Run(ctx context.Context) {
	const (
		minBackoff = 200 * time.Millisecond
		maxBackoff = 3 * time.Second
	)
	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.Connect(ctx)
		if err == nil {
			// Connected; block until the connection drops.
			c.waitClosed(ctx)
			backoff = minBackoff
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// waitClosed blocks until the current connection is torn down or ctx is done.
func (c *Client) waitClosed(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			down := c.conn == nil || c.closed
			c.mu.Unlock()
			if down {
				return
			}
		}
	}
}

func (c *Client) readLoop(conn net.Conn) {
	r := bufio.NewReaderSize(conn, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := trimNewline(line)
			if len(trimmed) > 0 && c.onMessage != nil {
				// Copy: bufio may reuse the buffer.
				buf := make([]byte, len(trimmed))
				copy(buf, trimmed)
				c.onMessage(buf)
			}
		}
		if err != nil {
			break
		}
	}
	// Connection ended.
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	if c.onConnState != nil {
		c.onConnState(false)
	}
}

// Send writes a raw JSON frame (a newline is appended). Returns ErrNotConnected
// if mpv is not currently reachable.
func (c *Client) Send(frame []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	buf := make([]byte, 0, len(frame)+1)
	buf = append(buf, frame...)
	buf = append(buf, '\n')
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(buf); err != nil {
		return err
	}
	return nil
}

// Connected reports whether a live connection to mpv currently exists.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.closed
}

// Close tears down the client and any live connection.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// Probe opens a one-shot connection, sends a single command frame, reads one
// reply line and returns it. Used by /status to test mpv reachability without
// disturbing a running relay connection. The command defaults to
// get_property mpv-version.
func Probe(ctx context.Context, path string) (string, error) {
	conn, err := dial(ctx, path)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	}

	req := map[string]any{"command": []any{"get_property", "mpv-version"}, "request_id": 1}
	frame, _ := json.Marshal(req)
	frame = append(frame, '\n')
	if _, err := conn.Write(frame); err != nil {
		return "", err
	}

	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var reply struct {
				RequestID *int   `json:"request_id"`
				Error     string `json:"error"`
				Data      any    `json:"data"`
			}
			if json.Unmarshal(trimNewline(line), &reply) == nil && reply.RequestID != nil {
				if s, ok := reply.Data.(string); ok {
					return s, nil
				}
				return reply.Error, nil
			}
			// Otherwise it was an async event; keep reading for the reply.
		}
		if err != nil {
			return "", err
		}
	}
}
