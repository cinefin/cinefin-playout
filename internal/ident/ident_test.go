package ident

import (
	"bytes"
	"os"
	"testing"
	"time"
)

// IntroEnd is read from Options, so the two cannot disagree.
func TestIntroEndIsTheLoopStart(t *testing.T) {
	if IntroEnd != 4*time.Second {
		t.Errorf("IntroEnd = %v, want 4s (ab-loop-a in %q)", IntroEnd, Options)
	}
	for in, want := range map[string]time.Duration{
		"ab-loop-a=2.5,ab-loop-b=10": 2500 * time.Millisecond,
		"ab-loop-b=10,ab-loop-a=6":   6 * time.Second,
		"end=5,keep-open=always":     0,
		"ab-loop-a=no":               0,
		"":                           0,
	} {
		if got := loopStart(in); got != want {
			t.Errorf("loopStart(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFileWritesTheClipOnce(t *testing.T) {
	dir := t.TempDir()
	path, err := File(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, clip) || len(clip) == 0 {
		t.Fatalf("written clip differs (%d bytes, err %v)", len(got), err)
	}
	fi, _ := os.Stat(path)
	if _, err := File(dir); err != nil {
		t.Fatal(err)
	}
	if fi2, _ := os.Stat(path); !fi2.ModTime().Equal(fi.ModTime()) {
		t.Error("an identical copy was rewritten")
	}
}
