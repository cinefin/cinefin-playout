// Package player abstracts the mpv playback backend behind one interface: the
// agent drives mpv as a supervised subprocess (talking to its local JSON-IPC
// socket) without the server or the control relay knowing the details.
//
// The relay is a near-transparent conduit of mpv's own JSON-IPC frames (see
// docs/ARCHITECTURE.md, "Control channel"). A Backend therefore presents exactly
// that shape: it accepts raw command frames via Send, reports Connected, and
// pushes every inbound reply/event frame to the sink registered with OnMessage.
// On top of that it owns the player lifecycle (Start/Stop/Restart/Status) and a
// reachability Probe for /status.
//
// There is one implementation, subprocess.go, behind the Backend interface; the
// interface remains so tests can substitute a fake and the relay/server stay
// decoupled from the process management.
package player

import (
	"context"

	"github.com/cinefin/cinefin-playout/internal/hostconfig"
	"github.com/cinefin/cinefin-playout/internal/mpvproc"
)

// Status is the player/process status reported to /status: pid, uptime,
// restarts and socket reachability of the supervised mpv child.
type Status = mpvproc.Status

// HostConfigProvider yields the current host config at (re)start time, so a saved
// PUT /hostconfig takes effect on the next start without caching a stale copy.
type HostConfigProvider = func() hostconfig.HostConfig

// Backend is a playback engine the agent can drive. Implementations are safe for
// concurrent use: Send and the lifecycle methods may be called from HTTP
// handlers while the inbound sink fires from a read/event goroutine.
type Backend interface {
	// Send forwards one raw mpv JSON-IPC command frame
	// (e.g. {"command":[...],"request_id":n}) to the player. It returns an error
	// if the player is not currently reachable.
	Send(frame []byte) error

	// Connected reports whether the player is reachable right now.
	Connected() bool

	// OnMessage registers the sink for inbound frames (replies + events),
	// delivered verbatim as mpv would emit them. Call once before Run.
	OnMessage(func([]byte))

	// OnMPVReconnect registers a callback fired when the link to mpv is
	// re-established after a drop (mpv restart/crash). The relay uses it to
	// force control clients to reconnect and re-subscribe, since the fresh mpv
	// has none of their observers. Call once before Run.
	OnMPVReconnect(func())

	// Run starts the backend's background work (mpv IPC connection maintenance)
	// and returns immediately; it stops when ctx is cancelled.
	Run(ctx context.Context)

	// Autostart starts the player at boot if the host config asks for it.
	Autostart()

	// Start ensures the player is running. wait blocks until it is controllable.
	Start(wait bool) (bool, string)
	// Stop stops the player.
	Stop() (bool, string)
	// Restart stops then starts.
	Restart(wait bool) (bool, string)

	// Status returns the current player/process status.
	Status() Status

	// Probe reports player reachability and its mpv version, for /status.
	Probe(ctx context.Context) (version string, ok bool)

	// Close tears the backend down (stops background work and the IPC client).
	Close() error
}
