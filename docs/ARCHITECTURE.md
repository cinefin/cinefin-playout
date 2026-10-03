# cinefin-playout — architecture

The playout agent is a single static Go binary that owns mpv on the box wired to
the projector. It spawns and supervises mpv, owns the host's graphics + audio
launch config, enumerates the real hardware, and relays mpv's JSON-IPC over one
authenticated WebSocket. Cinefin (the Django app) drives playback through that
WebSocket and never touches the mpv socket directly.

This document is the design reference; the [README](../README.md) covers
install and day-to-day operation.

## Shape

```
┌──────────────── cinefin (Django) ─────────────────┐        ┌────────── playout host: agent (Go) ──────────┐
│ mpv_service.py  (programme state machine)            │        │  HTTP + WS server  :8089  (bearer token)      │
│   └ MPVController  ───────────────────────────────────┼─ WS ──▶│   /ws/control   duplex JSON-IPC relay         │
│        └ WSMPV transport                             │        │   /health /pair /status /hostconfig /hardware │
│                                                      │        │   /mpv/start|stop|restart                     │
│ PlayoutHost model (N rows; 1 active)                 │        │        │ owns graphics+audio launch config    │
│ Settings→Playout: subtitle/style (global, live)      │        │        │ enumerates HW (audio/DRM/screens)    │
│                                                      │        │        ▼ spawns/supervises mpv                │
│                                                      │        │   mpv --input-ipc-server=<local socket/pipe>  │
└──────────────────────────────────────────────────────┘        └────────────────────────────────────────────────┘
```

Two responsibilities live on the host, not in Cinefin: reaching the mpv socket
(the agent's private business), and the graphics/audio launch options (host-owned
config). Everything about `mpv_service`'s playback behaviour stays in Cinefin.

## Control channel

`mpv_service` drives `MPVController`, whose transport is a `WSMPV` client. The
agent is a near-transparent relay of mpv's own JSON-IPC, so there is no
translation table — the vocabulary on the wire is mpv's:

- **Command** (Cinefin → agent → mpv):
  `{"command": ["loadfile", "<path>", "replace"], "request_id": 42}`
- **Reply** (mpv → agent → Cinefin):
  `{"request_id": 42, "error": "success", "data": <any>}`
- **Event** (mpv → agent → Cinefin):
  `{"event": "end-file", "reason": "eof"}`,
  `{"event": "property-change", "name": "time-pos", "data": 12.3}`

Observers are established as mpv expects: the client sends
`{"command":["observe_property", <id>, "time-pos"]}` and receives
`property-change` events. `WSMPV` therefore implements the small slice of a
JSON-IPC client `MPVController` uses — synchronous `command()` with `request_id`
matching, property get/set, event/property-change dispatch, and reconnect.

**Agent side.** The agent holds a persistent local connection to mpv's IPC (unix
socket on Linux/macOS, named pipe `\\.\pipe\mpv-…` on Windows) and fans it out:
each `/ws/control` client gets commands forwarded to mpv and every mpv event
broadcast back; `request_id`s are namespaced per client so replies route
correctly; if mpv isn't up, control frames get an immediate structured error.
Normally there is exactly one control client (Cinefin); more are allowed and
simply share the event stream.

**Reconnection.** On reconnect the client re-registers observers (mpv drops them
when the socket closes) and `mpv_service` re-syncs status — the same resilience
the old direct-socket path had, now behind one seam.

## Playback backend

`internal/player.Backend` is the seam between the agent and mpv. It presents the
control channel (Send / Connected / inbound frames) plus the player lifecycle
(Start / Stop / Restart / Status / Probe) as one interface, so the server and the
relay never depend on the process management underneath.

The one implementation is **subprocess** (`subprocess.go`, pure Go) — it spawns
and supervises an `mpv` child and talks to its local JSON-IPC socket. Being pure
Go, it cross-compiles to a static binary for every target. The mpv binary is
shipped alongside the agent in the release archive where a build is configured
for that target (the agent prefers an `mpv` next to its own executable, then the
configured `[mpv].binary`, then `PATH`), so "batteries included" needs no
in-process libmpv. The interface remains so tests can substitute a fake.

## Desktop shell (optional, `-tags ui`)

For operators who run the agent interactively rather than as a headless service,
a `-tags ui` build (`internal/ui`) adds one native surface:

- **System tray** (systray): a menu with live status (player / Cinefin), the
  pairing code while unpaired, copy address, start/stop/restart, "Forget
  Cinefin" and **Open status page…**. It drives the running agent
  **in-process** (injected `TrayDeps` closures), so it holds no privileged URL.
  It is shown when the agent runs in a desktop session (`internal/session`:
  `DISPLAY`/`WAYLAND_DISPLAY` on Linux, not a Windows service); a service, a box
  with no display, `--no-ui` or a non-ui build stays headless. "Open status
  page…" opens the agent's own **loopback-only `/ui` page**
  (`internal/server/panel.go`, an embedded HTML status page) in the default
  browser, so there is no embedded webview to build or ship.

