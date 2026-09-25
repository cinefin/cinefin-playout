package mpvproc

// connectorEdge reports whether the given connector went through a
// disconnected→connected transition between two /sys/class/drm status snapshots.
//
// This is the pure, testable core of the DRM re-modeset logic (retiring
// cinefin PR #235): when a display returns, mpv needs to re-acquire the VT and
// re-modeset. We detect the *rising edge* only — a display that stays connected,
// or one that disconnects, produces no signal. An absent-then-present connector
// (map key appears) also counts as a rising edge if its new status is connected.
func connectorEdge(prev, cur map[string]string, connector string) bool {
	curStatus, curOK := cur[connector]
	if !curOK || !isConnected(curStatus) {
		return false
	}
	prevStatus, prevOK := prev[connector]
	if !prevOK {
		// Newly-appeared connector that is connected: treat as an edge only if
		// we had a previous snapshot at all (nil prev = first observation).
		return prev != nil
	}
	return !isConnected(prevStatus)
}

func isConnected(status string) bool {
	return status == "connected"
}
