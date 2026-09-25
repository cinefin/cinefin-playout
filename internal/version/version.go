// Package version holds the agent's build version string.
package version

// Version is the agent version, overridable at build time via
// -ldflags "-X git.kef2.net/micky/cinefin-playout/internal/version.Version=…".
var Version = "0.1.0-go"
