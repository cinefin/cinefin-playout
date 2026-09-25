# cinefin-playout

The playout agent for a Cinefin cinema. It runs on the machine wired to your
projector, plays programmes full-screen through mpv, and is driven over the
network by [Cinefin](https://github.com/cinefin/cinefin). Linux and Windows.

## Quick start

1. **Download** the build for your machine from the
   [Releases page](https://github.com/cinefin/cinefin-playout/releases):
   - Linux: `cinefin-playout-<version>-linux-amd64.tar.gz`
   - Windows: `cinefin-playout-<version>-windows-amd64.zip`

   These bundle `mpv`, so there is nothing else to install. Unzip anywhere and
   keep the `mpv` file next to `cinefin-playout`.

2. **Run it.**
   - Windows: double-click `cinefin-playout.exe`.
   - Linux: `./cinefin-playout`.

   A tray icon appears. The agent generates an access token on first run. Open the
   tray menu and choose **Open control panel** to see this host's address and token
   (with copy buttons).

3. **Pair with Cinefin.** In Cinefin, go to **Settings > Playout > Add host** and
   paste the address and token. Cinefin now plays through this machine, and you set
   its graphics and audio from that page.

## Run as a service (booth box)

For a box with no one sitting at it, run the same binary headless with `--no-ui`
under a service manager. systemd:

```bash
sudo cp cinefin-playout mpv /usr/local/bin/
sudo cp etc/cinefin-playout.service.example /etc/systemd/system/cinefin-playout.service
sudoedit /etc/systemd/system/cinefin-playout.service   # set your username
sudo systemctl daemon-reload
sudo systemctl enable --now cinefin-playout.service
```

The unit already passes `--no-ui`. On Windows, register the binary with
`sc.exe create` or Task Scheduler.

## Configuration

A config file is optional: the agent runs on defaults and generates a token on
first run. Graphics and audio are set from the Cinefin Playout page, not by hand.
To pin the bind address, the mpv path, or a fixed token, add a `config.toml` (see
`config.example.toml`).

### Use your own mpv

The bundled `mpv` is used by default. To use your own, do any of: download the
`-nompv` archive, delete the bundled `mpv` file, or set `binary` under `[mpv]` in
`config.toml`. The agent picks `[mpv].binary`, then the sibling `mpv`, then `mpv`
on `PATH`, and logs the chosen path at startup.
