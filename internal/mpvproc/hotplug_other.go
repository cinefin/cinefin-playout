//go:build !linux

package mpvproc

import "context"

// hasVTHandlers is Linux-only (DRM/VT). Elsewhere mpv never has a controlling
// VT for re-modeset, so this is always false.
func hasVTHandlers() bool { return false }

// WatchDRM is a no-op off Linux (no /sys/class/drm, no DRM mode).
func (m *Manager) WatchDRM(_ context.Context, _ string) {}
