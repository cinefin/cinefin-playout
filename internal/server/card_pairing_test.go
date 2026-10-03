//go:build !windows

package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/ident"
	"github.com/cinefin/cinefin-playout/internal/pairing"
)

// overlays is the ASS text of each osd-overlay command sent to mpv.
func overlays(fp *fakePlayer) []string {
	var out []string
	for _, f := range fp.frames() {
		var cmd struct {
			Command struct {
				Name string `json:"name"`
				Data string `json:"data"`
			} `json:"command"`
		}
		if json.Unmarshal([]byte(f), &cmd) == nil && cmd.Command.Name == "osd-overlay" {
			out = append(out, cmd.Command.Data)
		}
	}
	return out
}

// setStandbyLoad pretends enterStandby loaded file (a name in the state
// directory) at the given time.
func setStandbyLoad(s *Server, file string, at time.Time) {
	sb := &s.standby
	sb.mu.Lock()
	sb.loaded, sb.loadedAt = filepath.Join(s.cfg.StateDir, file), at
	sb.mu.Unlock()
}

// The box waits for the System Ident's intro, taken from ident.IntroEnd, and
// for a cinema's own ident, which has no known intro, fades in at once.
func TestPairingRevealFollowsTheIdent(t *testing.T) {
	srv, _, _, _ := newPairTestServer(t)
	at := time.Now().Add(-time.Second)
	c := srv.card

	setStandbyLoad(srv, ident.FileName, at)
	c.mu.Lock()
	sc := c.pairingScene().(pairingScene)
	c.mu.Unlock()
	if want := at.Add(ident.IntroEnd); !sc.reveal.Equal(want) || ident.IntroEnd <= 0 {
		t.Errorf("System Ident: reveal %v after the load, want ident.IntroEnd (%v)", sc.reveal.Sub(at), ident.IntroEnd)
	}

	setStandbyLoad(srv, filepath.Join(identDir, strings.Repeat("a", 64)+".mp4"), at)
	c.mu.Lock()
	sc = c.pairingScene().(pairingScene)
	c.mu.Unlock()
	if !sc.reveal.Equal(at) {
		t.Errorf("cinema ident: reveal %v after the load, want 0", sc.reveal.Sub(at))
	}
}

// Before the intro ends nothing is drawn and the scene sleeps until it does;
// mid-fade the whole box is partly transparent and redrawn at frame rate;
// after the fade it is the full box on the idle countdown cadence.
func TestPairingBoxFadesInAfterTheIntro(t *testing.T) {
	loaded := time.Now()
	reveal := loaded.Add(ident.IntroEnd)
	expires := loaded.Add(pairing.CodeLifetime)
	sc := pairingScene{code: "340 957", address: "http://10.0.0.5:8089", expires: expires, reveal: reveal}

	for _, at := range []time.Duration{0, 2 * time.Second, ident.IntroEnd - time.Millisecond} {
		text, next := sc.at(loaded.Add(at))
		if text != "" {
			t.Errorf("%v after the load: box drawn during the intro:\n%s", at, text)
		}
		if want := ident.IntroEnd - at; next != want {
			t.Errorf("%v after the load: next = %v, want %v (the end of the intro)", at, next, want)
		}
	}

	now := reveal.Add(pairingFade / 2)
	text, next := sc.at(now)
	opacity := confirmProgress(pairingFade/2, 0, pairingFade)
	if opacity <= 0 || opacity >= 1 {
		t.Fatalf("mid-fade opacity %v", opacity)
	}
	if want := pairingBox("340 957", "http://10.0.0.5:8089", "", expires.Sub(now), opacity); text != want {
		t.Errorf("mid-fade frame is not the box at %v:\n got %s\nwant %s", opacity, text, want)
	}
	mustContain(t, text,
		rect(boxX, boxY, boxW, boxH, boxFill, boxFillOpacity*opacity),
		border(boxX, boxY, boxW, boxH, boxLine, opacity),
		textAt(7, leftX, codeY, 68, textBright, fade(opacity), "340 957"))
	if next != frameRedraw {
		t.Errorf("mid-fade next = %v, want frame rate", next)
	}

	now = reveal.Add(pairingFade)
	text, next = sc.at(now)
	if want := pairingBox("340 957", "http://10.0.0.5:8089", "", expires.Sub(now), 1); text != want {
		t.Errorf("after the fade the frame is not the full box:\n got %s\nwant %s", text, want)
	}
	mustLack(t, text, `\alpha`)
	if next <= frameRedraw || next > pairingRedraw {
		t.Errorf("after the fade next = %v, want the idle cadence", next)
	}
}

// Through the card: the standby load hides the box until the intro ends, a
// box first drawn long after the load has no fade, and a new code redraws
// the full box rather than fading it again.
func TestCardPairingFadeOnlyAfterALoad(t *testing.T) {
	srv, _, fp, codes := newPairTestServer(t)
	if err := srv.enterStandby(); err != nil {
		t.Fatal(err)
	}
	if next := srv.card.refresh(); len(overlays(fp)) != 0 {
		t.Fatalf("box drawn during the intro: %v", overlays(fp))
	} else if next < ident.IntroEnd-time.Second || next > ident.IntroEnd {
		t.Errorf("next = %v, want about the intro's %v", next, ident.IntroEnd)
	}

	// Standby has been up long past the intro: the box is simply there.
	setStandbyLoad(srv, ident.FileName, time.Now().Add(-time.Minute))
	srv.card.refresh()
	drawn := overlays(fp)
	if len(drawn) != 1 || strings.Contains(drawn[0], `\alpha`) ||
		!strings.Contains(drawn[0], assAlpha(boxFillOpacity)) {
		t.Fatalf("frames = %v, want one full-strength box", drawn)
	}

	codes.Rotate()
	next := srv.card.refresh()
	drawn = overlays(fp)
	code, _ := codes.Current()
	if len(drawn) != 2 || !strings.Contains(drawn[1], pairing.Format(code)) || strings.Contains(drawn[1], `\alpha`) {
		t.Fatalf("after a new code, frames = %v, want the full box with the new code", drawn)
	}
	if next <= frameRedraw {
		t.Errorf("next = %v after a new code, want the idle cadence, not a fade", next)
	}
}
