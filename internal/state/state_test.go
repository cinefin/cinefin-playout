package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cinefin/cinefin-playout/internal/hostconfig"
)

// A fresh directory opens empty with preset defaults, and values set on one
// Store are read back by the next Open.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Token() != "" {
		t.Errorf("fresh token = %q", s.Token())
	}
	if got, want := s.Launch().Graphics.VO, hostconfig.Detect().Graphics.VO; got != want {
		t.Errorf("fresh launch VO = %q, want preset %q", got, want)
	}

	hc := hostconfig.Default()
	hc.Graphics.Screen = 2
	hc.Audio.Device = "alsa/hdmi"
	if err := s.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	if s.HasLaunch() {
		t.Error("HasLaunch before SetLaunch")
	}
	if err := s.SetLaunch(hc); err != nil {
		t.Fatal(err)
	}
	if !s.HasLaunch() {
		t.Error("HasLaunch after SetLaunch = false")
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Token() != "tok" {
		t.Errorf("reloaded token = %q", again.Token())
	}
	if l := again.Launch(); l.Graphics.Screen != 2 || l.Audio.Device != "alsa/hdmi" {
		t.Errorf("reloaded launch = %+v", l)
	}
	if fi, err := os.Stat(filepath.Join(dir, FileName)); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("state file must exist and be private: %v %v", fi.Mode(), err)
	}
}

// A corrupt file is an error, not a silent reset.
func TestOpenCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected an error for a corrupt state file")
	}
}

// Reset unpairs and drops the launch config but keeps the player ID.
func TestResetKeepsID(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.ID()
	if err != nil || len(id) != 16 {
		t.Fatalf("ID = %q, %v", id, err)
	}
	hc := hostconfig.Default()
	hc.Graphics.Screen = 3
	if err := s.SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLaunch(hc); err != nil {
		t.Fatal(err)
	}
	if !s.Paired() {
		t.Fatal("Paired() = false with a token set")
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Paired() || again.Launch().Graphics.Screen == 3 {
		t.Errorf("Reset left pairing or launch config behind")
	}
	if id2, _ := again.ID(); id2 != id {
		t.Errorf("ID changed across Reset: %q -> %q", id, id2)
	}
}

// Cinefin's address survives a reload and is forgotten on Reset.
func TestCinefinAddressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.CinefinAddress() != "" {
		t.Errorf("fresh address = %q", s.CinefinAddress())
	}
	if err := s.SetCinefinAddress("192.168.1.20"); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.CinefinAddress(); got != "192.168.1.20" {
		t.Errorf("reloaded address = %q", got)
	}
	if err := again.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := again.CinefinAddress(); got != "" {
		t.Errorf("address after Reset = %q", got)
	}
}

// The standby spec survives a reopen and is forgotten on unpairing.
func TestStandbyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if s.Standby() != nil {
		t.Fatal("fresh state has a standby spec")
	}
	want := Standby{
		Ident:      StandbyIdent{URL: "http://c/ident?t=x", SHA256: "ab", Options: "end=4"},
		CinemaName: "The Roxy", PlayerName: "Screen 1", ShowStatus: true,
	}
	if err := s.SetStandby(want); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Standby(); got == nil || *got != want {
		t.Fatalf("reopened spec = %+v, want %+v", got, want)
	}
	if err := s2.Reset(); err != nil || s2.Standby() != nil {
		t.Errorf("after reset: %+v (err %v)", s2.Standby(), err)
	}
}
