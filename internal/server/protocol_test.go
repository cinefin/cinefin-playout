//go:build !windows

package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
	"github.com/cinefin/cinefin-playout/internal/pairing"
	"github.com/cinefin/cinefin-playout/internal/player"
	"github.com/cinefin/cinefin-playout/internal/state"
)

// newGateTestServer is a paired server, returned with its URL so a test can
// look at what the TV would say.
func newGateTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := config.Default()
	cfg.IPCSocket = "/tmp/does-not-exist-x.sock"
	cfg.StateDir = t.TempDir()
	st, err := state.Open(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetToken("secret"); err != nil {
		t.Fatal(err)
	}
	backend := player.New(cfg, func() hostconfig.HostConfig { return hostconfig.Preset("linux") }, log.New(io.Discard, "", 0))
	srv := New(cfg, Identity{ID: "test-id", Name: "Test player"}, st, pairing.New(), backend, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts.URL
}

// rawGet sends a GET with exactly the given headers: a plain transport, not
// the tests' default one, which adds the protocol header.
func rawGet(t *testing.T, url string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// A Cinefin from before the header (it sends its old token, no protocol) is
// refused before the token check, told to update, and the TV says so.
func TestProtocolGateRefusesOldCinefin(t *testing.T) {
	srv, base := newGateTestServer(t)
	code, body := rawGet(t, base+"/status", map[string]string{"Authorization": "Bearer old-token"})
	if code != http.StatusUpgradeRequired {
		t.Fatalf("GET /status from an old Cinefin = %d, want 426", code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "Cinefin "+minCinefin) {
		t.Errorf("error = %q, want it to name Cinefin %s", msg, minCinefin)
	}
	if body["min_protocol"] != float64(minProtocol) || body["protocol"] != float64(protocolVersion) {
		t.Errorf("426 body = %v, want the protocol range", body)
	}
	if n := srv.mismatchNotice(true); !strings.Contains(n, "needs updating to "+minCinefin) || !strings.Contains(n, "127.0.0.1") {
		t.Errorf("TV notice = %q", n)
	}
}

// A Cinefin newer than this player is refused and told to update the player.
func TestProtocolGateRefusesNewerCinefin(t *testing.T) {
	srv, base := newGateTestServer(t)
	code, body := rawGet(t, base+"/status", map[string]string{protocolHeader: "3", "Authorization": "Bearer secret"})
	if code != http.StatusUpgradeRequired {
		t.Fatalf("GET /status at protocol 3 = %d, want 426", code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "Update cinefin-playout") {
		t.Errorf("error = %q", msg)
	}
	if _, cinefinOld, ok := srv.mismatch.recent(); !ok || cinefinOld {
		t.Errorf("mismatch = ok %v, cinefinOld %v; want the player to be the one to update", ok, cinefinOld)
	}
}

// A request in range goes on to the token check as before.
func TestProtocolGatePassesCurrentCinefin(t *testing.T) {
	srv, base := newGateTestServer(t)
	if code, _ := rawGet(t, base+"/status", map[string]string{protocolHeader: "2", "Authorization": "Bearer secret"}); code != http.StatusOK {
		t.Errorf("GET /status at protocol 2 = %d, want 200", code)
	}
	if code, _ := rawGet(t, base+"/status", map[string]string{protocolHeader: "2"}); code != http.StatusUnauthorized {
		t.Errorf("GET /status at protocol 2 without a token = %d, want 401", code)
	}
	if n := srv.mismatchNotice(true); n != "" {
		t.Errorf("TV notice = %q, want none", n)
	}
}

// /health stays open to every protocol and reports the range.
func TestHealthReportsProtocolRange(t *testing.T) {
	_, base := newGateTestServer(t)
	code, body := rawGet(t, base+"/health", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /health = %d", code)
	}
	if body["protocol"] != float64(protocolVersion) || body["min_protocol"] != float64(minProtocol) {
		t.Errorf("/health = %v, want protocol and min_protocol", body)
	}
}

// Something that is not Cinefin (no header, no token: a browser, a scanner)
// is refused but puts nothing on the TV.
func TestProtocolGateIgnoresStrangers(t *testing.T) {
	srv, base := newGateTestServer(t)
	if code, _ := rawGet(t, base+"/", nil); code != http.StatusUpgradeRequired {
		t.Errorf("GET / with no header = %d, want 426", code)
	}
	if n := srv.mismatchNotice(true); n != "" {
		t.Errorf("TV notice = %q, want none for a stranger", n)
	}
}

// `cinefin-playout reset` sends no protocol header; /local stays open to it.
func TestProtocolGateLetsLocalThrough(t *testing.T) {
	srv, base := newGateTestServer(t)
	client := &http.Client{Transport: &http.Transport{}}
	resp, err := client.Post(base+"/local/unpair", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || srv.Paired() {
		t.Errorf("POST /local/unpair without the header = %d, paired %v; want 200 and unpaired", resp.StatusCode, srv.Paired())
	}
}
