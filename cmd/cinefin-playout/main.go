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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/discovery"
	"github.com/cinefin/cinefin-playout/internal/hardware"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
	"github.com/cinefin/cinefin-playout/internal/instance"
	"github.com/cinefin/cinefin-playout/internal/pairing"
	"github.com/cinefin/cinefin-playout/internal/player"
	"github.com/cinefin/cinefin-playout/internal/server"
	"github.com/cinefin/cinefin-playout/internal/session"
	"github.com/cinefin/cinefin-playout/internal/state"
	"github.com/cinefin/cinefin-playout/internal/ui"
	"github.com/cinefin/cinefin-playout/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset" {
		os.Exit(runReset(os.Args[2:]))
	}

	cfg := config.Default()
	var (
		name        string
		displays    displayFlag
		showVersion bool
		noUI        bool

		mode         string
		listDisplays bool
		legacyConfig string
	)
	hostname, _ := os.Hostname()
	flag.StringVar(&cfg.Listen, "listen", cfg.Listen, "address to listen on")
	flag.IntVar(&cfg.Port, "port", cfg.Port, "port to listen on")
	flag.StringVar(&cfg.MPVBinary, "mpv", "", "mpv executable (default: the mpv next to this program, then mpv on PATH)")
	flag.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "where the agent keeps its state and the mpv log")
	flag.StringVar(&name, "name", hostname, "the player's name, as shown in Cinefin")
	flag.Var(&displays, "display", "where to show the player; repeatable. A display server (\":0\", \"wayland-1\") on Linux, or a screen: an index (\"1\") or an output name (\"HDMI-A-1\"). The screen applies until Cinefin sets one")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&noUI, "no-ui", false, "never show the tray icon (by default it is shown when there is a desktop session)")
	flag.StringVar(&mode, "mode", "", "screen mode when mpv draws straight to the screen (DRM): WxH, WxH@Hz, preferred, highest or a mode index. Applies until Cinefin sets the launch config; ignored on a desktop")
	flag.BoolVar(&listDisplays, "list-displays", false, "print the connectors and screens with their modes, then exit")
	flag.StringVar(&cfg.MPVConfigFile, "mpv-config", "", "an extra mpv config file, read after the mpv.conf in <state-dir>/mpv")
	// --config is what 0.1 releases took (a config.toml). It is accepted so an
	// old service file still starts, and only warns; hidden from the usage.
	flag.StringVar(&legacyConfig, "config", "", "")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage:\n  cinefin-playout [flags]\n  cinefin-playout reset [--state-dir dir] [--port n]   forget the Cinefin pairing\n\nFlags:\n")
		printDefaults(out, flag.CommandLine, "config")
	}
	flag.Parse()

	if showVersion {
		fmt.Println(version.Version)
		return
	}
	if name == "" {
		name = "Cinefin player"
	}
	if mode != "" {
		if err := hostconfig.ValidateDRMMode(mode); err != nil {
			startupError("--mode: %v", err)
		}
	}
	if cfg.MPVConfigFile != "" {
		fi, err := os.Stat(cfg.MPVConfigFile)
		if err == nil && fi.IsDir() {
			err = errors.New("is a directory, not a file")
		}
		if err != nil {
			startupError("--mpv-config %s: %v", cfg.MPVConfigFile, err)
		}
		if abs, err := filepath.Abs(cfg.MPVConfigFile); err == nil {
			cfg.MPVConfigFile = abs
		}
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)

	// --display: a display server is applied now, before the desktop is
	// detected; a screen is applied to the launch config below.
	var screen string
	for _, v := range displays {
		if session.IsServer(v) {
			if err := session.UseServer(v); err != nil {
				logger.Fatal(err)
			}
		} else {
			screen = v
		}
	}

	if listDisplays {
		// On a Linux desktop the session's screens are listed too (what a
		// --display index counts); without one they would repeat the connectors.
		var screens []hardware.Screen
		if runtime.GOOS != "linux" || session.Desktop() {
			screens = hardware.Screens(context.Background())
		}
		hardware.WriteDisplays(os.Stdout, hardware.DRMConnectorList(""), screens)
		return
	}

	// A 0.1 install: its service passes --config, or its config.toml is still
	// in one of the old places. Neither is read any more.
	cfg.LegacyConfig = legacyConfig
	if cfg.LegacyConfig == "" {
		cfg.LegacyConfig = config.FindLegacyConfig(config.LegacyConfigPaths())
	}
	if cfg.LegacyConfig != "" {
		logger.Printf("%s: %s (remove --config from the service file and delete the file)", cfg.LegacyConfig, config.LegacyConfigNotice)
	}

	// One agent per machine: a second would fight the first for the screen,
	// the mpv socket and the network announcement.
	lock, err := instance.Acquire()
	if errors.Is(err, instance.ErrRunning) {
		logger.Fatalf("%v. Stop it first (Quit in its tray menu, or stop its service).", err)
	} else if err != nil {
		logger.Fatalf("instance lock: %v", err)
	}
	defer lock.Release()

	st, err := state.Open(cfg.StateDir)
	if err != nil {
		logger.Fatalf("state: %v", err)
	}
	id, err := st.ID()
	if err != nil {
		logger.Fatalf("state: %v", err)
	}

	// Log which mpv the agent will drive so a bring-your-own setup can be
	// confirmed at a glance. The resolver prefers --mpv, then a bundled sibling
	// mpv, then one on PATH (see config.ResolveMPVBinary).
	mpvBin := cfg.ResolveMPVBinary()
	if resolved, lerr := exec.LookPath(mpvBin); lerr == nil {
		logger.Printf("mpv: %s", resolved)
	} else {
		logger.Printf("mpv: %q not found yet (%v)", mpvBin, lerr)
	}

	// The player's own mpv config folder, used instead of ~/.config/mpv.
	if err := os.MkdirAll(cfg.MPVConfigDir(), 0o755); err != nil {
		logger.Printf("mpv config: %v", err)
	}
	logger.Printf("mpv config: %s", describeMPVConfig(cfg))

	// Decide desktop vs headless once, up front: on Linux this also adopts the
	// display sockets when the display variables are missing (tmux, ssh), so the
	// tray and mpv find the display.
	if session.Desktop() {
		logger.Printf("session: desktop %s", session.Displays())
	} else {
		logger.Printf("session: no desktop found; mpv will draw straight to the screen (DRM) and there is no tray")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The playback backend: mpv as a supervised subprocess. It owns the transport
	// (Send/inbound frames), the lifecycle (start/stop/supervise) and hardware
	// hotplug recovery, assembling launch options from the launch config, read on
	// each (re)start. An unpaired player launches full screen with the pairing
	// launch config, so the pairing box can be drawn; a paired one uses the
	// config Cinefin set.
	var modeIgnored sync.Once
	backend := player.New(cfg, func() hostconfig.HostConfig {
		hc := st.Launch()
		if !st.Paired() {
			hc = hostconfig.Pairing()
		}
		// The --display screen applies until Cinefin has chosen one.
		if screen != "" && (!st.Paired() || !st.HasLaunch()) {
			if err := hc.PickScreen(screen, hardware.ConnectedDRMConnectors()); err != nil {
				logger.Printf("--display: %v; using the default screen", err)
			}
		}
		// So does the --mode, when mpv draws straight to the screen.
		if mode != "" && (!st.Paired() || !st.HasLaunch()) {
			if err := hc.PickMode(mode); err != nil {
				modeIgnored.Do(func() { logger.Printf("--mode %s: %v", mode, err) })
			}
		}
		return hc
	}, logger)

	codes := pairing.New()
	go codes.Run(ctx)

	// The server owns the single-client /ws/control bridge; building it wires the
	// backend's control channel before backend.Run starts delivering frames.
	srv := server.New(cfg, server.Identity{ID: id, Name: name}, st, codes, backend, logger)

	// Announce the player on the LAN so Cinefin can list it, and keep the
	// paired flag in the announcement current.
	adv := discovery.New(discovery.Info{ID: id, Name: name, Version: version.Version, Port: cfg.Port}, st.Paired, logger)
	go adv.Run(ctx)

	// While unpaired, print the code in a box whenever it changes, for a
	// headless box where the log is the only screen besides the TV.
	logCode := func(code string) {
		if st.Paired() {
			return
		}
		for _, line := range pairing.Banner(name, code, srv.Address()) {
			logger.Print(line)
		}
	}
	codes.OnRotate(logCode)
	srv.OnPairingChange(func(paired bool) {
		adv.Update()
		if !paired {
			codes.Rotate() // a fresh code for the card; OnRotate logs it
		}
	})
	if code, _ := codes.Current(); !st.Paired() {
		logCode(code)
	}

	backend.Run(ctx)
	defer backend.Close()
	backend.Autostart()

	// The tray is shown when someone is logged in at a desktop (session.Desktop)
	// on a UI build; a service, a box with no display, --no-ui or a non-ui build
	// stays headless. When shown, the server runs in the background and the tray
	// takes the main goroutine (systray wants it).
	if !noUI && ui.Available() && session.Desktop() {
		go func() {
			if err := srv.Run(ctx); err != nil {
				logger.Fatalf("server: %v", err)
			}
		}()
		deps := ui.TrayDeps{
			Port:             cfg.Port,
			Address:          srv.Address,
			Paired:           st.Paired,
			Code:             func() string { c, _ := codes.Current(); return pairing.Format(c) },
			PlayerRunning:    func() bool { return backend.Status().Running },
			CinefinConnected: func() bool { return srv.ControlConnected() },
			Start:            func() { backend.Start(true) },
			Stop:             func() { backend.Stop() },
			Restart:          func() { backend.Restart(true) },
			Forget: func() {
				if err := srv.Unpair(); err != nil {
					logger.Printf("unpair: %v", err)
				}
			},
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

// runReset is `cinefin-playout reset`: forget the Cinefin pairing. If the agent
// is running it is asked to unpair (so the TV shows a new code straight away);
// otherwise the state file is reset directly.
func runReset(args []string) int {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	stateDir := fs.String("state-dir", config.DefaultStateDir(), "the agent's state directory")
	port := fs.Int("port", config.Default().Port, "the running agent's port")
	_ = fs.Parse(args)

	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)) + "/ui/unpair"
	client := &http.Client{Timeout: 5 * time.Second}
	if resp, err := client.Post(url, "application/json", nil); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "reset: the running agent answered %s\n", resp.Status)
			return 1
		}
		fmt.Println("Forgot the Cinefin pairing. The player now shows a pairing code.")
		return 0
	}

	st, err := state.Open(*stateDir)
	if err == nil {
		err = st.Reset()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset: %v\n", err)
		return 1
	}
	fmt.Printf("Forgot the Cinefin pairing in %s. The player shows a pairing code when it next starts.\n", st.Path())
	return 0
}

