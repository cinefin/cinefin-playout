package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"git.kef2.net/micky/cinefin-playout/internal/hostconfig"
)

// WriteLaunchConfig persists the launch config (autostart + graphics + audio)
// into the TOML file at path. It decodes whatever is already on disk, overwrites
// just the [mpv].autostart / [mpv.graphics] / [mpv.audio] tables from c, and
// re-encodes the file. Everything else in the file — [server], [agent] and the
// other [mpv] keys — is preserved by value; comments are not (BurntSushi/toml
// does not round-trip them). A missing file (or parent directory) is created.
// This is what the idle-media API and PUT /hostconfig call to save; config.toml
// is the single source of truth, so the agent picks the change up on the next
// player (re)start.
func WriteLaunchConfig(path string, c Config) error {
	var tf tomlFile
	if _, err := os.Stat(path); err == nil {
		if _, derr := toml.DecodeFile(path, &tf); derr != nil {
			return derr
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	autostart := c.Autostart
	tf.MPV.Autostart = &autostart
	tf.MPV.Graphics = graphicsTOMLOf(c.Graphics)
	tf.MPV.Audio = audioTOMLOf(c.Audio)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(tf); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// graphicsTOMLOf renders a hostconfig.Graphics as a fully-populated [mpv.graphics]
// table (every key emitted explicitly, so the table Cinefin owns is regenerated
// whole).
func graphicsTOMLOf(g hostconfig.Graphics) *graphicsTOML {
	return &graphicsTOML{
		Mode:           &g.Mode,
		VO:             &g.VO,
		GPUAPI:         &g.GPUAPI,
		GPUContext:     &g.GPUContext,
		HWDec:          &g.HWDec,
		Screen:         &g.Screen,
		DRMConnector:   &g.DRMConnector,
		DRMMode:        &g.DRMMode,
		Fullscreen:     &g.Fullscreen,
		HDRPassthrough: &g.HDRPassthrough,
		OSC:            &g.OSC,
		Display:        &g.Display,
		IdleMedia:      &g.IdleMedia,
	}
}

// audioTOMLOf renders a hostconfig.Audio as a fully-populated [mpv.audio] table.
func audioTOMLOf(a hostconfig.Audio) *audioTOML {
	spdif := a.SPDIFPassthrough
	if spdif == nil {
		spdif = []string{}
	}
	return &audioTOML{
		Device:           &a.Device,
		Channels:         &a.Channels,
		SPDIFPassthrough: spdif,
		MaxVolume:        &a.MaxVolume,
	}
}
