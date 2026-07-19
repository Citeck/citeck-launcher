// Package daemon holds proxy TLS defaulting glue for the namespace routes.
package daemon

import (
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// applySelfSignedTLSDefaults fills the ergonomic proxy host default when the
// desktop UI turns on self-signed HTTPS: a localhost host (validation
// requires a non-empty host when TLS is on). A custom host is preserved.
// The published port is NOT rewritten here — 80/443 are scheme defaults
// derived from TLS state at consumption time via EffectiveProxyPort, so a
// stored port of 80 correctly renders as the effective 443 once TLS is on
// (and self-corrects back to 80 if TLS is later disabled). No-op in server
// mode — the server keeps its existing behavior (self-signed remains a plain
// fallback with no rewriting).
func applySelfSignedTLSDefaults(nsCfg *namespace.Config) {
	if !config.IsDesktopMode() || !nsCfg.Proxy.TLS.Enabled {
		return
	}
	if nsCfg.Proxy.Host == "" {
		nsCfg.Proxy.Host = "localhost"
	}
}
