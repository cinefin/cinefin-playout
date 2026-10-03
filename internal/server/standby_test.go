//go:build !windows

package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cinefin/cinefin-playout/internal/ident"
)

// pairedStandbyServer is a paired server over a fake player, and a function
// that sends it an authenticated request.
func pairedStandbyServer(t *testing.T) (*Server, *fakePlayer, func(method, path string, body any) (int, map[string]any)) {
	t.Helper()
	srv, ts, fp, _ := newPairTestServer(t)
	if err := srv.state.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	return srv, fp, call
}

func spec(url, sha, options string) map[string]any {
	return map[string]any{
		"ident":       map[string]any{"url": url, "sha256": sha, "options": options},
		"cinema_name": "The Roxy",
		"player_name": "Screen 1",
		"show_status": true,
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// identServer serves clips by path and counts the requests.
func identServer(t *testing.T, clips map[string][]byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, ok := clips[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func waitStandby(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := srv.standbyStatus()
		if st["downloading"] == false {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatal("download did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPutStandbyRejectsBadSpecs(t *testing.T) {
	_, _, call := pairedStandbyServer(t)
	sha := strings.Repeat("a", 64)
	for name, body := range map[string]any{
		"no url":         spec("", sha, ""),
		"file url":       spec("file:///etc/passwd", sha, ""),
		"short sha":      spec("http://c/ident", "abc", ""),
		"space":          spec("http://c/ident", sha, "end=4, keep-open=always"),
		"quote":          spec("http://c/ident", sha, `end="4"`),
		"no value":       spec("http://c/ident", sha, "end"),
		"trailing comma": spec("http://c/ident", sha, "end=4,"),
	} {
		if code, _ := call("PUT", "/standby", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
}

// PUT /standby downloads the ident once, checks it and keeps only the current
// one; a bad download keeps the previous file; /status never shows the URL.
func TestPutStandbyDownloadsTheIdent(t *testing.T) {
	srv, _, call := pairedStandbyServer(t)
	one, two := []byte("ident one"), []byte("ident two")
	clips, hits := identServer(t, map[string][]byte{"/one": one, "/two": two})
	dir := filepath.Join(srv.cfg.StateDir, identDir)

	code, _ := call("PUT", "/standby", spec(clips.URL+"/one?token=secret", sum(one), "end=4,keep-open=always"))
	if code != http.StatusAccepted {
		t.Fatalf("first PUT: %d, want 202", code)
	}
	st := waitStandby(t, srv)
	if st["error"] != "" || st["file"] != filepath.Join(dir, sum(one)+".mp4") {
		t.Fatalf("after download: %v", st)
	}

	// The same ident again: no download.
	if code, _ := call("PUT", "/standby", spec(clips.URL+"/one?token=secret", sum(one), "end=5")); code != http.StatusOK {
		t.Fatalf("same ident: %d, want 200", code)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("downloaded %d times, want once", n)
	}

	// A checksum mismatch keeps the previous file and reports the error.
	call("PUT", "/standby", spec(clips.URL+"/two", sum(one)[:63]+"0", ""))
	if st := waitStandby(t, srv); !strings.Contains(st["error"].(string), "checksum mismatch") {
		t.Fatalf("mismatch: %v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, sum(one)+".mp4")); err != nil {
		t.Errorf("previous ident removed: %v", err)
	}

	// A new ident replaces the old one.
	call("PUT", "/standby", spec(clips.URL+"/two", sum(two), ""))
	waitStandby(t, srv)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != sum(two)+".mp4" {
		t.Errorf("ident dir holds %v, want only the new ident", entries)
	}

	_, status := call("GET", "/status", nil)
	raw, _ := json.Marshal(status["standby"])
	if strings.Contains(string(raw), clips.URL) || !strings.Contains(string(raw), `"cinema_name":"The Roxy"`) {
		t.Errorf("/status standby = %s", raw)
	}
}

// POST /standby loads the cinema's ident with its options once downloaded,
// the bundled System Ident before that, and unpauses.
func TestPostStandbyLoadsTheIdent(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	bundled := filepath.Join(srv.cfg.StateDir, ident.FileName)

	n := len(fp.frames())
	if code, _ := call("POST", "/standby", nil); code != http.StatusOK {
		t.Fatalf("POST /standby: %d", code)
	}
	want := []string{
		string(agentCommand("loadfile", bundled, "replace", -1, titled(ident.Options, identTitle))),
		`{"command":["set_property","pause",false],"request_id":2000000001}`,
	}
	if got := fp.frames()[n:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("no spec: sent %v\nwant %v", got, want)
	}

	clip := []byte("the roxy")
	clips, _ := identServer(t, map[string][]byte{"/roxy": clip})
	call("PUT", "/standby", spec(clips.URL+"/roxy", sum(clip), "end=4,keep-open=always"))
	waitStandby(t, srv)
	n = len(fp.frames())
	call("POST", "/standby", nil)
	cached := filepath.Join(srv.cfg.StateDir, identDir, sum(clip)+".mp4")
	if got := fp.frames()[n]; got != string(agentCommand("loadfile", cached, "replace", -1, titled("end=4,keep-open=always", identTitle))) {
		t.Fatalf("with spec: sent %s", got)
	}

	// mpv reports the file: the player knows it is on standby.
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000003,"name":"path","data":"` + cached + `"}`))
	if !srv.standby.onStandby() || srv.standby.idle() {
		t.Error("not on standby after mpv loaded the ident")
	}
	srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000003,"name":"path","data":"/media/trailer.mkv"}`))
	if srv.standby.onStandby() {
		t.Error("still on standby after another file loaded")
	}
}

// Each time mpv comes up the agent observes its path and loop-file and puts
// it on standby.
func TestMPVConnectEntersStandby(t *testing.T) {
	srv, fp, _ := pairedStandbyServer(t)
	srv.standby.observed(json.RawMessage(`"/old.mkv"`))
	n := len(fp.frames())
	srv.mpvConnected(true)
	got := fp.frames()[n:]
	if len(got) != 4 || got[0] != `{"command":["observe_property",2000000003,"path"],"request_id":2000000001}` ||
		got[1] != `{"command":["observe_property",2000000005,"loop-file"],"request_id":2000000001}` ||
		!strings.Contains(got[2], `"loadfile"`) {
		t.Fatalf("on connect: %v", got)
	}
	if !srv.standby.idle() {
		t.Error("the fresh mpv's path was not reset")
	}
}

func TestHealthReportsProtocol(t *testing.T) {
	_, _, call := pairedStandbyServer(t)
	if _, out := call("GET", "/health", nil); out["protocol"] != float64(2) {
		t.Errorf("/health protocol = %v, want 2", out["protocol"])
	}
}

// Cinefin sends PUT /standby and then POST /standby at once. When the new
// ident finishes downloading before mpv reports the file POST loaded, the
// player still switches to it; once Cinefin has loaded something else, it
// does not.
func TestStandbySwitchesToAnIdentDownloadedBeforeMPVReports(t *testing.T) {
	srv, fp, call := pairedStandbyServer(t)
	bundled := filepath.Join(srv.cfg.StateDir, ident.FileName)
	path := func(p string) {
		srv.control.fromMPV([]byte(`{"event":"property-change","id":2000000003,"name":"path","data":"` + p + `"}`))
	}
	release := make(chan struct{})
	clip := []byte("the roxy")
	clips := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write(clip)
	}))
	t.Cleanup(clips.Close)
	cached := filepath.Join(srv.cfg.StateDir, identDir, sum(clip)+".mp4")
	loadCached := string(agentCommand("loadfile", cached, "replace", -1, titled("", identTitle)))
	// The switch follows the end of the download, so wait for it.
	loadedCached := func(from int) bool {
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			for _, f := range fp.frames()[from:] {
				if f == loadCached {
					return true
				}
			}
		}
		return false
	}

	// Cinefin was playing a feature; mpv has not yet reported the bundled
	// ident POST loads when the download completes.
	path("/media/feature.mkv")
	call("PUT", "/standby", spec(clips.URL+"/roxy", sum(clip), ""))
	call("POST", "/standby", nil)
	n := len(fp.frames())
	close(release)
	if !loadedCached(n) {
		t.Fatalf("new ident not loaded; sent %v", fp.frames()[n:])
	}

	// mpv reports the file, then Cinefin loads a feature: a later download
	// leaves the feature alone.
	path(bundled)
	path(cached)
	path("/media/feature.mkv")
	two := []byte("the roxy, again")
	clips2, _ := identServer(t, map[string][]byte{"/two": two})
	n = len(fp.frames())
	call("PUT", "/standby", spec(clips2.URL+"/two", sum(two), ""))
	waitStandby(t, srv)
	time.Sleep(100 * time.Millisecond)
	if got := fp.frames()[n:]; len(got) != 0 {
		t.Fatalf("switched away from Cinefin's file: sent %v", got)
	}
}
