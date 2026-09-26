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

   A tray icon appears, and on first run the agent creates its access token.

4. **Connect it to Cinefin.** From the tray menu, choose "Open control panel".
   The page shows the host address and token with copy buttons. In Cinefin, go to
   Settings > Playout > Add host and paste them in.

That is the whole setup. Cinefin now plays through this machine, and you set its
screen and audio from the same page.

## Run as a service (booth box)

For a box that should start on boot with no one logged in, run the same binary
with `--no-ui` under a service manager.

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

On first run the token is printed to the log and saved to `<state_dir>/token`. A
`config.toml` is optional (see `config.example.toml`); use it to pin the bind
address, the mpv binary, or a fixed token.

## Screen and audio

The screen, resolution, video output, HDR, audio device, and channels live on the
host in `config.toml`, under `[mpv.graphics]` and `[mpv.audio]`. You normally set
these from Cinefin's Playout page, which lists the real devices on the box and
writes your choices back to the file. Because the file is the source of truth on
the host, the box still boots, starts mpv, and shows the idle ident even when
Cinefin is offline.

You can hand-edit `config.toml` as a fallback. See `config.example.toml` for the
options, including headless DRM and audio passthrough. Changes take effect on the
next player restart.

### Running without a desktop (DRM/KMS)

mpv can draw straight to the screen through the kernel, with no Xorg or Wayland. In
the graphics config set `mode: "drm"` and one of:

- `gpu_api: "vulkan"` with `gpu_context: "displayvk"`: best on NVIDIA.
- `gpu_context: "drm"`: the GBM/EGL route for Intel and AMD, or NVIDIA with recent
  drivers.

Set `drm_connector` (for example `HDMI-A-1`) if the box has more than one output.
On NVIDIA, enable DRM KMS:

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

## Bringing your own mpv

Every standard archive includes a working mpv, so most people can skip this. If you
would rather use your own, the agent looks for mpv in this order:

1. The `binary` path under `[mpv]` in `config.toml`.
2. An `mpv` (or `mpv.exe`) next to the agent binary. This is the bundled one.
3. `mpv` on your `PATH`.

It logs the one it picked at startup (for example `mpv: /usr/bin/mpv`). To use your
own, download the `-nompv` archive, delete the bundled `mpv` file, or set
`[mpv].binary`.

## How it works

The agent owns mpv on the host. Cinefin sends mpv's own JSON-IPC commands over one
WebSocket and gets mpv's replies and events back. If mpv crashes, the agent
relaunches it with backoff; an explicit stop keeps it stopped. On Linux DRM, a
projector power-cycle re-modesets without a restart.

For the full design, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## API

Everything except `/health` and the loopback-only `/ui` needs an
`Authorization: Bearer <token>` header. Traffic is plain HTTP for a trusted LAN. Do
not expose it to the internet; put a reverse proxy in front if you need TLS.

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET  | `/health` | Liveness, version, os/arch (no auth). |
| GET  | `/status` | Agent, mpv, and control-link status. |
| WS   | `/ws/control` | Duplex mpv JSON-IPC. |
| GET  | `/hostconfig` | Current graphics, audio, and autostart config. |
| PUT  | `/hostconfig` | Replace the launch config. Returns `{restart_required}`. |
| PUT  | `/hostconfig/idle-media` | Set just the idle-screen media (the cinema ident). |
| GET  | `/hardware` | Audio devices, DRM connectors, screens, and mpv features. |
| POST | `/mpv/start`, `/mpv/stop`, `/mpv/restart` | Player lifecycle. |
| GET  | `/ui` | Control panel (loopback only). |

## Build from source

You need Go (see `go.mod` for the version). The common tasks are in the Makefile:

```bash
make build     # build the agent for this machine
make check     # gofmt, vet, tests, and a cross-compile of every target
```

`make build` includes the system tray. It is pure Go on Linux (D-Bus) and Windows
(GDI), so it cross-compiles with CGO off. The tray needs cgo only on macOS (Cocoa):

```bash
./scripts/build-macos-app.sh   # builds a self-contained "Cinefin Playout.app" into dist/
```

Releases are built by CI. Which targets ship a bundled mpv is set by
`mpv-bundle.json`, a map of `"<os>/<arch>"` to a download URL; targets with no entry
ship without mpv, and every bundled target also gets a `-nompv` archive. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for more.
