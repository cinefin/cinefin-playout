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

**Keepalive.** Cinefin keeps the control link open all the time, not only while
it is driving playback, so the player knows whether Cinefin is there. Both ends
send WebSocket pings every 15 s. The agent drops a client that has not answered
a ping within 10 s; Cinefin closes and redials a link on which nothing (frame or
pong) has arrived for 45 s, backing off from 2 s to 30 s between attempts. A dead
link is noticed in well under a minute instead of hanging until a command times
out.

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
| GET | `/health` | Liveness + version + os/arch + id/name/paired + `protocol` (unauth). |
| POST | `/pair` | `{"code"}` → `{"token", "id", "name", "protocol", …}`; 403 wrong code, 429 rate-limited, 409 already paired (unauth). |
| POST | `/unpair` | Forget the token and launch config; the player restarts onto the pairing box. |
| GET | `/status` | Agent + mpv process + control-link + standby status, and `test_card` / `test_sound` (below). |
| WS | `/ws/control` | Duplex JSON-IPC relay (above). |
| GET | `/hostconfig` | Current graphics + audio + autostart launch config. |
| PUT | `/hostconfig` | Replace the launch config (validated, persisted to the state file); returns `{restart_required}`. |
| PUT | `/standby` | Set the standby spec (below); 200 when its ident is already here, 202 while it downloads. |
| POST | `/standby` | Go to standby now; 503 when mpv is not running. |
| POST | `/testcard` | `{"on": true\|false}`: show or hide the test card (below); 400 without `on`. |
| POST | `/testsound` | Play the test sound (below); 409 off standby with the test card off, or while one plays; 503 when mpv is not running. |
| GET | `/hardware` | Enumerated audio devices, DRM connectors, screens, mpv version/features. |
| POST | `/mpv/start` \| `/mpv/stop` \| `/mpv/restart` | Process lifecycle; launch args assembled from `/hostconfig`. |
| GET | `/ui`, `/ui/state`, `/ui/player/*`, `/ui/unpair` | Status page + state/actions. **Loopback-only** (no token); opened in the browser by the `-tags ui` tray; `/ui/unpair` also serves `cinefin-playout reset`. |

## Pairing

A player is either **unpaired** (no token in its state file) or **paired**.

