# cinefin-playout

The playout agent that runs on the machine connected to your projector or screen.
It runs mpv, plays programmes full-screen (trailers, bumpers, idents, the
feature), and is controlled over the network by [Cinefin](../cinefin). Cinefin
speaks mpv's JSON-IPC over a single authenticated WebSocket; the mpv socket itself
never leaves the host.

Runs on Linux, Windows, and macOS, on a desktop machine or a headless booth box.

## Requirements

- mpv. Most release archives bundle it (see [Downloads](#downloads)). You can also
  provide your own; see [Choosing an mpv](#choosing-an-mpv).
- Network access to the Cinefin server.

## Downloads

Release archives are on the [Releases page](https://github.com/cinefin/cinefin-playout/releases).
Archive names follow this pattern:

```
cinefin-playout-<version>-<os>-<arch>[-nompv].<ext>
```

- `<os>-<arch>`: `linux-amd64` (`.tar.gz`) and `windows-amd64` (`.zip`).
- `-nompv`: the agent binary only, with no bundled mpv (see
  [Choosing an mpv](#choosing-an-mpv)). The plain archive bundles mpv.

There is one agent binary. It shows a system tray when you run it interactively,
and runs headless when started with `--no-ui` (see [Run headless](#run-headless-service)).
So one download covers both a machine someone sits at and a booth box.

macOS is not in the release archives. Build the desktop app on a Mac with
`scripts/build-macos-app.sh`; it produces a self-contained `Cinefin Playout.app`
with mpv bundled.

### mpv bundling by OS

| OS | mpv bundled? |
| --- | --- |
| Linux (amd64) | Yes, a portable build, in the plain archive. |
| Windows (amd64) | Yes, in the plain archive. |
| macOS | The `.app` (built on a Mac) bundles mpv. |

The `-nompv` archives never contain mpv.

## Choosing an mpv

The agent resolves which mpv to run in this order:

1. An explicit `binary` path in `config.toml` under `[mpv]`.
2. An `mpv` (or `mpv.exe`) sitting next to the agent binary. This is the bundled
   one in a standard archive.
3. `mpv` found on `PATH`.

It logs the resolved path at startup, for example `mpv: /usr/bin/mpv`, so you can
confirm which one is in use.

To use your own mpv instead of a bundled one, do any of:

- Download the matching `-nompv` archive, so no bundled mpv is present and the
  agent uses `PATH` (or your configured `[mpv].binary`).
- Delete the `mpv` file next to the agent in a standard archive.
- Set `binary` under `[mpv]` in `config.toml` to a specific path.

## Install and run

Unzip the archive anywhere. Keep the `mpv` file next to `cinefin-playout` unless
you are bringing your own.

### Run with a tray

Run the agent directly and it shows a system-tray icon:

- Windows: run `cinefin-playout.exe`. A tray icon appears.
- Linux: run `./cinefin-playout`. The tray appears on any D-Bus desktop. The build
  is pure Go, so there is no GTK or WebKit to install.
- macOS: open `Cinefin Playout.app`. It is unsigned, so on first run use
  right-click then Open, or run `xattr -d com.apple.quarantine "Cinefin Playout.app"`.
  The icon appears in the menu bar.

On first run the agent generates an access token. Use the tray menu:

- Open control panel: opens the status page (`/ui`) in your browser. It shows the
  host address and token with copy buttons.
- Copy address / Copy token: copies each value to the clipboard.

### Run headless (service)

Start the same binary with `--no-ui` to skip the tray, and run it under a service
manager. systemd example:

```bash
sudo cp cinefin-playout mpv /usr/local/bin/     # copy the bundled mpv too, or rely on a system mpv
sudo cp etc/cinefin-playout.service.example /etc/systemd/system/cinefin-playout.service
sudoedit /etc/systemd/system/cinefin-playout.service   # set your username
sudo systemctl daemon-reload
sudo systemctl enable --now cinefin-playout.service
```

The agent owns mpv itself, so disable any standalone `cinefin-mpv.service`. On
Windows, register the agent with `sc.exe create` or Task Scheduler.

A config file is optional. With no config the agent runs on defaults and
generates a token on first run (printed to the log and saved to
`<state_dir>/token`). Add a `config.toml` (see `config.example.toml`) to pin the
bind address, the mpv binary, or a fixed token. Graphics and audio are normally
set from Cinefin.

## Pair with Cinefin

In Cinefin, go to Settings > Playout > Add host and paste the host address and
token. Cinefin then plays through this machine, and you set its graphics and audio
from the same page.

## How it works

The agent owns mpv on the host. Cinefin sends mpv's own JSON-IPC commands over one
WebSocket and receives mpv's replies and events back.

```
+------------ cinefin (Django) ------------+        +--------- playout host: agent ---------+
| mpv_service  ->  MPVController           |        |  HTTP + WS server :8089 (bearer token)|
|                +- WSMPV transport --------+- WS ->|   /ws/control   duplex JSON-IPC       |
|                                          |        |   /hostconfig /hardware /mpv/*        |
| PlayoutHost row (url + token)            |        |      | spawns / supervises           |
| Settings: subtitles (applied live)       |        |      v                                |
+------------------------------------------+        |  mpv (subprocess)                     |
                                                    +---------------------------------------+
```

- Control: commands carry `request_id`s; replies and events (`property-change`,
  `end-file`, and so on) come back as mpv emits them.
- Supervision: if mpv crashes, the agent relaunches it with backoff. An explicit
  stop keeps it stopped until the next start.
- Host graphics and audio: the launch config lives on the host in `config.toml`.
  Cinefin reads and writes it over the API.
- DRM hotplug recovery (Linux DRM mode): a projector power-cycle re-modesets
  without a restart.

Full design: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Graphics and audio

The mpv launch config (screen, resolution, video output, HDR, audio device,
channels) lives on the host in the `[mpv.graphics]` and `[mpv.audio]` sections of
`config.toml`. You do not normally edit it by hand. The Cinefin Playout page reads
the agent's `/hostconfig`, shows dropdowns of the real devices from `/hardware`,
and writes changes back per host. The agent validates them and saves them to
`config.toml`. Because the file is the source of truth on the host, the box still
boots, autostarts mpv, and shows the idle ident when Cinefin is offline.

Hand-editing `config.toml` is available as an offline fallback (see
`config.example.toml`, including headless/DRM and audio passthrough). Changes apply
on the next player restart.

## Headless DRM/KMS (no display server)

mpv can render straight to the screen through the kernel's DRM interface, with no
Xorg or Wayland. In the graphics config set `mode: "drm"` and one of:

- `gpu_api: "vulkan"`, `gpu_context: "displayvk"`: Vulkan direct-to-display, the
  best path on NVIDIA.
- `gpu_context: "drm"`: the GBM/EGL route (Intel/AMD, or NVIDIA with GBM in
  drivers 545 and later).

Set `drm_connector` (for example `HDMI-A-1`) if the host has several outputs.
NVIDIA needs DRM KMS enabled in the kernel modules:

```
# /etc/modprobe.d/nvidia-drm.conf
options nvidia-drm modeset=1 fbdev=1
```

Regenerate the initramfs and reboot, then check that
`cat /sys/module/nvidia_drm/parameters/modeset` prints `Y`. Give the agent's user
DRM access (`SupplementaryGroups=video render`) and make sure no display manager
holds DRM master on that GPU. For audio without a desktop, run a user PipeWire or
PulseAudio session, or point the audio device at an ALSA sink such as
`alsa/hdmi:CARD=NVidia,DEV=0` (from `GET /hardware`).

## API

All endpoints except `/health` and the loopback-only `/ui` require an
`Authorization: Bearer <token>` header. Traffic is plaintext HTTP, intended for a
trusted LAN. Do not expose it to the internet; put a reverse proxy or tunnel in
front if you need TLS.

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET  | `/health` | Liveness, version, os/arch (no auth). |
| GET  | `/status` | Agent, mpv, and control-link status. |
| WS   | `/ws/control` | Duplex mpv JSON-IPC (commands, replies, events). |
| GET  | `/hostconfig` | Current graphics, audio, and autostart launch config. |
| PUT  | `/hostconfig` | Replace the launch config (validated, persisted). Returns `{restart_required}`. |
| PUT  | `/hostconfig/idle-media` | Set only the idle-screen media (the Cinefin cinema ident). |
| GET  | `/hardware` | Enumerated audio devices, DRM connectors, screens, and mpv features. |
| POST | `/mpv/start`, `/mpv/stop`, `/mpv/restart` | Player lifecycle. |
| GET  | `/ui` | Control-panel page (loopback only; opened in the browser by the desktop build's tray). |

## Build from source

```bash
go build ./...          # service build (subprocess mpv, no UI), pure Go
go vet ./... && gofmt -l . && go test ./...
```

Build the desktop flavour (adds the system tray) with `-tags ui`. It is still pure
Go, so it cross-compiles with CGO off, the same as the service build:

```bash
# Linux and Windows: systray runs over D-Bus/GDI, no native toolkit needed
go build -tags ui ./cmd/cinefin-playout
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags ui ./cmd/cinefin-playout

# macOS: its tray needs cgo (Cocoa); also needs the Xcode command line tools
./scripts/build-macos-app.sh   # builds the self-contained "Cinefin Playout.app" into dist/
```

The `ui` build tag adds the system tray. It needs cgo only on macOS (Cocoa); on
Linux it uses D-Bus and on Windows it uses GDI, both without cgo. Without the tag
you get the same binary, headless. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for details.

CI builds and attaches all variants to a release on each `v*` tag, except the
macOS `-desktop` `.app`, which must be built on a Mac with
`scripts/build-macos-app.sh` and attached by hand. Which targets bundle mpv is set
by `mpv-bundle.json` at the repo root: a JSON map of `"<os>/<arch>"` to a download
URL. On Linux the URL should point at a portable AppImage or static build. Targets
with no entry ship without mpv. Every target that bundles mpv also gets a `-nompv`
archive.
