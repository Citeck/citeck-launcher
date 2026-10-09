package daemon

import (
	"net"
	"net/http"
	"net/url"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// Local environment export is an explicit credential read over the same
// owner-only/authenticated API as app config. It is never offered on a server,
// logged, cached or written into generated runtime files/snapshots.
func (d *Daemon) handleContentStorageLocalEnv(w http.ResponseWriter, r *http.Request) {
	say := func(status int, key string) { writeError(w, status, d.translatorFor(r).T(key)) }
	if !config.IsDesktopMode() {
		say(http.StatusForbidden, "contentStorage.msg.local.desktopOnly")
		return
	}
	act := d.active()
	if act.nsConfig == nil || act.runtime == nil || act.nsConfig.ContentStorageType() != namespace.ContentStorageS3 {
		say(http.StatusConflict, "contentStorage.msg.local.notConfigured")
		return
	}
	s3 := act.workspaceConfig.ContentStorageS3()
	if s3 == nil {
		say(http.StatusConflict, "contentStorage.msg.local.notConfigured")
		return
	}
	provider, err := url.Parse(s3.URL)
	if err != nil || provider.Port() != "8333" || provider.Scheme != "http" {
		say(http.StatusConflict, "contentStorage.msg.local.notConfigured")
		return
	}
	providerApp := act.runtime.FindApp(s3.App)
	if providerApp == nil || providerApp.Status != namespace.AppStatusRunning {
		say(http.StatusConflict, "contentStorage.msg.local.providerNotReady")
		return
	}
	address, err := act.runtime.AppHTTPAddress(r.Context(), s3.App, 8333)
	if err != nil {
		say(http.StatusConflict, "contentStorage.msg.local.providerNotReady")
		return
	}
	host, _, addressErr := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if addressErr != nil || ip == nil || !ip.IsLoopback() {
		say(http.StatusConflict, "contentStorage.msg.local.providerNotReady")
		return
	}
	content, ok := runtimeAppDef(act.runtime, appdef.AppContent)
	if !ok {
		say(http.StatusConflict, "contentStorage.msg.local.notConfigured")
		return
	}
	env := map[string]string{}
	for _, key := range []string{
		"ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL",
		"ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_CREDENTIALS",
		"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_TYPE",
		"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME",
		"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD",
	} {
		value, exists := content.Environments.Get(key)
		if !exists || value == "" {
			say(http.StatusConflict, "contentStorage.msg.local.notConfigured")
			return
		}
		env[key] = value
	}
	env["ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL"] = "http://" + address
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, env)
}