- **Unpaired**, the agent starts mpv full screen with `hostconfig.Pairing()`
  (the machine's defaults, no on-screen controller) whatever the autostart
  setting, and puts it on standby with the bundled System Ident (see
  Standby below).
- Over the standby ident the agent draws a **pairing box** below the mark: the
  6-digit code on the left; on the right, where to enter it in Cinefin, the
  player's address for adding it by hand, and a bar and "new code in N min"
  counting down the code's life. On a player upgraded from 0.1 with a
  `config.toml` (`cfg.LegacyConfig`), one more line under the countdown says
  the file is no longer used. The box is an mpv `osd-overlay` of ASS events
  on a 1280x720 canvas, sent over IPC (`internal/server/card*.go`). Its fill
  is 75% opaque, so the ident's orbiting light shows faintly through it; the
  text, bar and border are opaque.
- The box stays off the screen during the ident's intro, while the mark and
  wordmark animate in, and then fades in over 0.8 s (a cubic ease-out, the
  same curve as the confirmation). The intro length is `ident.IntroEnd`,
  parsed from `ab-loop-a` in `ident.Options`, so it follows the clip's hold
  options. The fade is timed from when `enterStandby` sent the loadfile, not
  from when the pairing scene started: mpv reports the new path within a
  millisecond of that command, so the path observer would add nothing. A box
  first drawn long after that load (standby has been up for a while) appears
  without a fade, and a new code or a redraw never fades it again. Over a
  cinema's own ident, which has no known intro, the fade starts at the load.
  The scene sleeps until the fade starts and redraws at frame rate during it.
- After the fade the box is redrawn every 5 seconds for the countdown, at
  once when the code rotates or the pairing changes, and after mpv restarts
  (a restart reloads the ident, so the box waits for the intro again). The
  agent's own replies (`request_id` 2000000001) are not relayed to Cinefin.
  The code is also on the tray, the status page and, for a headless box, the
  log.
- **The code** (`internal/pairing`) is six digits. It rotates every 5 minutes,
  after each wrong attempt and once used; attempts are limited to one a second,
  and five wrong in a row lock pairing for a minute.
- **Cinefin pairs** by `POST /pair {"code"}`. The agent answers with a fresh
  24-byte bearer token (stored in `state.json`), its id and name, and turns the
  box into a short confirmation (`card_confirm.go`): it starts as the pairing box
  exactly as shown, the code turns green and "Code accepted" replaces the
  instructions, then once Cinefin's control link attaches (or after 2.5 s) the
  box shows "Paired" with a check mark and slides away. The green panel keeps
  the pairing box's 75% fill, so the box does not thicken as it turns green.
  From then on the player
  launches from the launch config Cinefin sets.
- **Unpairing** (`POST /unpair` from Cinefin when the host is deleted, the tray's
  "Forget Cinefin", the status page, or `cinefin-playout reset`) clears the token,
  launch config and standby spec, drops the control link and restarts mpv onto a
  new code.
- **Discovery** (`internal/discovery`): the agent registers
  `_cinefin-playout._tcp` over mDNS with TXT `id`, `name`, `version`, `paired`,
  on the interface that carries the default route (not Docker/libvirt/VPN
  bridges), re-registering when that interface's addresses change. `id` is a
  random value kept across unpairing, so Cinefin can match a player whose
  address changed. mDNS does not cross a Docker bridge network, so Cinefin also
  accepts an address plus the code.

### What the overlay shows

The overlay (`internal/server/card.go`) shows one scene at a time, chosen on
every pass in this order, first match wins:

1. Unpaired: the pairing box.
2. The test card, while it is on (see Test card below).
3. The paired confirmation, while it plays.
4. Not on standby (Cinefin is showing a programme, or anything else): nothing.
5. On standby with `show_status` on in the standby spec: the status line.
6. On standby otherwise: the offline notice or the reconnect toast, when due.

A scene that finishes hands over to the next one in the same pass, so the
confirmation gives way to the status line with no blank frame between them.

**Status line** (`card_status.go`). One line along the bottom of the screen,
its baseline 40 px above the bottom edge of the 1280x720 canvas, 56 px in
from each side, with no panel behind it. The cinema name from the spec is on
the left (22 px, medium weight), left out when there is none. On the right,
right-aligned: a square dot and a word for the control link, then the player
name and the address Cinefin last connected from ("Screen 1 · Cinefin
192.168.1.20:8000"). The player name falls back to the agent's own name, and
the address part is left out when no address is known. The link states are
Ready (a client is attached, green dot), Connecting to Cinefin (not attached
for less than 30 s since the agent started or the client detached, grey) and
Can't reach Cinefin (not attached for 30 s or more, amber). The dot and the
separator are drawings inline in one right-aligned ASS event, so they follow
the width of the text. Names are cut to 36 (cinema), 24 (player) and 32
(address) characters with an ellipsis so the two ends cannot meet. The line
is checked every second and sent to mpv only when its text changes. While it
shows, the offline notice and the toast are not drawn: the link state is the
dot and the word.

**Offline notice** (`card_link.go`). A paired player on standby without the
status line whose control link has been down for 30 s (counted from the
agent's start when Cinefin has not connected since) shows a notice along the
bottom of the screen: "Can't reach Cinefin at <address>", where the address is
the one Cinefin last connected from (kept in `state.json` as `cinefin`, so it
survives a reboot). When Cinefin reconnects the notice gives way to a
"Connected to Cinefin again" toast for 3 s. The box positions are the
`linkNoticeBox` and `linkToastBox` constants there. A player that is not on
standby shows neither; if Cinefin has been away for 30 s and mpv has nothing
loaded, the agent puts it on standby, trying again at most every 10 s should
the load fail.

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

## Standby

Standby is what the screen shows when no programme is playing: the cinema's
ident, played once, then held. The player owns standby; Cinefin owns
programmes. For the System Ident the hold is a loop (per-file mpv options
`ab-loop-a=4,ab-loop-b=34`: a 4 s intro, then the 4 to 34 s section loops); for
a cinema's own ident it is a freeze (`end=<hold>,keep-open=always`). The mpv
options are built only in Cinefin; the agent keeps one built-in copy of the
System Ident's (`ident.Options`) for when it is unpaired or has no spec.

**The spec.** Cinefin builds a standby spec once and sends it with
`PUT /standby`:

```jsonc
{
  "ident": {
    "url": "http://cinefin:8000/…/stream?token=…",  // any http(s) GET; may carry a token
    "sha256": "<64 hex digits>",                     // of the file at url
    "options": "ab-loop-a=4,ab-loop-b=34"            // per-file mpv options for the hold
  },
  "cinema_name": "The Roxy",
  "player_name": "Screen 1",
  "show_status": true
}
```

`options` is comma-separated `key=value` pairs with no spaces, quotes or
escapes (it is passed to `loadfile` as is); it may be empty. `cinema_name`
and `player_name` are drawn by the status line, which `show_status` turns on
(see "What the overlay shows" above). The agent saves the spec in `state.json` (`standby`), then downloads the ident in
the background (10 minute timeout) into `<state_dir>/idents/<sha256>.mp4`,
checking the checksum and writing through a temporary file and a rename. It
keeps only the current ident, skips the download when that file is already
there, and resumes a missing download on start. A failed download keeps the
previous file (or the bundled ident) and is reported in `/status`. When a new
ident arrives while mpv is on standby, the agent switches to it. The reply to
`PUT` and `POST /standby`, and the `standby` key of `/status`, are:

```jsonc
{
  "spec": {"ident": {"sha256": "…", "options": "…"},   // no url: it carries a token
           "cinema_name": "…", "player_name": "…", "show_status": true},  // null when none
  "file": "/…/idents/<sha256>.mp4",  // the downloaded ident, "" when not here
  "downloading": false,
  "error": "",                        // why the last download failed
  "on_standby": true                  // mpv reports the standby file as loaded
}
```

`on_standby` follows mpv, so right after `POST /standby` it may still be false.

**Entering standby** is one function, `enterStandby`: it sends
`["loadfile", <path>, "replace", -1, <options>]` (mpv 0.38+ argument order)
under the agent's request id, then `set_property pause false`. The path and
options come from the spec when its file is here and the player is paired,
else the bundled ident (`ident.File`) with `ident.Options`. It runs each time
the link to mpv comes up (mpv starts idle, with nothing on its command line),
on `POST /standby`, and when Cinefin is away and mpv has nothing loaded (see
"What the overlay shows" above).

**Knowing what is on screen.** Each time mpv comes up the agent observes its
`path` under observer id 2000000003. Those `property-change` events, like
replies to request id 2000000001 and the test sound's observer 2000000004
(below), are kept from Cinefin (`control.fromMPV`).
The agent is on standby when `path` is the file it last loaded for standby,
and idle when there is no `path`.

`/health` and the `/pair` reply carry `"protocol": 2`, so Cinefin can tell a
player that owns standby from an older one.

## Screen and sound check

Cinefin's "Add a player" wizard has a "Screen and sound" step: after pairing,
the user checks that the picture is on the right screen and not cropped, and
that sound comes out of the right device and channels. The player provides two
endpoints for it.

**Test card.** `POST /testcard` with `{"on": true}` shows a test card scene on
the overlay (`internal/server/card_testcard.go`); `{"on": false}` hides it. The
card starts with a full-canvas black rectangle, so whatever mpv is showing
(standby, usually) is hidden and no file is loaded. On the 1280x720 canvas it
draws white L-shaped corner marks 16 px in from each corner, the safe area as a
thin rectangle inset 64 px left and right and 36 px top and bottom, "TEST CARD",
the player's name (the standby spec's `player_name`, else the agent's name), a
line describing the output, seven 75% colour bars, an 11-step grey ramp from
black to white (with a hairline outline, so its black first step still shows
where it starts, in line with the bars), and a Left and a Right speaker box. The output line is best effort: the DRM connector
or screen name from the launch config, and the resolution and refresh rate from
a pinned `drm_mode` or the displays `hardware.Screens` finds; unknown parts are
left out. The card takes precedence over every scene except the pairing box. It
turns itself off 5 minutes after the last `{"on": true}`, and on unpair.

The reply, and the `test_card` key of `/status`, is:

```jsonc
{"on": true, "off_in_s": 300}
```

**Test sound.** `POST /testsound` loads a tone in place of whatever mpv plays,
through the main mpv, so it goes to the configured audio device and channel
layout (the thing being tested):

```
av://lavfi:aevalsrc=exprs='<left>|<right>':c=stereo:s=48000:d=3
```

The left expression carries a 440 Hz sine at 0.25 (-12 dBFS peak) for the
first 1.5 s and the right one for the next 1.5 s, each with 50 ms ramps. It is
loaded with the per-file options `keep-open=no,loop-file=no,aid=auto`, so it
plays to its end whatever Cinefin set globally. The screen shows black while it
plays, or the test card when that is on. It is allowed only while mpv is on
standby or the test card is on, so it never cuts into a programme, and not
while a test sound is already playing (409). The reply gives the timing:

```jsonc
{
  "playing": true,
  "frequency_hz": 440,
  "duration_ms": 3000,
  "sequence": [
    {"channel": "left",  "start_ms": 0,    "duration_ms": 1500},
    {"channel": "right", "start_ms": 1500, "duration_ms": 1500}
  ]
}
```

While the tone plays, the agent observes mpv's `playback-time` under observer
id 2000000004 (kept from Cinefin like the path observer), and the test card
lights the speaker box of the side mpv is actually at. The observed `path`
ends the test sound: the tone's URL means it is playing; no path after that
means it reached its end and the agent calls `enterStandby`; another file means
Cinefin loaded something, and the agent leaves it alone. Should mpv not report
the end within 5 s of the tone's length, the agent returns to standby anyway
(unless another file is loaded). The `test_sound` key of `/status` is
`{"playing": true, "channel": "left"}`, with `channel` `"left"`, `"right"` or
`""`.

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
                 "display": ":0"},
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
go to standby before Cinefin ever connects, and keeps running if Cinefin is
down. Changes apply on the next player restart.

There is no config file. Where the agent listens, which mpv it runs and where it
keeps its state are command-line flags (`--listen`, `--port`, `--mpv`,
`--state-dir`). `--display` (a screen) and `--mode` (a DRM mode, via
`HostConfig.PickScreen` / `PickMode`) adjust the launch config only while
Cinefin has not set one. A hidden `--config` is accepted from 0.1 service files
and only warns (`config.LegacyConfigNotice`).

### mpv's own config

mpv runs with `--config-dir=<state_dir>/mpv` and `--load-scripts=no`, so it
reads an `mpv.conf` from the player's folder rather than the personal
`~/.config/mpv` of the user running the agent, and loads no user scripts (the
built-in osc, console and stats still load). `--mpv-config <file>` adds
`--include=<file>`. `BuildMPVArgs` puts these first: mpv reads the folder's
`mpv.conf` before its command line and an `--include` where it stands on the
command line, so the enforced options and the launch config, which follow, win
over both files. `/status` reports them under `mpv_config`.

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
  internal/state/                 state.json: player id, token, launch config, standby spec
  internal/ident/                 the bundled System Ident and its hold options
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
