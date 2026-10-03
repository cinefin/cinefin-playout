package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cinefin/cinefin-playout/internal/ident"
	"github.com/cinefin/cinefin-playout/internal/state"
)

// Standby is what the player shows when Cinefin is not playing a programme:
// the cinema's ident, played once and then held by per-file mpv options (a
// loop for the System Ident, a freeze for a cinema's own). The player owns it:
// Cinefin sends a standby spec once (PUT /standby), the player downloads the
// ident and keeps it, and enterStandby loads it whenever mpv starts, on
// POST /standby, and when Cinefin is away and mpv has nothing loaded. An
// unpaired player, or one with no spec or no downloaded file yet, uses the
// bundled System Ident with ident.Options.
//
// The agent observes mpv's path (pathObserverID) so it knows whether mpv is on
// standby or has nothing loaded, without asking, and its loop-file
// (loopObserverID) for endHoldWhenAway.

// identDir is where the downloaded ident is kept, inside the state directory.
const identDir = "idents"

// Limits on an ident download.
const (
	identTimeout  = 10 * time.Minute
	identMaxBytes = 4 << 30
)

// standby is the agent's standby state. Guarded by mu.
type standby struct {
	mu   sync.Mutex
	path string // mpv's path, "" when nothing is loaded
	// looping is set while mpv's loop-file is inf, which only Cinefin's
	// command hold sets (standby holds with ab-loop or keep-open).
	looping bool
	loaded  string // the file enterStandby last loaded
	// loadedAt is when enterStandby sent that loadfile. It stands for when
	// the ident started: mpv reports the new path within a millisecond of the
	// command and shows the first frame some tens of milliseconds later, so
	// waiting for the path observer would not make it any closer.
	loadedAt time.Time
	// pending is set from enterStandby's loadfile until mpv reports loaded,
	// or a file that is not a standby file: until then path may still be the
	// file before, or "" between the two.
	pending  bool
	stateDir string // where the standby files are

	gen         int                // bumped by every fetch, so a stale download is dropped
	cancel      context.CancelFunc // cancels the download in flight
	downloading bool
	err         string // why the last download failed, "" when it did not
}

// idle reports whether mpv has nothing loaded.
func (sb *standby) idle() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.path == ""
}

// loopingForever reports whether mpv's loop-file is inf.
func (sb *standby) loopingForever() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.looping
}

// observedLoop takes mpv's loop-file from a property-change event on the
// agent's observer: "inf", a count, or false.
func (sb *standby) observedLoop(data json.RawMessage) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.looping = string(data) == `"inf"`
}

// onStandby reports whether mpv is showing the standby file.
func (sb *standby) onStandby() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.path != "" && sb.path == sb.loaded
}

// intro reports when the file enterStandby last loaded was sent to mpv (zero
// when none was) and how long its intro runs before the ident is at rest:
// ident.IntroEnd for the System Ident, 0 for a cinema's own ident, whose
// options say nothing about an intro.
func (sb *standby) intro() (at time.Time, intro time.Duration) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.loaded == filepath.Join(sb.stateDir, ident.FileName) {
		intro = ident.IntroEnd
	}
	return sb.loadedAt, intro
}

// observed takes mpv's path from a property-change event on the agent's
// observer: a string, or absent/null when nothing is loaded.
func (sb *standby) observed(data json.RawMessage) {
	var path string
	_ = json.Unmarshal(data, &path)
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.path = path
	// mpv has loaded the standby file, or Cinefin something else since.
	if path == sb.loaded || (path != "" && !sb.standbyFile(path)) {
		sb.pending = false
	}
}

// standbyFile reports whether path is the bundled System Ident or a
// downloaded ident.
func (sb *standby) standbyFile(path string) bool {
	return path == filepath.Join(sb.stateDir, ident.FileName) ||
		filepath.Dir(path) == filepath.Join(sb.stateDir, identDir)
}

// showingStandby reports whether mpv is on standby or about to be: it shows
// the file enterStandby last loaded, or has not yet reported that file.
// Guarded by mu.
func (sb *standby) showingStandby() bool {
	return sb.pending || (sb.path != "" && sb.path == sb.loaded)
}

