package server

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

// control is the bridge between the /ws/control websocket and the local mpv IPC
// connection. It is a near-transparent conduit of mpv's own JSON-IPC frames (see
// docs/ARCHITECTURE.md, "Control channel"): a client's command frames are
// forwarded to mpv verbatim, and every inbound reply/event frame is forwarded to
// the client verbatim — no request_id remapping and no observer bookkeeping.
//
// Exactly one client (Cinefin) is ever connected, so there is nothing to
// multiplex: request_ids and property-observer ids pass through untouched, which
// is also why observers need no agent-side cleanup — a reconnecting client
// re-subscribes with the same ids, so mpv updates them in place rather than
// accumulating duplicates. If a second client dials it simply displaces the
// first (the old one is closed).
//
// It also keeps the link state the offline notice needs: when a client last
// attached or detached (counted from the agent's start before the first one).
type control struct {
	mpv mpvLink

	// onChange, if set, is called after a client attaches or detaches (the
	// card redraws). onPath and onTime, if set, receive the agent's own
	// observations of mpv's path (see Server.pathChanged) and of the test
	// sound's position (Server.toneTime). New sets all three.
	onChange func()
	onPath   func(data json.RawMessage)
	onTime   func(data json.RawMessage)

	mu      sync.Mutex
	client  *ctlClient
	changed time.Time // when client last changed between nil and set
}

// mpvLink is the slice of the player backend the control bridge needs.
type mpvLink interface {
	Send(frame []byte) error
	Connected() bool
}

// ctlClient is the currently-attached websocket peer. send delivers a frame to
// it; close force-disconnects it (used to make it reconnect after mpv restarts).
type ctlClient struct {
	send  func([]byte)
	close func()
}

func newControl(mpv mpvLink) *control { return &control{mpv: mpv, changed: time.Now()} }

// attach registers the (single) websocket peer, displacing and closing any
// previous one. The returned handle is passed back to detach.
func (c *control) attach(send func([]byte), close func()) *ctlClient {
	cl := &ctlClient{send: send, close: close}
	c.mu.Lock()
	prev := c.client
	c.client = cl
	if prev == nil {
		c.changed = time.Now()
	}
	c.mu.Unlock()
	if prev != nil && prev.close != nil {
		prev.close()
	}
	c.notify()
	return cl
}

// detach removes cl if it is still the current client (a later attach may have
// already displaced it).
func (c *control) detach(cl *ctlClient) {
	c.mu.Lock()
	gone := c.client == cl
	if gone {
		c.client = nil
		c.changed = time.Now()
	}
	c.mu.Unlock()
	if gone {
		c.notify()
	}
}

func (c *control) notify() {
	if c.onChange != nil {
		c.onChange()
	}
}

// disconnect force-closes the current client. Called when mpv reconnects
// (restart): the client re-establishes and re-subscribes against the fresh
// instance, which otherwise has none of its observers.
func (c *control) disconnect() {
	c.mu.Lock()
	cl := c.client
	c.mu.Unlock()
	if cl != nil && cl.close != nil {
		cl.close()
	}
}

// connected reports whether a control client is attached.
func (c *control) connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client != nil
}

// link reports whether a control client is attached and since when that has
// been so: the last attach, or the last detach (or the agent's start).
func (c *control) link() (connected bool, since time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client != nil, c.changed
}

// inbound forwards one raw client frame to mpv, or answers with a structured
// error (so the client's pending command fails fast) when mpv is unreachable.
func (c *control) inbound(raw []byte) {
	if c.mpv.Connected() {
		if err := c.mpv.Send(raw); err == nil {
			return
		}
	}
	c.toClient(errorFrame(requestID(raw), "mpv not running"))
}

// fromMPV forwards one raw mpv frame (reply or event) to the current client,
// except the agent's own traffic: replies to its commands (the overlay and
// standby), and changes to the properties it observes, which go to onPath and
// onTime.
func (c *control) fromMPV(raw []byte) {
	var f struct {
		RequestID json.RawMessage `json:"request_id"`
		Event     string          `json:"event"`
		ID        json.RawMessage `json:"id"`
		Data      json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &f) == nil {
		switch {
		case string(f.RequestID) == agentRequestIDJSON:
			return
		case f.Event == "property-change" && string(f.ID) == pathObserverIDJSON:
			if c.onPath != nil {
				c.onPath(f.Data)
			}
			return
		case f.Event == "property-change" && string(f.ID) == timeObserverIDJSON:
			if c.onTime != nil {
				c.onTime(f.Data)
			}
			return
		}
	}
	c.toClient(raw)
}

// The agent's own ids as they appear in mpv's frames.
var (
	agentRequestIDJSON = strconv.Itoa(agentRequestID)
	pathObserverIDJSON = strconv.Itoa(pathObserverID)
)

func (c *control) toClient(frame []byte) {
	c.mu.Lock()
	cl := c.client
	c.mu.Unlock()
	if cl != nil {
		cl.send(frame)
	}
}

// requestID extracts the raw request_id from a client frame, or nil if absent.
func requestID(raw []byte) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	return obj["request_id"]
}

// errorFrame builds a structured mpv-style error reply, echoing the client's
// original request_id when present so the reply routes back to its command.
func errorFrame(origID json.RawMessage, msg string) []byte {
	m := map[string]any{"error": msg}
	if len(origID) > 0 {
		m["request_id"] = origID
	}
	b, _ := json.Marshal(m)
	return b
}