// describeMPVConfig says which mpv config applies, for the startup log.
func describeMPVConfig(cfg config.Config) string {
	s := cfg.MPVConfigDir()
	if cfg.HasMPVConf() {
		s += " (with mpv.conf)"
	} else {
		s += " (no mpv.conf)"
	}
	if cfg.MPVConfigFile != "" {
		s += ", then " + cfg.MPVConfigFile
	}
	return s
}

// startupError reports a bad flag value and exits, like a flag parse error.
func startupError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cinefin-playout: "+format+"\n", args...)
	os.Exit(2)
}

// printDefaults is from.PrintDefaults without the hidden flags.
func printDefaults(out io.Writer, from *flag.FlagSet, hidden ...string) {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(out)
	from.VisitAll(func(f *flag.Flag) {
		if slices.Contains(hidden, f.Name) {
			return
		}
		fs.Var(f.Value, f.Name, f.Usage)
		fs.Lookup(f.Name).DefValue = f.DefValue
	})
	fs.PrintDefaults()
}

// displayFlag collects repeated --display values.
type displayFlag []string

func (d *displayFlag) String() string { return strings.Join(*d, ",") }

func (d *displayFlag) Set(v string) error {
	if v = strings.TrimSpace(v); v == "" {
		return fmt.Errorf("empty value")
	}
	*d = append(*d, v)
	return nil
}
