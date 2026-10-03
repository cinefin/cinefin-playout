package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The test sound. Cinefin's "Add a player" wizard plays it (POST /testsound)
// so the user can hear that sound comes out of the right device and channels:
// a tone on the left channel, then on the right. It plays through the main mpv,
// so it goes to the configured audio device with the configured channel
// layout, which is what is being tested. The tone is an av://lavfi: source (no
// file); when it ends, mpv goes idle and the player returns to standby.
//
// The agent observes mpv's playback position under its own observer id while
// the tone plays, so the test card lights the speaker box of the side actually
// sounding rather than guessing from a clock.

// The tone: toneFreq Hz at toneLevel (-12 dBFS peak), toneSide on each side,
// with 50 ms ramps so it does not click.
const (
	toneFreq  = 440
	toneLevel = 0.25
	toneSide  = 1500 * time.Millisecond
	toneTotal = 2 * toneSide
	// toneGrace is how long past toneTotal the agent waits for mpv to report
	// the end before it returns to standby anyway.
	toneGrace = 5 * time.Second
)

// timeObserverID is the agent's observer of mpv's playback position while the
// test sound plays (see agentRequestID).
const timeObserverID = 2_000_000_004

// toneURL is the tone as an mpv source: a stereo aevalsrc whose first channel
// (left) carries the tone for the first toneSide and the second (right) for
// the next. clip(20*min(...)) is the envelope: a 50 ms ramp in and out, and 0
// outside the side's slot.
var toneURL = func() string {
	side := toneSide.Seconds()
	tone := fmt.Sprintf("%g*sin(2*PI*%d*t)", toneLevel, toneFreq)
	return fmt.Sprintf("av://lavfi:aevalsrc=exprs='%s*clip(20*min(t,%g-t),0,1)|%s*clip(20*min(t-%g,%g-t),0,1)':c=stereo:s=48000:d=%g",
		tone, side, tone, side, 2*side, 2*side)
}()

// toneOptions are the tone's per-file options: play to the end and stop,
// whatever Cinefin set globally, and with audio on, named in the window title.
const toneOptions = "keep-open=no,loop-file=no,aid=auto,force-media-title=Test Sound"

// testSound is the test sound's state. Guarded by mu.
type testSound struct {
	mu     sync.Mutex
	gen    int     // bumped by each test sound, so a stale timeout does nothing
	active bool    // loaded and not finished
	seen   bool    // mpv has reported the tone as its path
	pos    float64 // mpv's playback position in the tone, < 0 until known
}

var errTonePlaying = errors.New("a test sound is already playing")

// channel is the side of the test sound that is playing: "left", "right" or
// "" (none, or mpv has not reported a position yet).
func (ts *testSound) channel() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.channelLocked()
}

func (ts *testSound) channelLocked() string {
	switch {
	case !ts.active || !ts.seen || ts.pos < 0:
		return ""
	case ts.pos < toneSide.Seconds():
		return "left"
	case ts.pos < toneTotal.Seconds():
		return "right"
	}
	return ""
}

// playing reports whether a test sound is loaded and not finished.
func (ts *testSound) playing() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.active
}

// reset forgets any test sound (mpv restarted, or the player was unpaired).
func (ts *testSound) reset() {
	ts.mu.Lock()
	ts.active = false
	ts.gen++
	ts.mu.Unlock()
}

// startTestSound loads the tone in place of whatever mpv is playing and
// observes its position.
func (s *Server) startTestSound() error {
	ts := &s.tone
	ts.mu.Lock()
	if ts.active {
		ts.mu.Unlock()
		return errTonePlaying
	}
	ts.gen++
	gen := ts.gen
	ts.active, ts.seen, ts.pos = true, false, -1
	ts.mu.Unlock()

	// Unobserve first: observing an id twice would add a second observer.
	for _, cmd := range [][]any{
		{"unobserve_property", timeObserverID},
		{"observe_property", timeObserverID, "playback-time"},
		{"loadfile", toneURL, "replace", -1, toneOptions},
		{"set_property", "pause", false},
	} {
		if err := s.backend.Send(agentCommand(cmd...)); err != nil {
			ts.reset()
			return err
		}
	}
	time.AfterFunc(toneTotal+toneGrace, func() { s.toneTimedOut(gen) })
	return nil
}

