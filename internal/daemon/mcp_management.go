package daemon

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
)

// Only unixHandlerChain can set this key. Host, RemoteAddr, CSRF, cookies and
// forwarded headers are not evidence of local management authority.
type localTransportKey struct{}

func (d *Daemon) initMCP() {
	d.mcpStore, d.mcpInitError = launchermcp.OpenStore(filepath.Join(config.ConfDir(), "mcp-private", "mcp.db"))
	if d.mcpInitError != nil {
		slog.Error("MCP private storage unavailable")
		return
	}
	d.mcp = launchermcp.New(d.mcpStore, mcpBackend{d})
	d.mcp.SetOAuthLocale(d.daemonCfg.Locale)
	if err := d.mcp.Start(); err != nil {
		slog.Warn("MCP listener unavailable", "err", err)
	}
}
func (d *Daemon) requireMCPManagement(w http.ResponseWriter, r *http.Request) bool {
	if trusted, _ := r.Context().Value(localTransportKey{}).(bool); !trusted {
		writeError(w, http.StatusForbidden, "MCP management requires the local Unix/desktop transport")
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if d.mcp == nil {
		writeError(w, http.StatusServiceUnavailable, "MCP private storage unavailable")
		return false
	}
	return true
}
func (d *Daemon) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if d.requireMCPManagement(w, r) {
		writeJSON(w, d.mcp.Status())
	}
}
func (d *Daemon) handleMCPConfigure(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	var v launchermcp.Settings
	if err := readMCPJSON(w, r, &v); err != nil {
		if !errors.Is(err, errMCPBodyHandled) {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if err := d.mcp.Configure(v); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, d.mcp.Status())
}
func (d *Daemon) handleMCPTokenCreate(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	var v struct {
		Name         string           `json:"name"`
		Role         launchermcp.Role `json:"role"`
		ExpiresAt    *time.Time       `json:"expiresAt"`
		NeverExpires bool             `json:"neverExpires"`
	}
	if err := readMCPJSON(w, r, &v); err != nil {
		if !errors.Is(err, errMCPBodyHandled) {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if v.NeverExpires && v.ExpiresAt != nil {
		writeError(w, 400, "choose expiry or neverExpires, not both")
		return
	}
	if v.ExpiresAt == nil && !v.NeverExpires {
		expiry := time.Now().UTC().Add(30 * 24 * time.Hour)
		v.ExpiresAt = &expiry
	}
	token, value, err := d.mcpStore.Create(v.Name, v.Role, v.ExpiresAt)
	if err != nil {
		if !errors.Is(err, errMCPBodyHandled) {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, struct {
		Token launchermcp.Token `json:"token"`
		Value string            `json:"value"`
	}{token, value})
}
func (d *Daemon) handleMCPTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	if err := d.mcpStore.Revoke(r.PathValue("id")); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, d.mcp.Status())
}
func readMCPJSON(w http.ResponseWriter, r *http.Request, v any) error {
	b, ok := readBodyUpTo(w, r, 16<<10)
	if !ok {
		return errMCPBodyHandled
	}
	return strictMCPJSON(b, v)
}

var errMCPBodyHandled = errors.New("MCP management body already refused")

// These owner actions share the same Unix provenance gate as manual tokens.
// Browser CSRF/auth middleware and loopback TCP are never a substitute for it.
func (d *Daemon) handleMCPOAuthPending(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	pending, err := d.mcpStore.ListOAuthPending(r.Context())
	if err != nil {
		writeError(w, 503, "MCP OAuth storage unavailable")
		return
	}
	writeJSON(w, struct {
		Pending []launchermcp.OAuthPending `json:"pending"`
	}{pending})
}
func (d *Daemon) handleMCPOAuthApprove(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	var input struct {
		Role launchermcp.Role `json:"role"`
		launchermcp.OAuthApproval
	}
	if err := readMCPJSON(w, r, &input); err != nil {
		if !errors.Is(err, errMCPBodyHandled) {
			writeError(w, 400, "MCP_OAUTH_INVALID_REQUEST")
		}
		return
	}
	if input.Role != "" && !input.Role.Valid() {
		writeError(w, 400, "MCP_OAUTH_INVALID_ROLE")
		return
	}
	if err := d.mcpStore.ApproveOAuthPending(r.Context(), r.PathValue("id"), input.Role, input.OAuthApproval); err != nil {
		if errors.Is(err, launchermcp.ErrOAuthApprovalInvalid) {
			writeError(w, 400, "MCP_OAUTH_INVALID_APPROVAL")
			return
		}
		writeError(w, 409, "MCP_OAUTH_REQUEST_UNAVAILABLE")
		return
	}
	writeJSON(w, map[string]string{"status": "approved"})
}
func (d *Daemon) handleMCPOAuthDeny(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	if err := d.mcpStore.DenyOAuthPending(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, 409, "MCP_OAUTH_REQUEST_UNAVAILABLE")
		return
	}
	writeJSON(w, map[string]string{"status": "denied"})
}
func (d *Daemon) handleMCPOAuthConnections(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	connections, err := d.mcpStore.ListOAuthConnections(r.Context())
	if err != nil {
		writeError(w, 503, "MCP OAuth storage unavailable")
		return
	}
	writeJSON(w, struct {
		Connections []launchermcp.OAuthConnection `json:"connections"`
		Warning     string                        `json:"warning,omitempty"`
	}{connections, d.mcpStore.RevocationWarning()})
}
func (d *Daemon) handleMCPOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if !d.requireMCPManagement(w, r) {
		return
	}
	if err := d.mcpStore.RevokeOAuthConnection(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, 503, "MCP_OAUTH_REVOCATION_UNCONFIRMED: "+d.mcpStore.RevocationWarning())
		return
	}
	writeJSON(w, map[string]string{"status": "revoked"})
}