systray talks to the desktop over D-Bus (Linux) or GDI (Windows) — both CGO-free
— so the `-tags ui` build still cross-compiles with CGO off there. Only macOS's
tray needs cgo (Cocoa).

## Build tags

| Tags | Result |
| --- | --- |
| *(none)* | pure-Go static binary, mpv subprocess backend, headless. The default/shipped service build. |
| `ui` | + the system tray (same binary; CGO-free on Linux/Windows, cgo/Cocoa on macOS). |

## REST surface

All endpoints except `/health` and `/pair` require `Authorization: Bearer
<token>` (the WS upgrade carries the same header). The token is issued by
pairing (below); an unpaired agent has none and refuses them all. **No TLS** — plaintext on a trusted LAN;
encryption, if wanted, is a reverse proxy's job. JSON in/out.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/health` | Liveness + version + os/arch + id/name/paired (unauth). |
| POST | `/pair` | `{"code"}` → `{"token", "id", "name", …}`; 403 wrong code, 429 rate-limited, 409 already paired (unauth). |
| POST | `/unpair` | Forget the token and launch config; the player restarts onto the pairing card. |
| GET | `/status` | Agent + mpv process + control-link status. |
| WS | `/ws/control` | Duplex JSON-IPC relay (above). |
| GET | `/hostconfig` | Current graphics + audio + autostart launch config. |
| PUT | `/hostconfig` | Replace the launch config (validated, persisted to the state file); returns `{restart_required}`. |
| PUT | `/hostconfig/idle-media` | Set only `graphics.idle_media` (the Cinefin-owned cinema ident); returns `{restart_required}`. |
| GET | `/hardware` | Enumerated audio devices, DRM connectors, screens, mpv version/features. |
| POST | `/mpv/start` \| `/mpv/stop` \| `/mpv/restart` | Process lifecycle; launch args assembled from `/hostconfig`. |
| GET | `/ui`, `/ui/state`, `/ui/player/*`, `/ui/unpair` | Status page + state/actions. **Loopback-only** (no token); opened in the browser by the `-tags ui` tray; `/ui/unpair` also serves `cinefin-playout reset`. |

## Pairing

A player is either **unpaired** (no token in its state file) or **paired**.

