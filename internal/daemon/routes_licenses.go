package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/license"
)

// licenseDTO is the wire shape returned by the licenses API. It mirrors the
// internal license.Instance but uses ISO-8601 date strings on the wire so the
// Web UI does not have to deal with Go's time.Time round-tripping rules.
//
// `Valid` is computed server-side (against the daemon's clock) so the UI can
// render expired/future licenses without re-implementing the validator.
type licenseDTO struct {
	ID         string          `json:"id"`
	Tenant     string          `json:"tenant"`
	Priority   int64           `json:"priority"`
	IssuedTo   string          `json:"issuedTo"`
	IssuedAt   string          `json:"issuedAt,omitempty"`
	ValidFrom  string          `json:"validFrom,omitempty"`
	ValidUntil string          `json:"validUntil,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	Valid      bool            `json:"valid"`
}

func toLicenseDTO(lic license.Instance) licenseDTO {
	return licenseDTO{
		ID:         lic.ID,
		Tenant:     lic.Tenant,
		Priority:   lic.Priority,
		IssuedTo:   lic.IssuedTo,
		IssuedAt:   formatLicenseDate(lic.IssuedAt.Time),
		ValidFrom:  formatLicenseDate(lic.ValidFrom.Time),
		ValidUntil: formatLicenseDate(lic.ValidUntil.Time),
		Content:    lic.Content,
		Valid:      lic.IsValid(),
	}
}

// formatLicenseDate emits ISO-8601 with no trailing T00:00:00Z for date-only
// values, matching Kotlin's LicenseDateSerializer behavior.
func formatLicenseDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	s := t.UTC().Format(time.RFC3339)
	return strings.TrimSuffix(s, "T00:00:00Z")
}

func (d *Daemon) handleListLicenses(w http.ResponseWriter, _ *http.Request) {
	licenses, err := d.licenses.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]licenseDTO, 0, len(licenses))
	for _, lic := range licenses {
		out = append(out, toLicenseDTO(lic))
	}
	writeJSON(w, out)
}

// expiringSoonDays is the window within which a still-valid license is
// flagged ExpiringSoon (amber badge in the UI, hint in the CLI).
const expiringSoonDays = 14

// handleLicenseStatus implements GET /api/v1/licenses/status — the compact
// effective-license summary for status surfaces (CLI `citeck status` line,
// dashboard indicator).
//
// A dedicated endpoint (rather than extending the namespace or daemon-status
// DTOs) is deliberate: licenses are workspace-global, not namespace state,
// and daemon-status is polled by the desktop wrapper on a hot path where a
// locked secret store must not inject errors. A read-only sibling of the
// existing /licenses collection keeps the blast radius zero for existing
// clients — older CLIs never call it, newer CLIs treat a 404 from older
// daemons as "no license info" and omit the line.
func (d *Daemon) handleLicenseStatus(w http.ResponseWriter, _ *http.Request) {
	if d.licenses == nil {
		writeJSON(w, api.LicenseStatusDto{})
		return
	}
	st, err := d.licenses.Status()
	if err != nil {
		// Locked secret store and the like — same surfacing rule as
		// handleListLicenses (don't silently pretend "community").
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, licenseStatusToDTO(st))
}

func licenseStatusToDTO(st license.StatusSummary) api.LicenseStatusDto {
	return api.LicenseStatusDto{
		Enterprise:   st.Enterprise,
		Tenant:       st.Tenant,
		IssuedTo:     st.IssuedTo,
		ValidUntil:   formatLicenseDate(st.ValidUntil),
		DaysLeft:     st.DaysLeft,
		ExpiringSoon: st.Enterprise && st.DaysLeft <= expiringSoonDays,
	}
}

func (d *Daemon) handleCreateLicense(w http.ResponseWriter, r *http.Request) {
	var lic license.Instance
	if err := json.NewDecoder(r.Body).Decode(&lic); err != nil {
		writeError(w, http.StatusBadRequest, "invalid license JSON: "+err.Error())
		return
	}
	if lic.ID == "" {
		writeError(w, http.StatusBadRequest, "license id is required")
		return
	}
	if err := d.licenses.Add(lic); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, _ := json.Marshal(toLicenseDTO(lic))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}

func (d *Daemon) handleDeleteLicense(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "license id is required")
		return
	}
	if err := d.licenses.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
