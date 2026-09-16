package namespace

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (r *Runtime) checkStatus() {
	if r.status != NsStatusStarting && r.status != NsStatusRunning && r.status != NsStatusStalled {
		return
	}
	allRunning := true
	// anyStuck: something is wrong AND it will not resolve itself. That is what
	// separates the two statuses — RUNNING says the namespace is whole and
	// usable, STALLED says a problem arose that needs a person. A failed app is
	// the original member; an app HELD by a dependency the user detached is the
	// other, and it is not a lesser one: nothing in the namespace will release
	// it, so leaving such a namespace in STARTING pinned it there forever and
	// took the reconciler and every liveness probe down with it (both are gated
	// on RUNNING/STALLED).
	anyStuck := false
	for _, app := range r.apps {
		// Skip manually-stopped apps — they are intentionally detached
		if r.isDetachedLocked(app.Name) {
			continue
		}
		if app.Status != AppStatusRunning {
			allRunning = false
		}
		if app.Status == AppStatusStartFailed || app.Status == AppStatusPullFailed {
			anyStuck = true
		}
		if r.heldByDetachedDepsUnderLock(app) {
			anyStuck = true
		}
	}
	if len(r.apps) > 0 && allRunning && r.status != NsStatusRunning {
		r.setStatus(NsStatusRunning)
	}
	if anyStuck && (r.status == NsStatusStarting || r.status == NsStatusRunning) {
		r.setStatus(NsStatusStalled)
	}
	// Recover from STALLED once nothing is stuck any more — a failed app came
	// back, or the operator started the dependency that was holding one.
	if !anyStuck && r.status == NsStatusStalled {
		r.setStatus(NsStatusStarting)
	}
}

func formatMemory(usage, limit int64) string {
	if limit <= 0 {
		return formatBytes(usage)
	}
	return fmt.Sprintf("%s / %s", formatBytes(usage), formatBytes(limit))
}

// probeClient is a shared HTTP client for health probes.
// Reuses connections across probe invocations. Timeouts are set per-request via context.
var probeClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	},
}

func httpProbeCheck(ctx context.Context, host string, port int, path string, timeoutSec int) bool {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	if host == "" {
		host = "127.0.0.1"
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, "GET", fmt.Sprintf("http://%s:%d%s", host, port, path), http.NoBody)
	if err != nil {
		return false
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

func formatBytes(b int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fG", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%dM", b/mb)
	default:
		return fmt.Sprintf("%dK", b/1024)
	}
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
