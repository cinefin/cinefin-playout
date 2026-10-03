package session

import (
	"fmt"

	"golang.org/x/sys/windows/svc"
)

// Desktop reports whether the agent runs in an interactive session rather than
// as a Windows service (session 0, no desktop).
func Desktop() bool {
	isService, err := svc.IsWindowsService()
	return err != nil || !isService
}

// Displays is empty on Windows, which has no display variables.
func Displays() string { return "" }

// UseServer is unsupported on Windows: there is no display server to choose.
func UseServer(v string) error {
	return fmt.Errorf("--display %s: choosing a display server only applies on Linux", v)
}
