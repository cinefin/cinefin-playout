package session

import "fmt"

// Desktop is always true on macOS: the agent ships there as an app that runs in
// the user's session.
func Desktop() bool { return true }

// Displays is empty on macOS, which has no display variables.
func Displays() string { return "" }

// UseServer is unsupported on macOS: there is no display server to choose.
func UseServer(v string) error {
	return fmt.Errorf("--display %s: choosing a display server only applies on Linux", v)
}
