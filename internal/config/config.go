// Package config loads the playout agent's own configuration.
//
// This configures the agent itself: where it listens ([server]), its auth token,
// where its state lives ([agent]), and — all under one [mpv] table — how it
// reaches mpv (binary + local IPC endpoint), whether to autostart it, and the
// video/audio *launch options* in the [mpv.graphics]/[mpv.audio] subtables. The
// launch options are edited by hand here or remotely by Cinefin via PUT
// /hostconfig, which rewrites just those subtables. See docs/ARCHITECTURE.md,
// "Config ownership". The config file, its default search paths and the
// CINEFIN_PLAYOUT_CONFIG environment override are all optional: a host with no
// config file at all runs on defaults with an auto-generated token.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// EnvConfigPath is the environment variable that overrides the config search.
const EnvConfigPath = "CINEFIN_PLAYOUT_CONFIG"

// Config is the resolved agent configuration.
//
// Everything about mpv lives under one [mpv] table: how the agent reaches the
// player (binary/ipc_socket/extra_env), whether to autostart it, and the video/
// audio launch options in the [mpv.graphics]/[mpv.audio] subtables. The launch
// options are the embedded hostconfig.HostConfig (autostart + graphics + audio),
// so there is exactly one field for each — HostConfig is what GET/PUT /hostconfig
// exchange with Cinefin.
type Config struct {
	// [server]
	Host  string
	Port  int
	Token string

	// [mpv]
	MPVBinary string
	IPCSocket string
	ExtraEnv  map[string]string

	// [mpv].autostart + [mpv.graphics]/[mpv.audio] — the mpv launch config,
	// embedded so cfg.Autostart / cfg.Graphics / cfg.Audio read through to it and
	// cfg.HostConfig is the whole launch config in one value.
	hostconfig.HostConfig

	// [agent]
	StateDir string

	// Path is the config file this was resolved from ("" if it ran on defaults
	// with no file present). It is where launch-config edits are written back.
	Path string
}

// Default returns a Config populated with the shipped defaults. The launch
// config (autostart + graphics + audio) defaults to the per-OS preset.
func Default() Config {
	return Config{
		Host:       "0.0.0.0",
		Port:       8089,
		Token:      "",
		MPVBinary:  "", // auto-resolved by ResolveMPVBinary (bundled sibling, then PATH)
		IPCSocket:  "/tmp/mpvsocket",
		ExtraEnv:   map[string]string{},
		StateDir:   defaultStateDir(),
		HostConfig: hostconfig.Default(),
	}
}

// ResolveMPVBinary returns the mpv executable the agent should launch. An
// explicitly configured [mpv].binary always wins. Otherwise the agent prefers an
// "mpv" sitting next to its own executable (drop one there to pin a version
// without editing config), and finally falls back to the bare "mpv" name
// resolved on PATH — so a host with mpv installed needs no configuration.
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

// WriteTarget is the file the agent writes launch-config changes to: the file
// this config was loaded from, or the per-user default when it ran on defaults
// with no file present.
func (c Config) WriteTarget() string {
	if c.Path != "" {
		return c.Path
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "cinefin-playout", "config.toml")
	}
	return "config.toml"
}

// tomlFile mirrors the on-disk TOML layout. Every field is a pointer (or a
// pointer-holding sub-struct) so an absent key leaves the corresponding default
// untouched on load rather than zeroing it. The same struct is re-encoded by
// WriteLaunchConfig, so the preserved-by-value keys carry omitempty (an absent
// key stays absent) and the [mpv] scalar keys are declared before its sub-tables
// (graphics/audio/extra_env) as the TOML encoder requires.
type tomlFile struct {
	Server struct {
		Host  *string `toml:"host,omitempty"`
		Port  *int    `toml:"port,omitempty"`
		Token *string `toml:"token,omitempty"`
	} `toml:"server"`
	MPV struct {
		Binary    *string           `toml:"binary,omitempty"`
		IPCSocket *string           `toml:"ipc_socket,omitempty"`
		Autostart *bool             `toml:"autostart"`
		ExtraEnv  map[string]string `toml:"extra_env,omitempty"`
		Graphics  *graphicsTOML     `toml:"graphics,omitempty"`
		Audio     *audioTOML        `toml:"audio,omitempty"`
	} `toml:"mpv"`
	Agent struct {
		StateDir *string `toml:"state_dir,omitempty"`
	} `toml:"agent"`
}

// graphicsTOML mirrors the [mpv.graphics] table onto hostconfig.Graphics.
type graphicsTOML struct {
	Mode           *string `toml:"mode"`
	VO             *string `toml:"vo"`
	GPUAPI         *string `toml:"gpu_api"`
	GPUContext     *string `toml:"gpu_context"`
	HWDec          *string `toml:"hwdec"`
	Screen         *int    `toml:"screen"`
	DRMConnector   *string `toml:"drm_connector"`
	DRMMode        *string `toml:"drm_mode"`
	Fullscreen     *bool   `toml:"fullscreen"`
	HDRPassthrough *bool   `toml:"hdr_passthrough"`
	OSC            *bool   `toml:"osc"`
	Display        *string `toml:"display"`
	IdleMedia      *string `toml:"idle_media"`
}

// audioTOML mirrors the [mpv.audio] table onto hostconfig.Audio.
type audioTOML struct {
	Device           *string  `toml:"device"`
	Channels         *string  `toml:"channels"`
	SPDIFPassthrough []string `toml:"spdif_passthrough"`
	MaxVolume        *int     `toml:"max_volume"`
}

