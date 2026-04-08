package daemon

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
)

// safeIDPattern allows only alphanumeric, hyphens, underscores, dots.
var safeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validateID checks that an ID is safe for use in file paths and SQL queries.
func validateID(id string) bool {
	return id != "" && len(id) <= 128 && safeIDPattern.MatchString(id) &&
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
