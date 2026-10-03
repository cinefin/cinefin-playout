package server

import (
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/cinefin/cinefin-playout/internal/netaddr"
)

// registerLocal mounts the routes for this machine only. `cinefin-playout
// reset` asks the running agent to forget its pairing here, so the TV shows a
// new code straight away. They carry no bearer token and are gated to loopback
// instead.
func (s *Server) registerLocal(mux *http.ServeMux) {
	mux.Handle("POST /local/unpair", loopbackOnly(http.HandlerFunc(s.handleUnpair)))
}

// loopbackOnly rejects any request whose peer is not on the loopback interface.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			http.Error(w, "loopback only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// pairingAddress is the URL to paste into Cinefin: the host's primary LAN IP
// (falling back to hostname, then loopback) and the agent port.
func (s *Server) pairingAddress() string {
	host := netaddr.PrimaryIP()
	if host == "" {
		if hn, err := os.Hostname(); err == nil && hn != "" {
			host = hn
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(s.cfg.Port))
}