// tonePath follows mpv's path while a test sound plays: the tone's URL means
// it is playing; nothing loaded after that means it finished, and the player
// returns to standby; another file means Cinefin loaded something, and the
// test sound just ends.
func (s *Server) tonePath(path string) {
	ts := &s.tone
	ts.mu.Lock()
	if !ts.active {
		ts.mu.Unlock()
		return
	}
	switch {
	case path == toneURL:
		ts.seen = true
		ts.mu.Unlock()
		s.card.wake()
		return
	case !ts.seen: // still the file before the tone, or between the two
		ts.mu.Unlock()
		return
	}
	ts.active = false
	ts.mu.Unlock()
	// Not on mpv's read goroutine, which delivered this event.
	go s.endTestSound(path == "")
}

// toneTime takes mpv's playback position from the agent's observer, and
// redraws the card when the side sounding changes.
func (s *Server) toneTime(data json.RawMessage) {
	pos := -1.0
	_ = json.Unmarshal(data, &pos)
	ts := &s.tone
	ts.mu.Lock()
	before := ts.channelLocked()
	ts.pos = pos
	changed := ts.channelLocked() != before
	ts.mu.Unlock()
	if changed {
		s.card.wake()
	}
}

// toneTimedOut ends test sound gen should mpv not have reported its end in
// time, returning to standby unless something else was loaded meanwhile.
func (s *Server) toneTimedOut(gen int) {
	ts := &s.tone
	ts.mu.Lock()
	if !ts.active || ts.gen != gen {
		ts.mu.Unlock()
		return
	}
	ts.active = false
	ts.mu.Unlock()
	s.standby.mu.Lock()
	path := s.standby.path
	s.standby.mu.Unlock()
	s.log.Printf("testsound: mpv did not report the end of the tone; stopping it")
	s.endTestSound(path == "" || path == toneURL)
}

// endTestSound stops observing the position, redraws the card, and returns to
// standby when toStandby.
func (s *Server) endTestSound(toStandby bool) {
	_ = s.backend.Send(agentCommand("unobserve_property", timeObserverID))
	s.card.wake()
	if toStandby {
		s.enterStandby()
	}
}

// testSoundStatus describes the test sound for /status.
func (s *Server) testSoundStatus() map[string]any {
	ts := &s.tone
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return map[string]any{"playing": ts.active, "channel": ts.channelLocked()}
}

// handleTestSound plays the test sound. It is allowed only on standby or with
// the test card on, so it never cuts into a programme, and not while one is
// already playing. The reply gives the sequence's timing.
func (s *Server) handleTestSound(w http.ResponseWriter, _ *http.Request) {
	if !s.backend.Connected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "mpv not running"})
		return
	}
	if !s.standby.onStandby() && s.card.testCardLeft() == 0 && !s.tone.playing() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "the test sound plays only on standby or with the test card on"})
		return
	}
	switch err := s.startTestSound(); {
	case errors.Is(err, errTonePlaying):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	side := toneSide.Milliseconds()
	writeJSON(w, http.StatusOK, map[string]any{
		"playing":      true,
		"frequency_hz": toneFreq,
		"duration_ms":  toneTotal.Milliseconds(),
		"sequence": []map[string]any{
			{"channel": "left", "start_ms": 0, "duration_ms": side},
			{"channel": "right", "start_ms": side, "duration_ms": side},
		},
	})
}

// timeObserverIDJSON is timeObserverID as it appears in mpv's frames.
var timeObserverIDJSON = strconv.Itoa(timeObserverID)
