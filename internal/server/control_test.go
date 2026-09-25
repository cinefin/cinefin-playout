package server

import (
	"encoding/json"
	"sync"
	"testing"
)

// fakeLink is a mpvLink whose Connected flag is toggleable and that records the
// frames forwarded to it.
type fakeLink struct {
	mu        sync.Mutex
	sent      [][]byte
	connected bool
}

func (f *fakeLink) Send(frame []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(frame))
	copy(cp, frame)
	f.sent = append(f.sent, cp)
	return nil
}

func (f *fakeLink) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeLink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// collector captures frames delivered to a control client.
type collector struct {
	mu     sync.Mutex
	frames [][]byte
}

func (c *collector) send(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	c.frames = append(c.frames, cp)
}

func (c *collector) all() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.frames))
	copy(out, c.frames)
	return out
}

// Command frames pass through to mpv verbatim — no request_id rewriting.
func TestInboundForwardsVerbatim(t *testing.T) {
	link := &fakeLink{connected: true}
	c := newControl(link)
	c.attach((&collector{}).send, nil)

	frame := []byte(`{"command":["get_property","pause"],"request_id":1}`)
	c.inbound(frame)

	if link.count() != 1 {
		t.Fatalf("forwarded %d frames, want 1", link.count())
	}
	if string(link.sent[0]) != string(frame) {
		t.Fatalf("forwarded %s, want verbatim %s", link.sent[0], frame)
	}
}

// mpv frames (replies + events) reach the attached client verbatim.
func TestFromMPVDeliveredToClient(t *testing.T) {
	c := newControl(&fakeLink{connected: true})
	col := &collector{}
	c.attach(col.send, nil)

	event := `{"event":"property-change","name":"time-pos","data":12.3}`
	c.fromMPV([]byte(event))

	got := col.all()
	if len(got) != 1 || string(got[0]) != event {
		t.Fatalf("client got %v, want [%s]", got, event)
	}
}

// When mpv is down a command gets an immediate error reply (with its request_id
// echoed) instead of being forwarded, so the client fails fast.
func TestInboundMPVDownErrorReply(t *testing.T) {
	link := &fakeLink{connected: false}
	c := newControl(link)
	col := &collector{}
	c.attach(col.send, nil)

	c.inbound([]byte(`{"command":["loadfile","x.mkv"],"request_id":7}`))

	if link.count() != 0 {
		t.Fatalf("nothing should be forwarded to a down mpv, got %d", link.count())
	}
	got := col.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 error frame, got %d", len(got))
	}
	var e struct {
		RequestID int    `json:"request_id"`
		Error     string `json:"error"`
	}
	if json.Unmarshal(got[0], &e) != nil || e.RequestID != 7 || e.Error == "" {
		t.Fatalf("bad error frame: %s", got[0])
	}
}

// A second client displaces the first (which is closed); disconnect closes the
// current one so it re-subscribes after an mpv restart.
func TestAttachDisplacesAndDisconnect(t *testing.T) {
	c := newControl(&fakeLink{connected: true})

	firstClosed := false
	c.attach((&collector{}).send, func() { firstClosed = true })
	c.attach((&collector{}).send, nil)
	if !firstClosed {
		t.Fatalf("attaching a second client should close the first")
	}
	if !c.connected() {
		t.Fatalf("a client should be attached")
	}

	secondClosed := false
	// Re-attach with a close hook so we can observe disconnect().
	c.attach((&collector{}).send, func() { secondClosed = true })
	c.disconnect()
	if !secondClosed {
		t.Fatalf("disconnect should close the current client")
	}
}

// detach only clears the client if it is still current (a later attach wins).
func TestDetachOnlyClearsCurrent(t *testing.T) {
	c := newControl(&fakeLink{connected: true})
	first := c.attach((&collector{}).send, nil)
	c.attach((&collector{}).send, nil) // displaces first
	c.detach(first)                    // stale handle: must not clear the current client
	if !c.connected() {
		t.Fatalf("detaching a displaced client should not clear the current one")
	}
}
