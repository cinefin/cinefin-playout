//go:build ui

package ui

import (
	"context"
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
		mCode := systray.AddMenuItem("", "Enter this code in Cinefin to pair this player")
		mAddr := systray.AddMenuItem("", "This player's address")
		mPlayer.Disable()
		mCine.Disable()
		mCode.Disable()
		mAddr.Disable()
		systray.AddSeparator()
		mPanel := systray.AddMenuItem("Open status page…", "Open the status page in your browser")
		mCopyAddr := systray.AddMenuItem("Copy address", "Copy this player's address, to add it in Cinefin by hand")
		systray.AddSeparator()
		mStart := systray.AddMenuItem("Start player", "")
		mStop := systray.AddMenuItem("Stop player", "")
		mRestart := systray.AddMenuItem("Restart player", "")
		systray.AddSeparator()
		// A submenu is the confirmation: forgetting takes two deliberate clicks.
		mForget := systray.AddMenuItem("Forget Cinefin", "Unpair this player; it shows a new pairing code")
		mForgetYes := mForget.AddSubMenuItem("Yes, forget this Cinefin", "")
		mQuit := systray.AddMenuItem("Quit", "Stop the agent")

		refresh := func() {
			mAddr.SetTitle(deps.Address())
			if deps.PlayerRunning() {
				mPlayer.SetTitle("● Player: running")
			} else {
				mPlayer.SetTitle("○ Player: stopped")
			}
			switch {
			case !deps.Paired():
				mCine.SetTitle("○ Cinefin: not paired")
				mCode.SetTitle("Pairing code: " + deps.Code())
				mCode.Show()
				mForget.Disable()
			case deps.CinefinConnected():
				mCine.SetTitle("● Cinefin: connected")
				mCode.Hide()
				mForget.Enable()
			default:
				mCine.SetTitle("○ Cinefin: paired, not connected")
				mCode.Hide()
				mForget.Enable()
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
					_ = clipboard.WriteAll(deps.Address())
				case <-mStart.ClickedCh:
					deps.Start()
					refresh()
				case <-mStop.ClickedCh:
					deps.Stop()
					refresh()
				case <-mRestart.ClickedCh:
					deps.Restart()
					refresh()
				case <-mForgetYes.ClickedCh:
					deps.Forget()
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
