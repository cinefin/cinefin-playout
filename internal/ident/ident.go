// Package ident is Cinefin's System Ident, bundled with the agent: a silent
// 34 second 1080p clip. The first 4 seconds are the intro (the mark warms up and
// the coloured lights unfold); 4 to 34 seconds is a seamless loop of the lights
// orbiting the lockup, which Options holds on. It is the player's standby screen while it is unpaired or has no
// standby spec from Cinefin (see server's standby.go).
//
// The clip is a copy of cinefin/backend/cinefin/assets/system/ident.mp4;
// `make sync-ident` refreshes it.
package ident

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed ident.mp4
var clip []byte

// Options are the per-file mpv options that hold the System Ident on standby:
// it plays its intro once, then loops the 4 to 34 s section. Cinefin builds the
// same string in its standby spec; this copy is for a player that is unpaired
// or has no spec yet.
const Options = "ab-loop-a=4,ab-loop-b=34"

// IntroEnd is when the intro ends and the loop begins: ab-loop-a in Options.
// The pairing box waits for it before fading in over the ident.
var IntroEnd = loopStart(Options)

// loopStart is the ab-loop-a value in a per-file options string, or 0 when
// there is none.
func loopStart(options string) time.Duration {
	for _, opt := range strings.Split(options, ",") {
		if v, ok := strings.CutPrefix(opt, "ab-loop-a="); ok {
			if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
				return time.Duration(secs * float64(time.Second))
			}
		}
	}
	return 0
}

// FileName is the clip's name in the state directory.
const FileName = "ident.mp4"

// File writes the clip to stateDir (mpv needs a path) unless an identical copy
// is already there, and returns its path.
func File(stateDir string) (string, error) {
	path := filepath.Join(stateDir, FileName)
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, clip) {
		return path, nil
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, clip, 0o644); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}
