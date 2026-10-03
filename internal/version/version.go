// Package version holds the agent's build version string.
package version

// Version is the agent version, overridable at build time via
// -ldflags "-X github.com/cinefin/cinefin-playout/internal/version.Version=…".
// This literal is only the fallback for a plain `go build`: release builds stamp
// the git tag over it (see the release workflow, which resolves the module path
// with `go list -m`) and `make build` stamps `git describe`, so it never needs
// hand-editing. "dev" matches what Cinefin reports when it has no version.
var Version = "dev"
