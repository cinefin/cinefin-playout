//go:build ui && !windows

package ui

import _ "embed"

//go:embed icon.png
var trayIcon []byte
