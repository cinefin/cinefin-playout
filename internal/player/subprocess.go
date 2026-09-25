package player

import (
	"context"
	"log"
	"sync/atomic"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/mpvipc"
	"github.com/cinefin/cinefin-playout/internal/mpvproc"
)

// subprocess drives mpv as a supervised child process, talking to its local
// JSON-IPC socket. It composes the persistent IPC client (transport: Send /
// Connected / inbound frames) with the process manager (lifecycle: start / stop
// / supervise / DRM hotplug) behind the Backend interface.
type subprocess struct {
	cfg config.Config
	mpv *mpvipc.Client
	mgr *mpvproc.Manager
	log *log.Logger

	onReconnect func()      // fired when the mpv link comes back after a drop
	sawDown     atomic.Bool // did the mpv link drop since the last "up"?
}

// New builds the mpv playback backend: mpv driven as a supervised subprocess
// over its local JSON-IPC socket. hcOf yields the current host config at spawn
// time, re-read on each (re)start so a config edit takes effect without
// restarting the agent.
func New(cfg config.Config, hcOf HostConfigProvider, logger *log.Logger) Backend {
	if logger == nil {
		logger = log.Default()
	}
	return &subprocess{
		cfg: cfg,
		mpv: mpvipc.New(cfg.IPCSocket),
		mgr: mpvproc.New(cfg, hcOf, logger),
		log: logger,
	}
}

func (s *subprocess) Send(frame []byte) error   { return s.mpv.Send(frame) }
func (s *subprocess) Connected() bool           { return s.mpv.Connected() }
func (s *subprocess) OnMessage(fn func([]byte)) { s.mpv.OnMessage(fn) }
func (s *subprocess) OnMPVReconnect(fn func())  { s.onReconnect = fn }

// Run wires connection logging, starts the persistent IPC client, the process
// supervisor and the DRM hotplug watcher, then returns. All stop when ctx ends.
func (s *subprocess) Run(ctx context.Context) {
	s.mpv.OnConnState(func(up bool) {
		if up {
			s.log.Printf("mpv ipc connected: %s", s.cfg.IPCSocket)
			// Reconnect after a drop = a possibly-restarted mpv with no
			// observers → make control clients re-establish and re-subscribe.
			if s.sawDown.Swap(false) && s.onReconnect != nil {
				s.onReconnect()
			}
		} else {
			s.log.Printf("mpv ipc disconnected: %s", s.cfg.IPCSocket)
			s.sawDown.Store(true)
		}
	})
	go s.mpv.Run(ctx)
	s.mgr.StartMonitor()
	go s.mgr.WatchDRM(ctx, "")
}

func (s *subprocess) Autostart()                       { s.mgr.Autostart() }
func (s *subprocess) Start(wait bool) (bool, string)   { return s.mgr.Start(wait) }
func (s *subprocess) Stop() (bool, string)             { return s.mgr.Stop() }
func (s *subprocess) Restart(wait bool) (bool, string) { return s.mgr.Restart(wait) }
func (s *subprocess) Status() Status                   { return s.mgr.Status() }

func (s *subprocess) Probe(ctx context.Context) (string, bool) {
	v, err := mpvipc.Probe(ctx, s.cfg.IPCSocket)
	if err != nil {
		return "", false
	}
	return v, true
}

func (s *subprocess) Close() error {
	s.mgr.Shutdown()
	return s.mpv.Close()
}