// mpvConnected runs each time the link to mpv comes up. mpv starts idle, so
// the agent observes its path and puts it on standby. After a drop, the fresh
// mpv has neither the control client's observers nor the overlay, so the
// client is made to reconnect and the card redraws.
func (s *Server) mpvConnected(reconnect bool) {
	if reconnect {
		s.control.disconnect()
		s.card.forget()
	}
	s.standby.observed(nil)
	s.standby.observedLoop(nil)
	s.tone.reset()
	if err := s.backend.Send(agentCommand("observe_property", pathObserverID, "path")); err != nil {
		s.log.Printf("standby: observe mpv's path: %v", err)
	}
	if err := s.backend.Send(agentCommand("observe_property", loopObserverID, "loop-file")); err != nil {
		s.log.Printf("standby: observe mpv's loop-file: %v", err)
	}
	s.enterStandby()
}

// standbyTarget is the file and per-file options standby plays: the cinema's
// ident from the saved spec when its file is here, else the bundled System
// Ident.
func (s *Server) standbyTarget() (path, options string, err error) {
	if spec := s.state.Standby(); spec != nil && s.state.Paired() {
		if p, ok := cachedIdent(s.cfg.StateDir, spec.Ident.SHA256); ok {
			return p, spec.Ident.Options, nil
		}
	}
	path, err = ident.File(s.cfg.StateDir)
	return path, ident.Options, err
}

// enterStandby loads the standby ident in place of whatever mpv is playing,
// and makes sure it plays (a paused player stays paused across a loadfile).
func (s *Server) enterStandby() error {
	path, opts, err := s.standbyTarget()
	if err != nil {
		s.log.Printf("standby: %v", err)
		return err
	}
	// Recorded before the loadfile, so mpv cannot report the file first.
	sb := &s.standby
	sb.mu.Lock()
	sb.loaded, sb.loadedAt, sb.pending = path, time.Now(), true
	sb.mu.Unlock()
	// mpv 0.38 added the playlist index argument before the options.
	if err := s.backend.Send(agentCommand("loadfile", path, "replace", -1, titled(opts, identTitle))); err != nil {
		sb.mu.Lock()
		sb.pending = false
		sb.mu.Unlock()
		return err
	}
	return s.backend.Send(agentCommand("set_property", "pause", false))
}

// pathChanged takes mpv's path from the agent's observer, for standby and the
// test sound.
func (s *Server) pathChanged(data json.RawMessage) {
	s.standby.observed(data)
	var path string
	_ = json.Unmarshal(data, &path)
	s.tonePath(path)
}

// agentCommand is an mpv command under the agent's request id, whose reply
// is not relayed to Cinefin.
func agentCommand(args ...any) []byte {
	frame, _ := json.Marshal(map[string]any{"command": args, "request_id": agentRequestID})
	return frame
}

// identTitle names the standby ident in mpv's window title, whether it is the
// bundled System Ident or the cinema's copy of it from Cinefin.
const identTitle = "System Ident"

// titled adds force-media-title to a per-file options string, naming the file
// in mpv's window title. The title must not contain a comma.
func titled(opts, title string) string {
	if opts == "" {
		return "force-media-title=" + title
	}
	return opts + ",force-media-title=" + title
}

// cachedIdent is the downloaded ident with checksum sha, if it is here.
func cachedIdent(stateDir, sha string) (string, bool) {
	if sha == "" {
		return "", false
	}
	p := filepath.Join(stateDir, identDir, sha+".mp4")
	_, err := os.Stat(p)
	return p, err == nil
}

// fetchIdent makes sure the ident in spec is downloaded, starting a background
// download when it is not. It reports whether the file is already here.
// A newer fetch cancels an older one; a failed download keeps the previous
// file and records the error for /status.
func (s *Server) fetchIdent(spec state.StandbyIdent) (ready bool) {
	sb := &s.standby
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.cancel != nil {
		sb.cancel()
		sb.cancel = nil
	}
	sb.gen++
	sb.downloading = false
	if _, ok := cachedIdent(s.cfg.StateDir, spec.SHA256); ok {
		sb.err = ""
		pruneIdents(s.cfg.StateDir, spec.SHA256)
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), identTimeout)
	sb.cancel, sb.downloading, sb.err = cancel, true, ""
	go s.downloadIdent(ctx, cancel, sb.gen, spec)
	return false
}

