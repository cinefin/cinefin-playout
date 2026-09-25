// Command cinefin-playout is the Go playout agent: it owns the mpv player on
// the projector host and relays mpv's JSON-IPC over an authenticated WebSocket.
//
// It relays control over /ws/control, owns the host graphics/audio config and
// hardware enumeration, and drives mpv as a supervised subprocess — see
// docs/ARCHITECTURE.md. A -tags ui build also shows a system tray whose "control
// panel" item opens the agent's loopback /ui page in the default browser.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
	"github.com/cinefin/cinefin-playout/internal/player"
	"github.com/cinefin/cinefin-playout/internal/server"
	"github.com/cinefin/cinefin-playout/internal/ui"
	"github.com/cinefin/cinefin-playout/internal/version"
)

func main() {
	var (
		configPath  string
		showVersion bool
		noUI        bool
	)
	flag.StringVar(&configPath, "config", "", "path to config.toml")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&noUI, "no-ui", false, "run headless — do not show the desktop tray (which is shown by default on a -tags ui build)")
	flag.Parse()

	if showVersion {
		fmt.Println(version.Version)
		return
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatalf("config: %v", err)
	}

	// Auto-generate an API token on first run if none was configured, so a host
	// is authenticated out of the box. Log it once for the operator to paste
	// into the Cinefin playout host record.
	if generated, terr := config.EnsureToken(&cfg); terr != nil {
		logger.Fatalf("token: %v", terr)
	} else if generated {
		logger.Printf("generated a new API token (saved to %s):", config.TokenPath(cfg.StateDir))
		logger.Printf("    %s", cfg.Token)
		logger.Printf("set this as the bearer token on the Cinefin playout host record")
	}

	// Log which mpv the agent will drive so a bring-your-own setup can be
	// confirmed at a glance. The resolver prefers an explicit [mpv].binary, then a
	// bundled sibling mpv, then one on PATH (see config.ResolveMPVBinary).
	mpvBin := cfg.ResolveMPVBinary()
	if resolved, lerr := exec.LookPath(mpvBin); lerr == nil {
		logger.Printf("mpv: %s", resolved)
	} else {
		logger.Printf("mpv: %q not found yet (%v)", mpvBin, lerr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The playback backend: mpv as a supervised subprocess. It owns the transport
	// (Send/inbound frames), the lifecycle (start/stop/supervise) and hardware
	// hotplug recovery, assembling launch options from the launch config. The
	// provider re-reads config.toml on each (re)start so a config edit takes
	// effect on the next player restart without restarting the agent.
	backend := player.New(cfg, func() hostconfig.HostConfig {
		reloaded, err := config.Load(cfg.Path)
		if err != nil {
			logger.Printf("config reload (using in-memory config): %v", err)
			return cfg.HostConfig
		}
		return reloaded.HostConfig
	}, logger)

	// The server owns the single-client /ws/control bridge; building it wires the
	// backend's control channel before backend.Run starts delivering frames.
	srv := server.New(cfg, backend, logger)
	backend.Run(ctx)
	defer backend.Close()
	backend.Autostart()

	// The desktop tray is shown by default on a UI build; --no-ui (or a non-ui
	// build, e.g. the headless service) stays headless. When shown, the server
	// runs in the background and the tray takes the main goroutine (systray wants
	// it).
	if !noUI && ui.Available() {
		go func() {
			if err := srv.Run(ctx); err != nil {
				logger.Fatalf("server: %v", err)
			}
		}()
		deps := ui.TrayDeps{
			Port:             cfg.Port,
			Token:            cfg.Token,
			PlayerRunning:    func() bool { return backend.Status().Running },
			CinefinConnected: func() bool { return srv.ControlConnected() },
			Start:            func() { backend.Start(true) },
			Stop:             func() { backend.Stop() },
			Restart:          func() { backend.Restart(true) },
		}
		if err := ui.RunTray(ctx, deps); err != nil {
			logger.Printf("ui: %v", err)
		}
		return
	}

	if err := srv.Run(ctx); err != nil {
		logger.Fatalf("server: %v", err)
	}
}
