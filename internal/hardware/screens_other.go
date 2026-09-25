//go:build !linux && !windows

package hardware

import "context"

// enumScreens has no portable implementation off Linux/Windows.
func enumScreens(_ context.Context) []Screen { return nil }
