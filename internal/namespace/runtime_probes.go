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
	anyFailed := false
	for _, app := range r.apps {
		// Skip manually-stopped apps — they are intentionally detached
		if r.manualStoppedApps[app.Name] {
			continue
		}
		if app.Status != AppStatusRunning {
			allRunning = false
		}
		if app.Status == AppStatusStartFailed || app.Status == AppStatusPullFailed {
			anyFailed = true
		}
	}
	if len(r.apps) > 0 && allRunning && r.status != NsStatusRunning {
		r.setStatus(NsStatusRunning)
	}
	if anyFailed && (r.status == NsStatusStarting || r.status == NsStatusRunning) {
		r.setStatus(NsStatusStalled)
	}
	// Recover from STALLED when failed apps have recovered
	if !anyFailed && r.status == NsStatusStalled {
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

// execHTTPProbeCheck performs the HTTP health check INSIDE the container via
// `docker exec`, hitting 127.0.0.1:<port><path> in the container's own network
// namespace. This is the correct path for server mode (no host port is published
// for non-proxy apps) and for rootless docker (the daemon's host cannot route to
// container bridge IPs) — both cases where the host-side httpProbeCheck can never
// connect. Succeeds on HTTP 200. Tries curl, then wget, then a bash /dev/tcp
// fallback, so it works across images that ship any one of them (the ecos Java
// images carry bash; the infra images carry curl/wget).
func (r *Runtime) execHTTPProbeCheck(ctx context.Context, containerID string, port int, path string, timeoutSec int) bool {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	script := fmt.Sprintf(
		"curl -fsS -m %d '%s' >/dev/null 2>&1 && exit 0\n"+
			"wget -q -T %d -O /dev/null '%s' >/dev/null 2>&1 && exit 0\n"+
			"command -v bash >/dev/null 2>&1 && bash -c 'exec 3<>/dev/tcp/127.0.0.1/%d || exit 1; "+
			"printf \"GET %s HTTP/1.0\\r\\nHost: localhost\\r\\n\\r\\n\" >&3; "+
			"IFS= read -r l <&3; case \"$l\" in *\" 200 \"*) exit 0;; esac; exit 1' && exit 0\n"+
			"exit 1",
		timeoutSec, url, timeoutSec, url, port, path)
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+3)*time.Second)
	defer cancel()
	_, exitCode, err := r.docker.ExecInContainer(execCtx, containerID, []string{"/bin/sh", "-c", script}) //nolint:gosec // probe target derived from internal app config, not user input
	return err == nil && exitCode == 0
}

// probeHTTP runs an HTTP health check against a container, choosing the reachable
// path: a host-published port (desktop mode) is probed from the host on
// 127.0.0.1; otherwise (server mode publishes no host port) the check runs INSIDE
// the container via docker exec — the only path that works under rootless docker,
// where the daemon's host cannot route to container bridge IPs. The container-IP
// host probe is kept as a last-resort fallback for rootful hosts whose daemon can
// reach the bridge network. Shared by the startup and liveness probes.
func (r *Runtime) probeHTTP(ctx context.Context, containerID string, publishedPort, containerPort int, path string, timeoutSec int) bool {
	if publishedPort > 0 {
		return httpProbeCheck(ctx, "127.0.0.1", publishedPort, path, timeoutSec)
	}
	if r.execHTTPProbeCheck(ctx, containerID, containerPort, path, timeoutSec) {
		return true
	}
	if ip := r.docker.GetContainerIP(ctx, containerID); ip != "" {
		return httpProbeCheck(ctx, ip, containerPort, path, timeoutSec)
	}
	return false
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