- **Unpaired**, the agent starts mpv full screen with `hostconfig.Pairing()`
  (the machine's defaults, no on-screen controller, no idle media) whatever the
  autostart setting, and draws a **pairing card** on it: the player's name, a
  6-digit code and its address. The card is an mpv `osd-overlay` sent over IPC
  (`internal/server/card.go`), redrawn when the code or address changes and after
  mpv restarts; the agent's own replies (`request_id` 2000000001) are not
  relayed to Cinefin. The code is also on the tray, the status page and, for a
  headless box, the log.
- **The code** (`internal/pairing`) is six digits. It rotates every 5 minutes,
  after each wrong attempt and once used; attempts are limited to one a second,
  and five wrong in a row lock pairing for a minute.
- **Cinefin pairs** by `POST /pair {"code"}`. The agent answers with a fresh
  24-byte bearer token (stored in `state.json`), its id and name, and removes the
  card. From then on the player launches from the launch config Cinefin sets.
- **Unpairing** (`POST /unpair` from Cinefin when the host is deleted, the tray's
  "Forget Cinefin", the status page, or `cinefin-playout reset`) clears the token
  and launch config, drops the control link and restarts mpv onto a new card.
- **Discovery** (`internal/discovery`): the agent registers
  `_cinefin-playout._tcp` over mDNS with TXT `id`, `name`, `version`, `paired`,
  on the interface that carries the default route (not Docker/libvirt/VPN
  bridges), re-registering when that interface's addresses change. `id` is a
  random value kept across unpairing, so Cinefin can match a player whose
  address changed. mDNS does not cross a Docker bridge network, so Cinefin also
  accepts an address plus the code.

`/hardware` is what lets the Cinefin UI show **dropdowns of real devices**
instead of hand-typed strings:

```jsonc
{
  "audio_devices": [
    {"name": "alsa/hdmi:CARD=NVidia,DEV=0", "description": "HDA NVidia, HDMI"},
    {"name": "pipewire", "description": "PipeWire"}
  ],
  "drm_connectors": ["HDMI-A-1", "DP-1", "DP-2"],     // linux only
  "screens": [{"index": 0, "w": 3840, "h": 2160, "hz": 24.0}],
  "mpv": {"version": "0.41", "vo": ["gpu-next","gpu"], "gpu_apis": ["vulkan","opengl"]}
}
```

Enumeration is **on demand** (a Cinefin "rescan" is another `GET /hardware` —
no background polling): audio via `mpv --audio-device=help` (portable, no login
session needed); DRM connectors from `/sys/class/drm/*/status` (Linux); mpv
features from `--vo=help` / `--gpu-api=help`; **screens/monitors** from the live
session (`xrandr` on X11, `wlr-randr` on wlroots Wayland, with a `/sys/class/drm`
fallback for a headless console) on Linux and `EnumDisplayDevices`/
`EnumDisplaySettings` on Windows. A missing mpv binary or absent tool degrades to
empty lists plus a `note`, never a 500.

## Config ownership

### Launch config: agent state, set from Cinefin

The mpv launch options (autostart, graphics, audio) are a `hostconfig.HostConfig`,
exchanged as-is by `GET/PUT /hostconfig`. Cinefin sets them; the agent keeps a
copy in its state file, `<state_dir>/state.json` (package `state`), next to the
bearer token:

```jsonc
{
  "token": "…",
  "launch": {
    "autostart": true,
    "graphics": {"mode": "desktop", "vo": "gpu-next", "gpu_api": "", "gpu_context": "",
                 "hwdec": "auto", "screen": 0, "drm_connector": "", "drm_mode": "",
                 "fullscreen": true, "hdr_passthrough": true, "osc": false,
                 "display": ":0", "idle_media": ""},
    "audio": {"device": "", "channels": "auto", "spdif_passthrough": [], "max_volume": 130}
  }
}
```

The agent turns these into mpv launch args at spawn (`hostconfig.BuildMPVArgs`,
the single place that decides the command line); the IPC socket, `--idle=yes` and
`--force-window=yes` are always enforced so config can't disable what Cinefin
relies on. Until Cinefin sets a launch config, the agent uses a per-OS default
**preset** (Linux/NVIDIA desktop gpu-next; Windows/NVIDIA gpu-next + d3d11 +
wasapi; a `PresetDRM` for the headless booth box). The DRM/KMS knobs are
first-class fields gated by `mode = "drm"`, and the agent performs the re-modeset
on display hotplug itself (watches `/sys/class/drm/*/status`) so a projector
power-cycle recovers without a restart.

Cinefin `GET`s `/hostconfig` (with `/hardware` supplying dropdowns of real
devices) and `PUT`s the whole launch config back per `PlayoutHost`; the agent
validates it (`hostconfig.Validate`), stores it and returns `{restart_required}`.
The agent's copy is what it launches from, so it can boot, autostart mpv and
show the idle ident before Cinefin ever connects, and keeps running if Cinefin is
down. The narrow `PUT /hostconfig/idle-media` remains for the one field that is
Cinefin *content* (its streamed cinema ident): it changes only that key. Changes
apply on the next player restart.

There is no config file. Where the agent listens, which mpv it runs and where it
keeps its state are command-line flags (`--listen`, `--port`, `--mpv`,
`--state-dir`).

### Cinefin-owned — `Settings → playout.subtitles` (global, applied live)

```jsonc
{
  "subtitles": {
    "font": "", "font_size": 52, "color": "#FFFFFF",
    "border_style": "opaque-box", "back_color": "#000000BF",
    "position": 88, "margin_y": 10, "use_margins": true, "scale_by_window": false
  }
}
```

Applied by `mpv_service` via `set_property` (`sub-font-size`, `sub-pos`,
`sub-color`, `sub-border-style`, …) over the control channel — on connect and
whenever saved, no mpv restart. Per-block audio/subtitle *track* selection is
unchanged. (Per-programme style override is deferred; the model leaves room.)

## Cinefin data model — `PlayoutHost`

```
id, name, base_url, token, enabled, is_active (exactly one true),
last_seen_at, agent_version, os, arch
```

`MPVController` resolves the active host's `base_url`+`token` to open the WS.
The token comes from pairing (`POST /pair`), not from the user.
The Settings → Playout page has two sections: **host** graphics/audio/autostart
(reads/writes the agent's `/hostconfig`, dropdowns from `/hardware`; per-host,
persisted in the agent's state file) and **presentation** subtitles
(reads/writes Cinefin Settings, applied live).

## Go project layout

```
cinefin-playout/            (module: github.com/cinefin/cinefin-playout)
  cmd/cinefin-playout/main.go   flags, token, run server
  internal/config/                startup options (flag defaults, mpv binary resolution)
  internal/state/                 state.json: player id, token, launch config
  internal/pairing/               rotating pairing code + attempt limits
  internal/discovery/             mDNS advertisement (_cinefin-playout._tcp)
  internal/netaddr/               primary LAN address / interface
  internal/session/               desktop session vs headless detection
  internal/hostconfig/            graphics+audio+autostart types + preset → mpv args
  internal/hardware/              audio/DRM/screen/mpv enumeration (os-tagged)
  internal/player/                Backend interface + mpv subprocess impl
  internal/mpvproc/               spawn/supervise/backoff, DRM hotplug re-modeset
  internal/mpvipc/                local mpv socket/pipe client (os-tagged)
  internal/server/                HTTP + WS, auth, handlers, single-client control bridge
  etc/                            systemd unit (linux)
  docs/ARCHITECTURE.md            this file
```

- HTTP/WS: stdlib `net/http` + `github.com/coder/websocket` (context-native).
- State: one JSON file (`encoding/json`), written atomically on every change.
- The default/service build is pure Go, no cgo → one static binary per OS. CI
  cross-compiles linux/windows/darwin (amd64 + arm64); `release.yml` ships
  archives + checksums on `v*` tags. The named-pipe vs unix-socket IPC path is
  chosen by os-tagged files at build time.

## Out of scope (for now)

- Per-programme subtitle overrides; multi-host UI routing; agent
  auto-discovery/registration.