// DefaultConfigPaths returns the config file search order, lowest priority last.
func DefaultConfigPaths() []string {
	paths := []string{"config.toml"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "cinefin-playout", "config.toml"))
	}
	paths = append(paths, "/etc/cinefin-playout/config.toml")
	return paths
}

// Load resolves the config. If path is non-empty it is required (missing → error).
// Otherwise the CINEFIN_PLAYOUT_CONFIG env var and the default search paths are
// tried in order; a missing file yields all-defaults.
func Load(path string) (Config, error) {
	cfg := Default()

	var candidates []string
	if path != "" {
		candidates = append(candidates, path)
	}
	if env := os.Getenv(EnvConfigPath); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, DefaultConfigPaths()...)

	var chosen string
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			chosen = c
			break
		}
		if path != "" && c == path {
			return cfg, fmt.Errorf("config file not found: %s", path)
		}
	}

	if chosen == "" {
		return cfg, nil
	}
	cfg.Path = chosen

	var tf tomlFile
	if _, err := toml.DecodeFile(chosen, &tf); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", chosen, err)
	}

	if tf.Server.Host != nil {
		cfg.Host = *tf.Server.Host
	}
	if tf.Server.Port != nil {
		cfg.Port = *tf.Server.Port
	}
	if tf.Server.Token != nil {
		cfg.Token = *tf.Server.Token
	}
	if tf.MPV.Binary != nil {
		cfg.MPVBinary = *tf.MPV.Binary
	}
	if tf.MPV.IPCSocket != nil {
		cfg.IPCSocket = *tf.MPV.IPCSocket
	}
	if tf.MPV.ExtraEnv != nil {
		cfg.ExtraEnv = tf.MPV.ExtraEnv
	}
	if tf.MPV.Autostart != nil {
		cfg.Autostart = *tf.MPV.Autostart
	}
	if tf.Agent.StateDir != nil {
		cfg.StateDir = expandHome(*tf.Agent.StateDir)
	}
	applyGraphics(&cfg.Graphics, tf.MPV.Graphics)
	applyAudio(&cfg.Audio, tf.MPV.Audio)

	return cfg, nil
}

// applyGraphics overlays the [mpv.graphics] table onto g, leaving preset defaults
// in place for any key the file omits.
func applyGraphics(g *hostconfig.Graphics, t *graphicsTOML) {
	if t == nil {
		return
	}
	if t.Mode != nil {
		g.Mode = *t.Mode
	}
	if t.VO != nil {
		g.VO = *t.VO
	}
	if t.GPUAPI != nil {
		g.GPUAPI = *t.GPUAPI
	}
	if t.GPUContext != nil {
		g.GPUContext = *t.GPUContext
	}
	if t.HWDec != nil {
		g.HWDec = *t.HWDec
	}
	if t.Screen != nil {
		g.Screen = *t.Screen
	}
	if t.DRMConnector != nil {
		g.DRMConnector = *t.DRMConnector
	}
	if t.DRMMode != nil {
		g.DRMMode = *t.DRMMode
	}
	if t.Fullscreen != nil {
		g.Fullscreen = *t.Fullscreen
	}
	if t.HDRPassthrough != nil {
		g.HDRPassthrough = *t.HDRPassthrough
	}
	if t.OSC != nil {
		g.OSC = *t.OSC
	}
	if t.Display != nil {
		g.Display = *t.Display
	}
	if t.IdleMedia != nil {
		g.IdleMedia = *t.IdleMedia
	}
}

// applyAudio overlays the [mpv.audio] table onto a.
func applyAudio(a *hostconfig.Audio, t *audioTOML) {
	if t == nil {
		return
	}
	if t.Device != nil {
		a.Device = *t.Device
	}
	if t.Channels != nil {
		a.Channels = *t.Channels
	}
	if t.SPDIFPassthrough != nil {
		a.SPDIFPassthrough = t.SPDIFPassthrough
	}
	if t.MaxVolume != nil {
		a.MaxVolume = *t.MaxVolume
	}
}

// TokenPath is where an auto-generated API token is persisted inside stateDir.
func TokenPath(stateDir string) string {
	return filepath.Join(stateDir, "token")
}

// EnsureToken makes sure cfg.Token is set. An explicit token (from the config
// file) is left untouched. Otherwise the agent looks for a previously generated
// token at TokenPath(stateDir); failing that it generates a fresh random one,
// persists it (0600) and returns generated=true so the caller can log it once.
// This lets a host run with no configured secret yet still be authenticated.
func EnsureToken(cfg *Config) (generated bool, err error) {
	if cfg.Token != "" {
		return false, nil
	}
	path := TokenPath(cfg.StateDir)
	if data, rerr := os.ReadFile(path); rerr == nil {
		if tok := strings.TrimSpace(string(data)); tok != "" {
			cfg.Token = tok
			return false, nil
		}
	}
	tok, gerr := randomToken(24)
	if gerr != nil {
		return false, gerr
	}
	if merr := os.MkdirAll(cfg.StateDir, 0o755); merr != nil {
		return false, fmt.Errorf("mkdir %s: %w", cfg.StateDir, merr)
	}
	if werr := os.WriteFile(path, []byte(tok+"\n"), 0o600); werr != nil {
		return false, fmt.Errorf("write %s: %w", path, werr)
	}
	cfg.Token = tok
	return true, nil
}

// randomToken returns a URL-safe base64 token carrying nbytes of entropy.
func randomToken(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func defaultStateDir() string {
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

func expandHome(p string) string {
	if p == "~" || len(p) >= 2 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
