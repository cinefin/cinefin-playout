// Package config holds the agent's startup options: where it listens, which mpv
// it runs, and where it keeps its state. They come from command-line flags only;
// there is no config file. Everything Cinefin sets (the pairing token and the
// mpv launch config) lives in the agent's state file instead (see package state).
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Config is the resolved startup configuration.
type Config struct {
	Listen    string // address to bind, e.g. "0.0.0.0"
	Port      int    // HTTP/WebSocket port
	MPVBinary string // "" = auto-resolve (see ResolveMPVBinary)
	StateDir  string // agent state: state.json, mpv log
	IPCSocket string // mpv's local JSON-IPC endpoint (unix socket or named pipe)

	// MPVConfigFile is an extra mpv config file (--mpv-config), passed to mpv
	// as --include. "" = none. The player's own config folder is MPVConfigDir.
	MPVConfigFile string
	// LegacyConfig is set when the agent was started the 0.1 way: with
	// --config, or with a config.toml at one of the old default paths. It holds
	// that path. Those files are no longer read; see LegacyConfigNotice.
	LegacyConfig string
}

// Default returns the shipped defaults.
func Default() Config {
	return Config{
		Listen:    "0.0.0.0",
		Port:      8089,
		StateDir:  DefaultStateDir(),
		IPCSocket: defaultIPCSocket(),
	}
}

// ResolveMPVBinary returns the mpv executable the agent should launch. An
// explicit --mpv always wins. Otherwise the agent prefers an "mpv" sitting next
// to its own executable (the release archive ships one), and finally falls back
// to the bare "mpv" name resolved on PATH.
func (c Config) ResolveMPVBinary() string {
	if c.MPVBinary != "" {
		return c.MPVBinary
	}
	if exe, err := os.Executable(); err == nil {
		name := "mpv"
		if runtime.GOOS == "windows" {
			name = "mpv.exe"
		}
		sibling := filepath.Join(filepath.Dir(exe), name)
		if fi, serr := os.Stat(sibling); serr == nil && !fi.IsDir() {
			return sibling
		}
	}
	return "mpv"
}

// MPVConfigDir is the player's own mpv config folder, <state-dir>/mpv. mpv is
// pointed at it with --config-dir, so it reads an mpv.conf there and ignores
// the personal ~/.config/mpv of whichever user runs the agent.
func (c Config) MPVConfigDir() string {
	return filepath.Join(c.StateDir, "mpv")
}

// MPVConfPath is the mpv.conf path inside MPVConfigDir.
func (c Config) MPVConfPath() string {
	return filepath.Join(c.MPVConfigDir(), "mpv.conf")
}

// HasMPVConf reports whether there is an mpv.conf in MPVConfigDir.
func (c Config) HasMPVConf() bool {
	fi, err := os.Stat(c.MPVConfPath())
	return err == nil && !fi.IsDir()
}

// LegacyConfigNotice is what the agent says when it finds a 0.1 config.toml.
const LegacyConfigNotice = "config.toml is no longer used; pair this player again in Settings › Playout"

// LegacyConfigPaths are the places 0.1 releases read config.toml from.
func LegacyConfigPaths() []string {
	paths := []string{"config.toml"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "cinefin-playout", "config.toml"))
	}
	return append(paths, "/etc/cinefin-playout/config.toml")
}

// FindLegacyConfig returns the first of paths that is an existing file, or "".
func FindLegacyConfig(paths []string) string {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// DefaultStateDir is the per-user state directory: %LOCALAPPDATA%\cinefin-playout
// on Windows, ~/.local/state/cinefin-playout elsewhere.
func DefaultStateDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "cinefin-playout")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "cinefin-playout")
	}
	return filepath.Join(os.TempDir(), "cinefin-playout")
}

// defaultIPCSocket is mpv's IPC endpoint: a named pipe on Windows (mpv and the
// agent must agree on the full \\.\pipe\ path), a unix socket elsewhere.
func defaultIPCSocket() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\cinefin-playout-mpv`
	}
	return "/tmp/mpvsocket"
}
