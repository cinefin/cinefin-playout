# cinefin-playout

This is the playout agent for [Cinefin](../cinefin). Install it on the machine
wired to your projector or screen. It runs mpv, plays your programmes full screen
(trailers, bumpers, idents, and the feature), and takes its orders from Cinefin
over the network.

Cinefin talks to mpv through one authenticated WebSocket, so the mpv socket never
leaves the host. The agent runs on Linux, Windows, and macOS, on a desktop you sit
at or a headless booth box.

## Quick start

1. **Download** the archive for your machine from the
   [Releases page](https://github.com/cinefin/cinefin-playout/releases). Most
   archives include mpv, so there is nothing else to install. Pick `linux-amd64`
   (`.tar.gz`). Windows builds currently ship on the pre-release marked `edge`
   while they settle. On macOS, build the app yourself (see
   [Build from source](#build-from-source)).

2. **Unzip it** anywhere, and keep the `mpv` file next to `cinefin-playout`.

3. **Run it.**
   - Windows: double-click `cinefin-playout.exe`.
   - Linux: run `./cinefin-playout`.
   - macOS: open `Cinefin Playout.app`. It is unsigned, so the first time,
     right-click it and choose Open.

   The screen shows the Cinefin mark with a pairing box below it: a 6-digit
   code and the player's address. The code is also in the tray menu.

4. **Pair it.** In Cinefin, go to Settings > Playout and enter the code from the
   screen. If the player is not listed (for example, Cinefin runs in Docker),
   add it by the address shown on the screen and use the same code.

That is the whole setup. Cinefin now plays through this machine, and you set its
screen and audio from the same page. To pair the player with a different
Cinefin, remove it there, or choose "Forget Cinefin" in the tray, or run
`cinefin-playout reset`.

## Run as a service (booth box)

For a box that should start on boot with no one logged in, run the same binary
under a service manager. With no desktop session it runs without a tray icon on
its own; the example unit also passes `--no-ui`, because it sets `DISPLAY` for
boxes that run a bare Xorg.

systemd:

```bash
sudo cp cinefin-playout mpv /usr/local/bin/     # include mpv, or rely on a system one
sudo cp etc/cinefin-playout.service.example /etc/systemd/system/cinefin-playout.service
sudoedit /etc/systemd/system/cinefin-playout.service   # set your username
sudo systemctl daemon-reload
sudo systemctl enable --now cinefin-playout.service
```

The agent runs mpv itself, so disable any separate `cinefin-mpv.service`. On
Windows, register it with `sc.exe create` or Task Scheduler.

Until it is paired, the agent prints the pairing code to the log each time it
changes (`journalctl -u cinefin-playout -f`), as well as showing it on the
screen. Pairing and the settings Cinefin sends are kept in the agent's state
file, `state.json` in the state directory (`~/.local/state/cinefin-playout` by
default). The agent has no config file of its own. A few startup options are
flags:

| Flag | Default | Purpose |
| ---- | ------- | ------- |
| `--listen` | `0.0.0.0` | Address to listen on. |
| `--port` | `8089` | Port to listen on. |
| `--mpv` | bundled, then `PATH` | The mpv executable. |
| `--state-dir` | per-user state directory | Where the state file and mpv log live. |
| `--name` | the hostname | The player's name, as shown in Cinefin. |
| `--display` | detected | Where to show the player; can be given twice. On Linux, a display server (`:0`, `wayland-1`) when the agent cannot find yours (for example over ssh). Or a screen: an index (`1`) or an output name (`HDMI-A-1`), used until you pick a screen in Cinefin. |
| `--mode` | the screen's preferred mode | The screen mode when mpv draws straight to the screen (DRM): `WxH`, `WxH@Hz` (for example `3840x2160@23.976`), `preferred`, `highest`, or a mode index as listed by `mpv --drm-mode=help`. Like the `--display` screen, it applies until Cinefin sets the launch config. It has no effect on a desktop, where the session sets the resolution; the agent logs that it was ignored. |
| `--mpv-config` | none | An extra mpv config file, such as `/etc/cinefin-playout/mpv.conf`. See [mpv's own config](#mpvs-own-config). |
| `--no-ui` | off | Never show the tray icon. By default it appears when someone is logged in at a desktop. |

`cinefin-playout --list-displays` prints each connector with its status and
the modes the screen on it offers, and on a desktop the screens the session
reports, then exits. Use it to find the names and modes for `--display` and
`--mode`:

```
Connectors (--display NAME; --mode WxH[@Hz] when mpv draws straight to the screen):
  DP-1      disconnected
  HDMI-A-1  connected
      3840x2160 (preferred), 2560x1440, 1920x1080, 1280x720
```

The kernel does not list refresh rates, so each resolution appears once; `mpv
--drm-mode=help` lists every mode with its rate and index.

Only one agent runs per machine: a second one exits with "already running".

`cinefin-playout reset` forgets the pairing. If the agent is running it shows a
new code straight away; pass `--state-dir` and `--port` if you changed them.

## Screen and audio

The screen, resolution, video output, HDR, audio device, and channels are set from
Cinefin's Playout page, which lists the real devices on the box. The agent keeps a
copy in its state file, so the box still boots, starts mpv, and goes to standby
even when Cinefin is offline. Changes take effect on the next player restart.

### Running without a desktop (DRM/KMS)

mpv can draw straight to the screen through the kernel, with no Xorg or Wayland. In
the graphics config set `mode: "drm"` and one of:

- `gpu_api: "vulkan"` with `gpu_context: "displayvk"`: best on NVIDIA.
- `gpu_context: "drm"`: the GBM/EGL route for Intel and AMD, or NVIDIA with recent
  drivers.

Set `drm_connector` (for example `HDMI-A-1`) if the box has more than one output.
Before Cinefin has set a launch config, `--display HDMI-A-1` and `--mode
3840x2160@60` do the same from the command line; `--list-displays` shows the
connectors and their modes. On NVIDIA, enable DRM KMS:

```
# /etc/modprobe.d/nvidia-drm.conf
options nvidia-drm modeset=1 fbdev=1
```

Rebuild the initramfs and reboot, then confirm that
`cat /sys/module/nvidia_drm/parameters/modeset` prints `Y`. Give the agent's user
DRM access (`SupplementaryGroups=video render`) and make sure no display manager is
holding the GPU. For audio with no desktop, run a user PipeWire or PulseAudio
session, or point the audio device at an ALSA sink such as
`alsa/hdmi:CARD=NVidia,DEV=0` (from `GET /hardware`).

## Standby

When no programme is playing, the player is on standby: it plays the cinema's
ident once, then holds it (the System Ident loops after its intro; a cinema's
own ident freezes on a frame). Cinefin sends the ident and how to hold it once;
the player downloads it and keeps it in its state directory, so standby looks
the same when Cinefin is unreachable. Until then, or while unpaired, the player
uses the System Ident built into it.

The player goes to standby when mpv starts, when Cinefin asks, and when Cinefin
has been unreachable for 30 seconds with nothing loaded. If Cinefin goes away
during a programme's command hold, after 30 seconds the player ends the hold
and plays the rest of the programme by itself.

When Cinefin turns on the status line for the player, standby shows one line
along the bottom of the screen: the cinema's name on the left, and on the
right whether Cinefin is connected ("Ready", "Connecting to Cinefin" or
"Can't reach Cinefin"), the player's name and Cinefin's address. Without the
status line, a paired player on standby that has not heard from Cinefin for
30 seconds says so on screen ("Can't reach Cinefin at <address>"), and shows
"Connected to Cinefin again" briefly once Cinefin is back. Nothing is drawn
over a programme.

## mpv's own config

The player keeps its own mpv config folder, `mpv` in the state directory
(`~/.local/state/cinefin-playout/mpv` by default), and starts mpv with
`--config-dir` pointing at it. mpv therefore does not read the personal
`~/.config/mpv` (mpv.conf, input.conf, scripts) of the user the agent runs as,
nor `/etc/mpv`. The agent creates the folder at startup. To set mpv options for
the player, put an `mpv.conf` there, for example:

```
# ~/.local/state/cinefin-playout/mpv/mpv.conf
sub-font-size=44
demuxer-max-bytes=512MiB
```

To keep the file somewhere else, pass it with `--mpv-config
/etc/cinefin-playout/mpv.conf`; the agent refuses to start if the file does not
exist. It is read after the folder's `mpv.conf`.

The order is: `mpv.conf` in the player's folder, then the `--mpv-config` file,
then the options the agent puts on mpv's command line. So an option the agent
sets always wins: the IPC socket, `idle` and `force-window` that Cinefin relies
on, and everything from the launch config Cinefin sets (video output, screen,
HDR, audio device, channels, maximum volume, on-screen controller). Use the
config files for what the launch config does not cover.

mpv starts with `--load-scripts=no`, so it loads no user scripts, including
ones in the player's folder. mpv's built-in scripts are not affected: the
on-screen controller still follows the launch config's setting, and the
console and stats overlay still load.

The agent logs the config at startup (`mpv config: <folder> (with mpv.conf),
then <file>`), and `GET /status` reports it as `"mpv_config": {"dir": ...,
"mpv_conf": true, "file": ...}`.

## Upgrading from 0.1

Releases up to 0.1.1 read a `config.toml`. It is no longer used: pairing
replaces it, and the launch config now comes from Cinefin. The new agent still
starts with `--config` so an old service keeps running, but it logs
`config.toml is no longer used; pair this player again in Settings › Playout`,
as it does when a `config.toml` is left in `./`, `~/.config/cinefin-playout/`
or `/etc/cinefin-playout/`. The pairing box on the screen says the same in a
line under the code's countdown. To finish the upgrade:

1. Remove `--config /etc/cinefin-playout/config.toml` from `ExecStart` in the
   service file (compare with `etc/cinefin-playout.service.example`), then run
   `sudo systemctl daemon-reload`.
2. Delete `config.toml`.
3. mpv no longer reads `~/.config/mpv`. Copy any lines you still want from
   `~/.config/mpv/mpv.conf` into the player's `mpv.conf` (see
   [mpv's own config](#mpvs-own-config)).
4. Restart the service and pair the player again in Cinefin's Settings >
   Playout with the code on the screen or in the log.

## Screen and sound check

When a player is added, Cinefin asks the user to check the picture and the
sound. `POST /testcard` puts a test card over the whole screen: corner marks at
the edges, the safe area, the player's name and output, colour bars and a grey
ramp. If a corner mark is missing or cut off, the picture is cropped.
`POST /testsound` plays a 440 Hz tone on the left channel for 1.5 seconds, then
on the right, through the configured audio device; the Left and Right boxes on
the test card light up in turn. The test card turns itself off after 5 minutes.

## Bringing your own mpv

Every standard archive includes a working mpv, so most people can skip this. If you
would rather use your own, the agent looks for mpv in this order:

1. The `--mpv` flag.
2. An `mpv` (or `mpv.exe`) next to the agent binary. This is the bundled one.
3. `mpv` on your `PATH`.

It logs the one it picked at startup (for example `mpv: /usr/bin/mpv`). To use your
own, download the `-nompv` archive, delete the bundled `mpv` file, or pass
`--mpv`.

## How it works

The agent owns mpv on the host. Cinefin sends mpv's own JSON-IPC commands over one
WebSocket and gets mpv's replies and events back. If mpv crashes, the agent
relaunches it with backoff; an explicit stop keeps it stopped. On Linux DRM, a
projector power-cycle re-modesets without a restart.

The agent announces itself on the local network over mDNS
(`_cinefin-playout._tcp`), which is how Cinefin lists it without an address.

For the full design, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## API

Everything except `/health`, `/pair` and the loopback-only `/local/unpair` needs an
`Authorization: Bearer <token>` header. Cinefin gets the token by pairing.
Everything except `/health` also needs a `Cinefin-Protocol` header within the
range the player serves; otherwise it answers `426` and says whether Cinefin or
the player needs updating (see `docs/ARCHITECTURE.md`, Protocol).
Traffic is plain HTTP for a trusted LAN. Do not expose it to the internet; put a
reverse proxy in front if you need TLS.

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET  | `/health` | Liveness, version, os/arch, id, name, paired, protocol range (no auth). |
| POST | `/pair` | Exchange the on-screen code (`{"code": "482913"}`) for the token (no auth). |
| POST | `/unpair` | Forget the pairing; the player shows a new code. |
| GET  | `/status` | Agent, mpv, control-link, standby, test card and test sound status. |
| WS   | `/ws/control` | Duplex mpv JSON-IPC. |
| GET  | `/hostconfig` | Current graphics, audio, and autostart config. |
| PUT  | `/hostconfig` | Replace the launch config. Returns `{restart_required}`. |
| PUT  | `/standby` | Set the standby spec (the cinema's ident and how to hold it). |
| POST | `/standby` | Go to standby now. |
| POST | `/testcard` | Show or hide the test card (`{"on": true}`). |
| POST | `/testsound` | Play the left/right test tone (on standby or with the test card on). |
| GET  | `/hardware` | Audio devices, DRM connectors, screens, and mpv features. |
| POST | `/mpv/start`, `/mpv/stop`, `/mpv/restart` | Player lifecycle. |
| POST | `/local/unpair` | Forget the pairing, for `cinefin-playout reset` (loopback only, no token). |

## Build from source

You need Go (see `go.mod` for the version). The common tasks are in the Makefile:

```bash
make build     # build the agent for this machine
make check     # gofmt, vet, tests, and a cross-compile of every target
```

`make build` includes the system tray. It is pure Go on Linux (D-Bus) and Windows
(GDI), so it cross-compiles with CGO off. The tray needs cgo only on macOS (Cocoa):

```bash
make macos   # builds a self-contained "Cinefin Playout.app" into dist/
```

Releases are built by CI. Which targets ship a bundled mpv is set by
`mpv-bundle.json`, a map of `"<os>/<arch>"` to a download URL; targets with no entry
ship without mpv, and every bundled target also gets a `-nompv` archive. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for more.

## License

GNU Affero General Public License v3.0, the same as Cinefin. See
[LICENSE](LICENSE).
