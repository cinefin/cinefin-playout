//go:build !windows

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// `cinefin-playout reset` unpairs the running agent over loopback with no
// token (httptest connects over loopback, which the gate allows).
func TestLocalUnpair(t *testing.T) {
	base, st := newHostTestServer(t, "/tmp/does-not-exist-x.sock")
	if !st.Paired() {
		t.Fatal("test server should start paired")
	}
	if resp, _ := post(t, base+"/local/unpair", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /local/unpair = %d", resp.StatusCode)
	}
	if st.Paired() {
		t.Error("still paired after /local/unpair")
	}
}

// The status page is gone: /ui is no longer served.
func TestNoStatusPage(t *testing.T) {
	base, _ := newHostTestServer(t, "/tmp/does-not-exist-x.sock")
	resp := do(t, "GET", base+"/ui", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /ui = %d, want 404", resp.StatusCode)
	}
}

// A peer that is not on loopback is refused.
func TestLoopbackOnlyRefusesRemotePeer(t *testing.T) {
	h := loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	r := httptest.NewRequest("POST", "/local/unpair", nil)
	r.RemoteAddr = "192.168.1.20:51000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("remote peer got %d, want 403", w.Code)
	}
}
