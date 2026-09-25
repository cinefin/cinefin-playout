//go:build !windows

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinefin/cinefin-playout/internal/config"
	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// newHostTestServer builds a Server with a temp state_dir and config.toml path,
// pointed at socketPath for reachability.
func newHostTestServer(t *testing.T, socketPath string) (string, config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.IPCSocket = socketPath
	cfg.Token = "secret"
	cfg.StateDir = t.TempDir()
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")
	ts := newServerForTest(t, cfg, nil)
	return ts.URL, cfg
}

func do(t *testing.T, method, url string, body []byte) *http.Response {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		r, err = http.NewRequest(method, url, bytes.NewReader(body))
	} else {
		r, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGetHostConfigDefault(t *testing.T) {
	base, _ := newHostTestServer(t, "/tmp/does-not-exist-x.sock")
	resp := do(t, "GET", base+"/hostconfig", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /hostconfig = %d", resp.StatusCode)
	}
	var hc hostconfig.HostConfig
	json.NewDecoder(resp.Body).Decode(&hc)
	if hc.Graphics.VO == "" {
		t.Errorf("default hostconfig should have a vo, got %+v", hc.Graphics)
	}
}

// A full launch config PUT is validated, persisted to config.toml and reflected
// by a subsequent GET.
func TestPutHostConfigPersistsAndRoundTrips(t *testing.T) {
	base, cfg := newHostTestServer(t, "/tmp/does-not-exist-x.sock")

	hc := config.Default().HostConfig
	hc.Autostart = false
	hc.Graphics.Mode = hostconfig.ModeDesktop
	hc.Graphics.VO = "gpu"
	hc.Graphics.Screen = 1
	hc.Audio.Device = "wasapi"
	hc.Audio.MaxVolume = 110
	body, _ := json.Marshal(hc)

	resp := do(t, "PUT", base+"/hostconfig", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /hostconfig = %d", resp.StatusCode)
	}

	loaded, err := config.Load(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Autostart || loaded.Graphics.VO != "gpu" || loaded.Graphics.Screen != 1 ||
		loaded.Audio.Device != "wasapi" || loaded.Audio.MaxVolume != 110 {
		t.Errorf("launch config not persisted: %+v", loaded.HostConfig)
	}

	// A subsequent GET reflects the written values.
	get := do(t, "GET", base+"/hostconfig", nil)
	defer get.Body.Close()
	var got hostconfig.HostConfig
	json.NewDecoder(get.Body).Decode(&got)
	if got.Graphics.VO != "gpu" || got.Audio.Device != "wasapi" {
		t.Errorf("GET after PUT = %+v", got)
	}
}

// An invalid launch config is rejected (400) and nothing is written.
func TestPutHostConfigRejectsInvalid(t *testing.T) {
	base, cfg := newHostTestServer(t, "/tmp/does-not-exist-x.sock")

	// drm mode with no connector fails hostconfig.Validate.
	bad := config.Default().HostConfig
	bad.Graphics.Mode = hostconfig.ModeDRM
	bad.Graphics.DRMConnector = ""
	body, _ := json.Marshal(bad)

	resp := do(t, "PUT", base+"/hostconfig", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT invalid /hostconfig = %d, want 400", resp.StatusCode)
	}
	if _, err := os.Stat(cfg.Path); err == nil {
		t.Errorf("rejected PUT must not write config.toml")
	}
}

// Only the idle media (the Cinefin-owned cinema ident) is pushed by the narrow
// idle-media PUT, and it must leave the rest of the config untouched.
func TestPutIdleMediaPersistsOnly(t *testing.T) {
	base, cfg := newHostTestServer(t, "/tmp/does-not-exist-x.sock")

	// Seed a known launch config in config.toml.
	seed := config.Default()
	seed.Audio.Device = "pipewire"
	seed.Graphics.Screen = 2
	if err := config.WriteLaunchConfig(cfg.Path, seed); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "PUT", base+"/hostconfig/idle-media", []byte(`{"idle_media":"http://cinefin/ident.mp4"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /hostconfig/idle-media = %d", resp.StatusCode)
	}
	var out struct {
		RestartRequired bool `json:"restart_required"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.RestartRequired {
		t.Errorf("restart_required should be false when mpv is not running")
	}

	loaded, err := config.Load(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Graphics.IdleMedia != "http://cinefin/ident.mp4" {
		t.Errorf("idle_media not persisted: %q", loaded.Graphics.IdleMedia)
	}
	// Graphics/audio settings must be untouched.
	if loaded.Audio.Device != "pipewire" || loaded.Graphics.Screen != 2 {
		t.Errorf("idle-media PUT altered graphics/audio: %+v", loaded)
	}
}

func TestPutIdleMediaRestartRequiredWhenRunning(t *testing.T) {
	fake := newFakeMPV(t) // responds to mpv-version → "running"
	base, _ := newHostTestServer(t, fake.path)

	resp := do(t, "PUT", base+"/hostconfig/idle-media", []byte(`{"idle_media":"x"}`))
	defer resp.Body.Close()
	var out struct {
		RestartRequired bool `json:"restart_required"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if !out.RestartRequired {
		t.Errorf("restart_required should be true when mpv responds on the socket")
	}
}

func TestHardwareEndpoint(t *testing.T) {
	base, _ := newHostTestServer(t, "/tmp/does-not-exist-x.sock")
	resp := do(t, "GET", base+"/hardware", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /hardware = %d, want 200 (never 500)", resp.StatusCode)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"audio_devices", "drm_connectors", "screens", "mpv"} {
		if _, ok := body[key]; !ok {
			t.Errorf("hardware response missing %q", key)
		}
	}
}
