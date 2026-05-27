package desktop

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// notifyExistingInstanceTimeout caps the whole dial+HTTP round trip. Short
// because the call happens on the second-launch path where the user is waiting
// for either focus-hand-off or an error; a hung daemon must not block.
const notifyExistingInstanceTimeout = 2 * time.Second

// NotifyExistingInstance dials the running daemon's Unix socket and posts to
// /desktop/focus so the existing instance raises its main window. Returns
// nil on HTTP 2xx. Any dial/HTTP/status error is returned so the caller can
// fall back to its previous behavior (stale lock without a live daemon must
// still surface as an error rather than silent success).
func NotifyExistingInstance(socketPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), notifyExistingInstanceTimeout)
	defer cancel()
	return notifyExistingInstanceCtx(ctx, socketPath)
}

func notifyExistingInstanceCtx(ctx context.Context, socketPath string) error {
	client := &http.Client{
		Timeout: notifyExistingInstanceTimeout,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://desktop/desktop/focus", http.NoBody)
	if err != nil {
		return fmt.Errorf("build focus request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dial daemon socket: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("focus endpoint returned %d", resp.StatusCode)
	}
	return nil
}