func (s *Server) downloadIdent(ctx context.Context, cancel context.CancelFunc, gen int, spec state.StandbyIdent) {
	defer cancel()
	dir := filepath.Join(s.cfg.StateDir, identDir)
	tmp, err := download(ctx, spec.URL, spec.SHA256, dir)

	sb := &s.standby
	sb.mu.Lock()
	if gen != sb.gen { // superseded by a newer spec
		sb.mu.Unlock()
		if tmp != "" {
			os.Remove(tmp)
		}
		return
	}
	sb.downloading, sb.cancel = false, nil
	if err == nil {
		dst, _ := cachedIdent(s.cfg.StateDir, spec.SHA256)
		if err = os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
		} else {
			pruneIdents(s.cfg.StateDir, spec.SHA256)
		}
	}
	if err != nil {
		sb.err = err.Error()
		sb.mu.Unlock()
		s.log.Printf("standby: download the ident: %v", err)
		return
	}
	sb.err = ""
	// Decided on what the agent loaded, not only on mpv's path, which lags:
	// right after POST /standby mpv may still report the file before.
	switchNow := sb.showingStandby()
	sb.mu.Unlock()
	s.log.Printf("standby: ident %s downloaded", spec.SHA256[:12])
	// On standby with the previous ident, or about to be: show the new one.
	if switchNow {
		s.enterStandby()
	}
}

// download fetches rawURL into a temporary file in dir and checks it against
// sha. It returns the temporary file's path, which the caller renames into
// place.
func download(ctx context.Context, rawURL, sha, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The URL carries a token; keep it out of the error.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server answered %s", resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, identMaxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n > identMaxBytes:
		err = fmt.Errorf("the ident is larger than %d bytes", identMaxBytes)
	case hex.EncodeToString(h.Sum(nil)) != sha:
		err = fmt.Errorf("checksum mismatch: got %s", hex.EncodeToString(h.Sum(nil)))
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// pruneIdents removes every file in the ident directory but the ident with
// checksum keep.
func pruneIdents(stateDir, keep string) {
	dir := filepath.Join(stateDir, identDir)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != keep+".mp4" {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// resumeStandby restarts the download of a saved spec whose file is missing
// (the agent stopped mid-download, or the last attempt failed).
func (s *Server) resumeStandby() {
	if spec := s.state.Standby(); spec != nil && s.state.Paired() {
		s.fetchIdent(spec.Ident)
	}
}

// standbyOptions matches the per-file options string: comma-separated
// key=value pairs with no spaces, quotes or escapes, since it goes into a
// loadfile command as is.
var standbyOptions = regexp.MustCompile(`^([a-z0-9-]+=[A-Za-z0-9._:+-]*)(,[a-z0-9-]+=[A-Za-z0-9._:+-]*)*$`)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validateStandby checks a spec from PUT /standby, normalising the checksum.
func validateStandby(sb *state.Standby) error {
	id := &sb.Ident
	u, err := url.Parse(id.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("ident.url must be an http or https URL")
	}
	id.SHA256 = strings.ToLower(id.SHA256)
	if !sha256Hex.MatchString(id.SHA256) {
		return errors.New("ident.sha256 must be 64 hex digits")
	}
	if id.Options != "" && !standbyOptions.MatchString(id.Options) {
		return errors.New("ident.options must be comma-separated key=value pairs with no spaces or quotes")
	}
	return nil
}

// handlePutStandby saves the standby spec and fetches its ident. It answers
// 200 when the ident is already here and 202 while it downloads.
func (s *Server) handlePutStandby(w http.ResponseWriter, r *http.Request) {
	var sb state.Standby
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&sb); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := validateStandby(&sb); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.state.SetStandby(sb); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	code := http.StatusAccepted
	if s.fetchIdent(sb.Ident) {
		code = http.StatusOK
	}
	writeJSON(w, code, s.standbyStatus())
}

// handleEnterStandby puts the player on standby.
func (s *Server) handleEnterStandby(w http.ResponseWriter, _ *http.Request) {
	if !s.backend.Connected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "mpv not running"})
		return
	}
	if err := s.enterStandby(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.standbyStatus())
}

// standbyStatus describes standby for /status and the standby endpoints. The
// spec's URL carries a token and is left out.
func (s *Server) standbyStatus() map[string]any {
	out := map[string]any{"spec": nil, "file": ""}
	if spec := s.state.Standby(); spec != nil {
		out["spec"] = map[string]any{
			"ident":       map[string]any{"sha256": spec.Ident.SHA256, "options": spec.Ident.Options},
			"cinema_name": spec.CinemaName,
			"player_name": spec.PlayerName,
			"show_status": spec.ShowStatus,
		}
		if p, ok := cachedIdent(s.cfg.StateDir, spec.Ident.SHA256); ok {
			out["file"] = p
		}
	}
	sb := &s.standby
	sb.mu.Lock()
	out["downloading"] = sb.downloading
	out["error"] = sb.err
	out["on_standby"] = sb.path != "" && sb.path == sb.loaded
	sb.mu.Unlock()
	return out
}
