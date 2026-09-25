// Package ui is the agent's optional desktop shell for operators who run the
// agent interactively rather than as a headless service: a system-tray icon
// (menu-only) with live status, pairing (copy address/token) and
// start/stop/restart — all driven in-process. "Open control panel…" launches
// the agent's loopback-only /ui status page in the operator's default browser,
// so there is no embedded webview to build or ship.
//
// It is compiled only under the `ui` build tag (native toolkit: systray, which
// talks to the desktop over D-Bus/GDI — no webview). The default build gets the
// stubs in stub.go, so the agent still runs headless.
package ui

// TrayDeps is what the tray needs from the running agent, injected so the ui
// package stays decoupled from the server/player internals. The closures are
// called from the tray's goroutine.
type TrayDeps struct {
	Port             int         // agent HTTP port, for the pairing address
	Token            string      // pairing token, for "Copy token"
	PlayerRunning    func() bool // is mpv up
	CinefinConnected func() bool // is a control client connected
	Start            func()      // start / stop / restart the player
	Stop             func()      //
	Restart          func()      //
}
