//go:build ui

package ui

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"fyne.io/systray"
	"github.com/atotto/clipboard"
)

// Available reports that this build has the desktop shell.
func Available() bool { return true }

// RunTray shows the system-tray icon and menu, driving the agent in-process via
// deps. It blocks until the user quits or ctx is cancelled. systray.Run must own
// the goroutine it is called on (the caller gives it the main goroutine).
func RunTray(ctx context.Context, deps TrayDeps) error {
	onReady := func() {
		if len(trayIcon) > 0 {
			systray.SetIcon(trayIcon)
		}
		systray.SetTitle("Cinefin Playout")
		systray.SetTooltip("Cinefin Playout agent")

		mPlayer := systray.AddMenuItem("Player: …", "")
		mCine := systray.AddMenuItem("Cinefin: …", "")
		mAddr := systray.AddMenuItem("", "This host's address")
		mPlayer.Disable()
		mCine.Disable()
		mAddr.Disable()
		systray.AddSeparator()
		mPanel := systray.AddMenuItem("Open control panel…", "Open the status & control page in your browser")
		mCopyAddr := systray.AddMenuItem("Copy address", "Copy this host's address for Cinefin")
		mCopyToken := systray.AddMenuItem("Copy token", "Copy the pairing token for Cinefin")
		systray.AddSeparator()
		mStart := systray.AddMenuItem("Start player", "")
		mStop := systray.AddMenuItem("Stop player", "")
		mRestart := systray.AddMenuItem("Restart player", "")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Stop the agent")

		address := pairingAddress(deps.Port)
		mAddr.SetTitle(address)

		refresh := func() {
			if deps.PlayerRunning() {
				mPlayer.SetTitle("● Player: running")
			} else {
				mPlayer.SetTitle("○ Player: stopped")
			}
			if deps.CinefinConnected() {
				mCine.SetTitle("● Cinefin: connected")
			} else {
				mCine.SetTitle("○ Cinefin: not connected")
			}
		}
		refresh()

		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					systray.Quit()
					return
				case <-t.C:
					refresh()
				case <-mPanel.ClickedCh:
					openControlPanel(deps.Port)
				case <-mCopyAddr.ClickedCh:
					_ = clipboard.WriteAll(address)
				case <-mCopyToken.ClickedCh:
					_ = clipboard.WriteAll(deps.Token)
				case <-mStart.ClickedCh:
					deps.Start()
					refresh()
				case <-mStop.ClickedCh:
					deps.Stop()
					refresh()
				case <-mRestart.ClickedCh:
					deps.Restart()
					refresh()
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}

	systray.Run(onReady, func() {})
	return nil
}

// openControlPanel opens the agent's loopback /ui status-and-control page in the
// operator's default browser. The page (served by the agent itself) holds all
// the status/control logic; the tray just launches it, so there is no embedded
// webview to build or ship.
func openControlPanel(port int) {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/ui"
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// pairingAddress is the URL to paste into Cinefin: the host's primary LAN IP
// (falling back to hostname) and the agent port.
func pairingAddress(port int) string {
	host := primaryIP()
	if host == "" {
		if hn, err := os.Hostname(); err == nil {
			host = hn
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// primaryIP returns the machine's primary outbound IP without sending packets.
func primaryIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	if addr, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}
