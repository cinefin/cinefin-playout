//go:build !linux

package hardware

// drmConnectors is Linux-only; other platforms have no /sys/class/drm.
func drmConnectors(_ string) []string {
	return []string{}
}

// ConnectedDRMConnectors is Linux-only; other platforms have none.
func ConnectedDRMConnectors() []string { return nil }
