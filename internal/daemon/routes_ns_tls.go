// Package daemon holds proxy TLS defaulting glue for the namespace routes.
package daemon

import (
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// applySelfSignedTLSDefaults fills ergonomic proxy defaults when the desktop
// UI turns on self-signed HTTPS: a localhost host (validation requires a
// non-empty host when TLS is on) and port 443 (so https://localhost works).
// A custom host/port is preserved. No-op in server mode — the server keeps its
// existing behavior (self-signed remains a plain fallback with no rewriting).
func applySelfSignedTLSDefaults(nsCfg *namespace.Config) {
	if !config.IsDesktopMode() || !nsCfg.Proxy.TLS.Enabled {
		return
	}
	if nsCfg.Proxy.Host == "" {
		nsCfg.Proxy.Host = "localhost"
	}
	if nsCfg.Proxy.Port == 80 {
		nsCfg.Proxy.Port = 443
	}
}
