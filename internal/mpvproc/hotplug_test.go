package mpvproc

import "testing"

func TestConnectorEdge(t *testing.T) {
	cases := []struct {
		name      string
		prev      map[string]string
		cur       map[string]string
		connector string
		want      bool
	}{
		{
			name:      "disconnected to connected is an edge",
			prev:      map[string]string{"HDMI-A-1": "disconnected"},
			cur:       map[string]string{"HDMI-A-1": "connected"},
			connector: "HDMI-A-1",
			want:      true,
		},
		{
			name:      "stays connected is not an edge",
			prev:      map[string]string{"HDMI-A-1": "connected"},
			cur:       map[string]string{"HDMI-A-1": "connected"},
			connector: "HDMI-A-1",
			want:      false,
		},
		{
			name:      "connected to disconnected is not an edge",
			prev:      map[string]string{"HDMI-A-1": "connected"},
			cur:       map[string]string{"HDMI-A-1": "disconnected"},
			connector: "HDMI-A-1",
			want:      false,
		},
		{
			name:      "stays disconnected is not an edge",
			prev:      map[string]string{"HDMI-A-1": "disconnected"},
			cur:       map[string]string{"HDMI-A-1": "disconnected"},
			connector: "HDMI-A-1",
			want:      false,
		},
		{
			name:      "first observation (nil prev) never edges",
			prev:      nil,
			cur:       map[string]string{"HDMI-A-1": "connected"},
			connector: "HDMI-A-1",
			want:      false,
		},
		{
			name:      "newly-appeared connected connector edges (had a prior snapshot)",
			prev:      map[string]string{"DP-1": "connected"},
			cur:       map[string]string{"DP-1": "connected", "HDMI-A-1": "connected"},
			connector: "HDMI-A-1",
			want:      true,
		},
		{
			name:      "other connector's change does not edge ours",
			prev:      map[string]string{"HDMI-A-1": "connected", "DP-1": "disconnected"},
			cur:       map[string]string{"HDMI-A-1": "connected", "DP-1": "connected"},
			connector: "HDMI-A-1",
			want:      false,
		},
		{
			name:      "unknown to connected edges",
			prev:      map[string]string{"HDMI-A-1": "unknown"},
			cur:       map[string]string{"HDMI-A-1": "connected"},
			connector: "HDMI-A-1",
			want:      true,
		},
		{
			name:      "missing in cur is not an edge",
			prev:      map[string]string{"HDMI-A-1": "connected"},
			cur:       map[string]string{},
			connector: "HDMI-A-1",
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectorEdge(tc.prev, tc.cur, tc.connector); got != tc.want {
				t.Fatalf("connectorEdge = %v, want %v", got, tc.want)
			}
		})
	}
}
