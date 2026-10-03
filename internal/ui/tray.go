//go:build ui

package ui

import (
	"context"
	"runtime"
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
		// macOS draws the title as text beside the icon in the menu bar; show only
		// the icon there. Elsewhere the title is the item's name, not visible text.
		if runtime.GOOS != "darwin" {
			systray.SetTitle("Cinefin Playout")
		}
		systray.SetTooltip("Cinefin Playout agent")

		mPlayer := systray.AddMenuItem("Player: …", "")
		mCine := systray.AddMenuItem("Cinefin: …", "")
		mCode := systray.AddMenuItem("", "Enter this code in Cinefin to pair this player")
		mAddr := systray.AddMenuItem("", "This player's address")
		mVersion := systray.AddMenuItem("cinefin-playout "+deps.Version, "This player's version")
		mPlayer.Disable()
		mCine.Disable()
		mCode.Disable()
		mAddr.Disable()
		mVersion.Disable()
		systray.AddSeparator()
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
