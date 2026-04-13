package namespace

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

func (r *Runtime) updateStats() {
	// Snapshot running apps under read lock
	r.mu.RLock()
	type appRef struct {
		name        string
		containerID string
	}
	var targets []appRef
	for _, app := range r.apps {
		if app.ContainerID != "" && app.Status == AppStatusRunning {
			targets = append(targets, appRef{name: app.Name, containerID: app.ContainerID})
		}
	}
	r.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	// Fetch stats in parallel
	type statResult struct {
		name   string
		cpu    string
		memory string
	}
	results := make([]statResult, len(targets))
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i, t := range targets {
		wg.Add(1)
		go func(idx int, ref appRef) {
			defer wg.Done()
			stats, err := r.docker.ContainerStats(ctx, ref.containerID)
			if err != nil {
				return
			}
			results[idx] = statResult{
				name:   ref.name,
				cpu:    fmt.Sprintf("%.1f%%", stats.CPUPercent),
				memory: formatMemory(stats.MemUsage, stats.MemLimit),
			}
		}(i, t)
	}
	wg.Wait()

	// Apply under write lock (brief)
	r.mu.Lock()
	for _, res := range results {
		if res.name == "" {
			continue
		}
		if app, ok := r.apps[res.name]; ok {
			app.CPU = res.cpu
			app.Memory = res.memory
		}
	}
	r.mu.Unlock()
}

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
