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

// sanitizeName converts a human name to a safe filesystem ID.
func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('-')
		}
	}
	id := b.String()
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

func (d *Daemon) activeNsID() string {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	if d.nsConfig != nil {
		return d.nsConfig.ID
	}
	return ""
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

// requireRuntime checks that a namespace runtime exists. Returns false and writes
// an error response if not configured. Callers should return immediately when false.
func (d *Daemon) requireRuntime(w http.ResponseWriter) bool {
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return false
	}
	return true
}

func (d *Daemon) volumesDir() string {
	return filepath.Join(d.volumesBase, "volumes")
}
