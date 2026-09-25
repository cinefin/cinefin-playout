//go:build !ui

package ui

import (
	"context"
	"errors"
)

// ErrNoUI is returned by the entry points when the agent was built without UI
// support (i.e. without `-tags ui`).
var ErrNoUI = errors.New("agent built without UI support (build with -tags ui)")

// Available reports whether this build has the desktop shell compiled in.
func Available() bool { return false }

// RunTray is a no-op in the default build.
func RunTray(_ context.Context, _ TrayDeps) error { return ErrNoUI }
