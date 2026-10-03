// Package discovery announces the player on the local network over mDNS/DNS-SD,
// so Cinefin can list it on its Playout page without anyone typing an address.
//
// The service type is _cinefin-playout._tcp. TXT records carry the player's
// stable id, its name, the agent version and whether it is paired, which
// Cinefin uses to tell new players from ones it already knows.
package discovery

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/cinefin/cinefin-playout/internal/netaddr"
)

// ServiceType is the DNS-SD service type the agent registers.
const ServiceType = "_cinefin-playout._tcp"

// Info is what the advertisement says about the player.
type Info struct {
	ID      string
	Name    string
	Version string
	Port    int
}

// Advertiser keeps the player's mDNS registration up.
type Advertiser struct {
	info   Info
	paired func() bool
	log    *log.Logger

	mu  sync.Mutex
	srv *zeroconf.Server
}

// New returns an Advertiser. paired is read whenever the TXT records are built.
func New(info Info, paired func() bool, logger *log.Logger) *Advertiser {
	return &Advertiser{info: info, paired: paired, log: logger}
}

// Run registers the service and keeps it registered until ctx ends. A box that
// boots before its network is up has no address to announce yet, so a failed
// registration is retried every 10 s, and the registration is renewed when the
// network changes (checked every minute).
//
// Only the interface carrying the default route is announced when there is one:
// otherwise every Docker, libvirt and VPN bridge address is advertised too, and
// Cinefin would have to guess which one reaches the player.
func (a *Advertiser) Run(ctx context.Context) {
	for {
		ifaces, key := announceOn()
		srv, err := zeroconf.Register(a.info.Name, ServiceType, "local.", a.info.Port, a.text(), ifaces)
		if err != nil {
			a.log.Printf("discovery: %v (retrying in 10s)", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}
		a.mu.Lock()
		a.srv = srv
		a.mu.Unlock()
		a.log.Printf("discovery: announcing %q as %s on %s", a.info.Name, ServiceType, key)

		changed := a.waitForChange(ctx, key)
		a.mu.Lock()
		a.srv = nil
		a.mu.Unlock()
		srv.Shutdown()
		if !changed {
			return
		}
	}
}

// waitForChange blocks until ctx ends (false) or the network to announce on
// differs from key (true).
func (a *Advertiser) waitForChange(ctx context.Context, key string) bool {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			if _, now := announceOn(); now != key {
				return true
			}
		}
	}
}

// announceOn picks the interfaces to announce on (nil = all) and a key that
// changes when they or their addresses do.
func announceOn() ([]net.Interface, string) {
	iface := netaddr.PrimaryInterface()
	if iface == nil {
		return nil, "all interfaces"
	}
	key := iface.Name
	if addrs, err := iface.Addrs(); err == nil {
		for _, a := range addrs {
			key += " " + a.String()
		}
	}
	return []net.Interface{*iface}, key
}

// Update re-announces the TXT records, after pairing or unpairing.
func (a *Advertiser) Update() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		a.srv.SetText(a.text())
	}
}

func (a *Advertiser) text() []string {
	paired := "0"
	if a.paired() {
		paired = "1"
	}
	return []string{
		"id=" + a.info.ID,
		"name=" + a.info.Name,
		"version=" + a.info.Version,
		"paired=" + paired,
	}
}
