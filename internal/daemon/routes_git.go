package daemon

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// gitSyncStoreAdapter forwards git.SyncStateStore reads/writes to the
// configured storage backend. Defined here (rather than in internal/git) so
// the git package stays free of a storage import — keeping the dependency
// direction one-way (daemon depends on both; the two leaves don't see each
// other).
type gitSyncStoreAdapter struct {
	store storage.Store
}

func (a gitSyncStoreAdapter) GetGitRepoState(path string) (*git.SyncStateEntry, error) {
	s, err := a.store.GetGitRepoState(path)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	return &git.SyncStateEntry{
		Path:           s.Path,
		LastSyncMs:     s.LastSyncMs,
		LastCommitHash: s.LastCommitHash,
	}, nil
}

func (a gitSyncStoreAdapter) SetGitRepoState(entry git.SyncStateEntry) error {
	return a.store.SetGitRepoState(storage.GitRepoState{
		Path:           entry.Path,
		LastSyncMs:     entry.LastSyncMs,
		LastCommitHash: entry.LastCommitHash,
	})
}

// SkipPullRequest is the request body for POST /api/v1/git/skip-pull.
// Mirrors the GitPullErrorDialog Skip action — the frontend posts the host
// portion of the failing repo URL (so all sibling repos hosted there are
// suppressed) plus an optional override for the suppression window.
// DurationSeconds <= 0 falls back to git.DefaultSkipPullDuration (1h, Kotlin
// parity); negative values clear an existing skip for that host.
type SkipPullRequest struct {
	Host            string `json:"host"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

// handleGitSkipPull records a user "Skip" decision from GitPullErrorDialog so
// subsequent pulls against the same host no-op for the suppression window.
// Mirrors Kotlin's `skipPullForRepoDecisionAt` map (docs/porting/07 §1.9).
//
// The handler is intentionally tiny: validation lives in the host parser and
// the duration clamp, and persistence is process-local (matches Kotlin —
// the decision does not survive a daemon restart, by design).
func (d *Daemon) handleGitSkipPull(w http.ResponseWriter, r *http.Request) {
	var req SkipPullRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Host == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}

	var dur time.Duration
	switch {
	case req.DurationSeconds < 0:
		// Negative values explicitly clear the skip.
		dur = -1
	case req.DurationSeconds == 0:
		dur = git.DefaultSkipPullDuration
	default:
		dur = time.Duration(req.DurationSeconds) * time.Second
	}

	git.SkipPullForHost(req.Host, dur)

	if dur > 0 {
		slog.Info("Git pull suppressed for host", "host", req.Host, "durationSec", int(dur.Seconds()))
		writeJSON(w, api.ActionResultDto{
			Success: true,
			Message: "skip recorded",
		})
		return
	}
	slog.Info("Git pull skip cleared for host", "host", req.Host)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "skip cleared"})
}
