package daemon

import (
	"crypto/rand"
	"encoding/base32"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// generateEntityID mirrors Kotlin's IdUtils.createStrId: 4 random bytes →
// lowercase base32 with the padding stripped (≈7 chars, e.g. "3v3ithq").
// Entity IDs are opaque to the user — the human-facing Name is stored
// separately. Two separate concerns, two separate fields.
func generateEntityID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand on Linux can only fail catastrophically; surface as
		// the empty string so the caller can reject the request rather than
		// silently writing zero-entropy IDs.
		return ""
	}
	enc := strings.ToLower(base32.StdEncoding.EncodeToString(b[:]))
	return strings.TrimRight(enc, "=")
}

// safeIDPattern allows only alphanumeric, hyphens, underscores, dots.
var safeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// safeSecretIDPattern additionally allows ':' so scoped secret keys
// ("images-repo:harbor.citeck.ru", "ws:<wsID>:repo") round-trip through the
// API. Secrets IDs are stored as SQLite PKs (parameterized queries, safe) and
// never used as file paths, so the broader char set is fine.
var safeSecretIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]*$`)

// validateID checks that an ID is safe for use in file paths and SQL queries.
func validateID(id string) bool {
	return id != "" && len(id) <= 128 && safeIDPattern.MatchString(id) &&
		!strings.Contains(id, "..") && !strings.ContainsAny(id, "/\\")
}

// validateSecretID is the secret-key-specific validator. Mirrors validateID
// but also permits ':' which the Kotlin parity scheme uses to namespace
// secrets by purpose (images-repo:<host>, ws:<wsID>:repo, etc.).
func validateSecretID(id string) bool {
	return id != "" && len(id) <= 128 && safeSecretIDPattern.MatchString(id) &&
		!strings.Contains(id, "..") && !strings.ContainsAny(id, "/\\")
}

// Single-field convenience accessors over the d.active() snapshot. Fine for
// handlers that need exactly ONE per-namespace field; a handler reading 2+
// fields should take one d.active() snapshot itself so all fields come from
// the same lock acquisition (consistent view).

func (d *Daemon) activeNsID() string {
	if cfg := d.active().nsConfig; cfg != nil {
		return cfg.ID
	}
	return ""
}

// activeWorkspaceID returns the active workspace ID. workspaceID is swapped
// by SwitchWorkspace under configMu — read it via the snapshot, never lock-free.
func (d *Daemon) activeWorkspaceID() string {
	return d.active().workspaceID
}

// activeDockerClient returns the Docker client scoped to the active
// (workspace, namespace) pair. Read-only accessor: the invariant "active
// client always matches the active namespace" is derived and validated at
// the loadNamespace choke-point and asserted by installLoadedNamespace —
// never re-derive or swap the client here.
func (d *Daemon) activeDockerClient() *docker.Client {
	return d.active().dockerClient
}

// activeVolumesBase returns the active namespace's rtfiles/volumes base dir
// ("" when no namespace is loaded).
func (d *Daemon) activeVolumesBase() string {
	return d.active().volumesBase
}

// parseTailParam reads the "tail" query parameter with a default and max cap.
func parseTailParam(r *http.Request, defaultVal, maxVal int) int {
	tailStr := r.URL.Query().Get("tail")
	tail := defaultVal
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}
	if tail < 0 {
		tail = defaultVal
	}
	if tail > maxVal {
		tail = maxVal
	}
	return tail
}

// requireRuntime returns the active namespace runtime, or nil after writing
// an error response when no namespace is configured. Callers must return
// immediately on nil and use the RETURNED runtime (one consistent snapshot)
// rather than re-reading the active state.
func (d *Daemon) requireRuntime(w http.ResponseWriter) *namespace.Runtime {
	rt := d.active().runtime
	if rt == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
	}
	return rt
}

func (d *Daemon) volumesDir() string {
	return filepath.Join(d.activeVolumesBase(), "volumes")
}
