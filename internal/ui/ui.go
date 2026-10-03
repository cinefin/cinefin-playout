// Package ui is the agent's optional desktop shell for operators who run the
// agent interactively rather than as a headless service: a system-tray icon
// (menu-only) with live status, the pairing code while unpaired, forgetting the
// Cinefin pairing, and start/stop/restart, all driven in-process.
//
// It is compiled only under the `ui` build tag (native toolkit: systray, which
// talks to the desktop over D-Bus/GDI — no webview). The default build gets the
// stubs in stub.go, so the agent still runs headless.
package ui

// TrayDeps is what the tray needs from the running agent, injected so the ui
// package stays decoupled from the server/player internals. The closures are
// called from the tray's goroutine.
type TrayDeps struct {
	Version          string        // the agent version, shown greyed in the menu
	Address          func() string // this player's address, for adding it by hand
	Paired           func() bool   // has a Cinefin paired with this player
	Code             func() string // the current pairing code, for display
	PlayerRunning    func() bool   // is mpv up
	CinefinConnected func() bool   // is a control client connected
	Start            func()        // start / stop / restart the player
	Stop             func()        //
	Restart          func()        //
	Forget           func()        // forget the Cinefin pairing
}
