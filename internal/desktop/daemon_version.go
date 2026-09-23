package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
)

// TitleVersionTimeout bounds how long the wrapper waits for a (re)started
// daemon to report its version for the window title. Longer than the health
// gates: the title is cosmetic and nothing waits on it, but giving up early is
// exactly the bug this wait exists for.
const TitleVersionTimeout = 2 * time.Minute

// RunningDaemonVersion asks the running daemon for its version, WAITING while
// it is still booting, until ctx ends.
//
// It has to wait. The wrapper asks right after a daemon restart (the
// auto-update swap, a rollback), and a restart counts as done on LIVENESS: the
// daemon binds its socket before its slow boot phase, and until that phase is
// over every API route answers 503 DAEMON_STARTING. Asking once and giving up
// on that answer is how the window kept the OLD version in its title after an
// auto-update until the launcher was restarted by hand. Any failure — a refused
// connection, a non-2xx answer, an unreadable body — is retried every `poll`.
//
// baseURL is "http://daemon" for the unix-socket client the wrapper uses.
// A "dev-" prefix is dropped: the title shows the build stamp.
func RunningDaemonVersion(ctx context.Context, client *http.Client, baseURL string, poll time.Duration) (string, error) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var last error
	for {
		ver, err := daemonVersionOnce(ctx, client, baseURL)
		if err == nil {
			return ver, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("daemon version not available: %w", errors.Join(ctx.Err(), last))
		case <-ticker.C:
		}
	}
}

func daemonVersionOnce(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+api.DaemonStatus, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build status request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch daemon status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("daemon status: HTTP %d", resp.StatusCode)
	}
	var st api.DaemonStatusDto
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "", fmt.Errorf("decode daemon status: %w", err)
	}
	ver := strings.TrimPrefix(st.Version, "dev-")
	if ver == "" {
		return "", errors.New("daemon status carries no version")
	}
	return ver, nil
}
