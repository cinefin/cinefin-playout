package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

// The 0.1 --config flag is accepted (so an old service file still starts)
// but kept out of the usage.
func TestHiddenConfigFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var legacy string
	fs.Int("port", 8089, "port to listen on")
	fs.Bool("no-ui", false, "never show the tray icon")
	fs.StringVar(&legacy, "config", "", "")
	if err := fs.Parse([]string{"--config", "/etc/cinefin-playout/config.toml", "--no-ui"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if legacy != "/etc/cinefin-playout/config.toml" {
		t.Errorf("--config = %q", legacy)
	}

	var b strings.Builder
	printDefaults(&b, fs, "config")
	out := b.String()
	if strings.Contains(out, "config") {
		t.Errorf("usage shows the hidden flag:\n%s", out)
	}
	if !strings.Contains(out, "-port int") || !strings.Contains(out, `(default 8089)`) || !strings.Contains(out, "-no-ui\n") {
		t.Errorf("usage lost a flag or its default:\n%s", out)
	}
}
