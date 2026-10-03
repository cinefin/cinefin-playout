package server

import (
	"net/http"
	"os"
	"strconv"
	"testing"
)

// The tests speak to the server as a current Cinefin: every request through
// the default transport (http.Get, http.DefaultClient, the WebSocket dialer)
// carries the protocol header. The protocol gate's own tests use a client of
// their own.
func TestMain(m *testing.M) {
	http.DefaultTransport = protocolTransport{http.DefaultTransport}
	os.Exit(m.Run())
}

type protocolTransport struct{ base http.RoundTripper }

func (t protocolTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get(protocolHeader) == "" {
		r = r.Clone(r.Context())
		r.Header.Set(protocolHeader, strconv.Itoa(protocolVersion))
	}
	return t.base.RoundTrip(r)
}
