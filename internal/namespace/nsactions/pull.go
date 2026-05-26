// Package nsactions holds pull-specific constants and helpers shared by the
// namespace runtime's worker factories. The previous executor framework
// (PullExecutor / actions.Service) is gone — runtime_workers.go calls the
// Docker API directly on the dispatcher goroutine.
package nsactions

import (
	"strings"
	"time"
)

// PullRetryDelays defines standard pull retry delays matching Kotlin RETRY_DELAYS: [1s, 1s, 1s, 5s, 10s].
var PullRetryDelays = []time.Duration{
	time.Second, time.Second, time.Second, 5 * time.Second, 10 * time.Second,
}

// InitPullRetryDelays defines shorter delays for init container images.
var InitPullRetryDelays = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second,
}

// PullRetriesForExistingImage is the threshold after which a local image is used on pull failure.
const PullRetriesForExistingImage = 3

// IsAuthError reports whether err looks like a Docker registry auth failure
// (401/403, "unauthorized", "denied", or the wrapped "authentication failed"
// form produced by runPullTask). Exported so worker factories in
// internal/namespace can share the same classification without duplicating
// the heuristic — and so handlePullResult can recognize the wrapped pull
// error to emit `pull_auth_required` SSE events for the Web UI.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") ||
		strings.Contains(s, "403") ||
		strings.Contains(s, "unauthorized") ||
		strings.Contains(s, "denied") ||
		strings.Contains(s, "authentication failed")
}

// RegistryHost extracts the registry host from a Docker image reference.
// Exported so worker factories can reuse the same logic.
func RegistryHost(img string) string {
	// "nexus.citeck.ru/ecos-model:1.0" → "nexus.citeck.ru"
	// "docker.io/library/postgres:17" → "docker.io"
	if idx := strings.IndexByte(img, '/'); idx > 0 && strings.ContainsAny(img[:idx], ".:") {
		return img[:idx]
	}
	return "docker.io"
}
