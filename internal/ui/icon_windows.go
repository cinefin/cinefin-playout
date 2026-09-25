//go:build ui && windows

package ui

import _ "embed"

// systray on Windows only accepts ICO bytes; PNG is silently rejected.
//
//go:embed icon.ico
var trayIcon []byte
