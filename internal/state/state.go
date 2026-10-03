// Package state is the agent's persistent state: a stable player ID, the bearer
// token Cinefin received when it paired, and the mpv launch config Cinefin has
// set. It lives in one JSON file, <state_dir>/state.json, owned by the agent.
// Users never edit it: pairing sets the token, Cinefin sets the launch config
// over PUT /hostconfig, and the agent keeps a copy so the box still boots and
// starts mpv when Cinefin is offline. A player with no token is unpaired.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// FileName is the state file inside the state directory.
const FileName = "state.json"

// data is the on-disk shape. Launch is nil until Cinefin first sets it.
type data struct {
	ID     string                 `json:"id"`
	Token  string                 `json:"token,omitempty"`
	Launch *hostconfig.HostConfig `json:"launch,omitempty"`
}

// Store is the loaded state, safe for concurrent use. Every setter writes the
// file through before returning.
type Store struct {
	path string

	mu sync.Mutex
	d  data
}

// Open loads the state from dir, or starts empty when there is no file yet. A
// file that exists but cannot be parsed is an error rather than silently reset,
// so a corrupt file never unpairs the host by accident.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName)}
	raw, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return s, nil
}

// ID is the player's stable identifier, generated on first use and kept across
// unpairing, so Cinefin can recognise a player whose address changed.
func (s *Store) ID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.ID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		s.d.ID = hex.EncodeToString(b)
		if err := s.saveLocked(); err != nil {
			s.d.ID = ""
			return "", err
		}
	}
	return s.d.ID, nil
}

// Paired reports whether a Cinefin has paired with this player.
func (s *Store) Paired() bool { return s.Token() != "" }

// Reset forgets the pairing and the launch config Cinefin set, returning the
// player to its unpaired state. The ID is kept.
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Token = ""
	s.d.Launch = nil
	return s.saveLocked()
}

// Path is the state file's location.
func (s *Store) Path() string { return s.path }

// Token is the bearer token Cinefin must present ("" when none is set).
func (s *Store) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Token
}

// SetToken stores the bearer token.
func (s *Store) SetToken(tok string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Token = tok
	return s.saveLocked()
}

// Launch is the mpv launch config: the one Cinefin set, or the defaults for
// this machine when it has not set one yet.
func (s *Store) Launch() hostconfig.HostConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Launch == nil {
		return hostconfig.Detect()
	}
	return *s.d.Launch
}

// HasLaunch reports whether Cinefin has set a launch config yet.
func (s *Store) HasLaunch() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Launch != nil
}

// SetLaunch stores the mpv launch config. It applies on the next player start.
func (s *Store) SetLaunch(hc hostconfig.HostConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Launch = &hc
	return s.saveLocked()
}

// saveLocked writes the state atomically (temp file + rename), 0600 because it
// holds the token. Caller holds s.mu.
func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
