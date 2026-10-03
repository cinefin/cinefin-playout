package server

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The protocol is the version of the API Cinefin speaks to the player. The
// player serves a range of them, minProtocol to protocolVersion, and reports
// both from /health and /pair; Cinefin sends the one it speaks in the
// Cinefin-Protocol header on every request. Either side can then tell which one
// needs updating.
//
// Raise protocolVersion when Cinefin is to rely on something new here (Cinefin
// raises its own to match). Raise minProtocol, and minCinefin with it, when
// something an older Cinefin uses is removed.
//
//	1: Cinefin before v0.3.0 (sends no header)
//	2: the player owns standby (/standby), pairing by code
const (
	protocolVersion = 2
	minProtocol     = 2
	// minCinefin is the first Cinefin release that speaks minProtocol.
	minCinefin = "v0.3.0"
)

// protocolHeader carries the protocol Cinefin speaks. A request without it is
// from a Cinefin older than the header, which spoke protocol 1.
const protocolHeader = "Cinefin-Protocol"

// mismatchShowFor is how long the TV keeps saying which side needs updating
// after a refused request. Cinefin retries well within it, so the message stays
// up while a mismatched Cinefin keeps trying.
const mismatchShowFor = 5 * time.Minute

// protocolOf is the protocol a request says it speaks, and whether it said so.
func protocolOf(r *http.Request) (int, bool) {
	h := strings.TrimSpace(r.Header.Get(protocolHeader))
	if h == "" {
		return 1, false
	}
	n, err := strconv.Atoi(h)
	if err != nil {
		return 0, true
	}
	return n, true
}

// protocolGate refuses requests from a Cinefin whose protocol is outside the
// range this player serves, with 426 and a sentence saying what to update. It
// runs before the token check, so even a Cinefin that can no longer
// authenticate (an old one, which never paired) shows the reason. /health
// stays open, since it is how Cinefin learns the range, and so do the
// loopback-only /local routes.
func (s *Server) protocolGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/local/") {
			next.ServeHTTP(w, r)
			return
		}
		p, said := protocolOf(r)
		if p >= minProtocol && p <= protocolVersion {
			next.ServeHTTP(w, r)
			return
		}
		cinefinOld := p < minProtocol
		// Only something that looks like Cinefin (it named a protocol, or sent
		// a token as every Cinefin does) gets to put a message on the TV; a
		// browser or a scanner poking the port does not.
		if said || r.Header.Get("Authorization") != "" {
			s.mismatch.note(remoteHost(r.RemoteAddr), cinefinOld)
			s.card.wake()
		}
		msg := "This player needs Cinefin " + minCinefin + " or later. Update Cinefin, then pair the player with the code on its screen."
		if !cinefinOld {
			msg = "This Cinefin needs a newer player. Update cinefin-playout on this player to the latest release."
		}
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error":        msg,
			"protocol":     protocolVersion,
			"min_protocol": minProtocol,
		})
	})
}

// protocolMismatch remembers the last Cinefin refused for its protocol, for
// the TV.
type protocolMismatch struct {
	mu         sync.Mutex
	at         time.Time
	from       string
	cinefinOld bool
}

func (m *protocolMismatch) note(from string, cinefinOld bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at, m.from, m.cinefinOld = time.Now(), from, cinefinOld
}

// recent is the last refusal within mismatchShowFor: who it came from and
// whether Cinefin (rather than this player) is the one to update.
func (m *protocolMismatch) recent() (from string, cinefinOld, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.at.IsZero() || time.Since(m.at) > mismatchShowFor {
		return "", false, false
	}
	return m.from, m.cinefinOld, true
}

// clear forgets the refusal, once a Cinefin that speaks a protocol in range
// has paired or connected.
func (m *protocolMismatch) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at = time.Time{}
}

// mismatchNotice is the TV's one line about the last refusal, "" for none.
// The pairing box shows it with Cinefin's address; the offline notice, whose
// title already names the address, without.
func (s *Server) mismatchNotice(withAddress bool) string {
	from, cinefinOld, ok := s.mismatch.recent()
	if !ok {
		return ""
	}
	who := "Cinefin"
	if withAddress && from != "" {
		who += " at " + from
	}
	if cinefinOld {
		return who + " needs updating to " + minCinefin + " or later."
	}
	return who + " needs a newer version of this player."
}
