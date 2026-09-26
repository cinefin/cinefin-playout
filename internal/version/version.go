// Package version holds the agent's build version string.
package version

// Version is the agent version, overridable at build time via
// -ldflags "-X github.com/cinefin/cinefin-playout/internal/version.Version=…".
// This literal is only a dev fallback: release builds stamp the git tag over it
// (see the release workflow, which resolves the module path with `go list -m`),
// so it never needs hand-editing.
var Version = "0.1.0"
