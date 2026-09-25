package mpvproc

import (
	"context"
	"encoding/json"
	"time"

	"git.kef2.net/micky/cinefin-playout/internal/mpvipc"
)

// sendQuit sends {"command":["quit"]} to mpv over its IPC endpoint. Best-effort;
// errors (mpv gone) are ignored — the caller then falls back to signals.
func sendQuit(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	c := mpvipc.New(path)
	if err := c.Connect(ctx); err != nil {
		return
	}
	defer c.Close()
	frame, _ := json.Marshal(map[string]any{"command": []any{"quit"}})
	_ = c.Send(frame)
	// Give mpv a moment to receive it before we close.
	time.Sleep(100 * time.Millisecond)
}
