package session

import "testing"

func TestIsServer(t *testing.T) {
	for v, want := range map[string]bool{
		":0": true, ":1.0": true, "localhost:10.0": true,
		"wayland-1": true, "/run/user/1000/wayland-1": true,
		"0": false, "1": false, "HDMI-A-1": false, "DP-2": false, "wayland": false,
	} {
		if got := IsServer(v); got != want {
			t.Errorf("IsServer(%q) = %v, want %v", v, got, want)
		}
	}
}
