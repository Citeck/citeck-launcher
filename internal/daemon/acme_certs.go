package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
)

// Proxy TLS certificate provisioning: Let's Encrypt (ACME) with a self-signed
// fallback. Shared by initial namespace load and reload via ensureProxyTLSCerts.

// ensureProxyTLSCerts runs the full cert-provisioning step for a namespace's
// proxy: self-signed generation when TLS is on without LE, or an ACME obtain
// when LE is enabled. Returns the acme.Client when LE was attempted (so the
// caller can build a renewal service), or nil otherwise. contextLabel
// distinguishes Start vs reload flows in log messages (e.g. "on reload").
func ensureProxyTLSCerts(nsCfg *namespace.Config, contextLabel string) *acme.Client {
	ensureSelfSignedCert(nsCfg)
	return ensureACMECert(nsCfg, contextLabel)
}

// ensureACMECert obtains or refreshes the Let's Encrypt certificate for the
// proxy when TLS + LE are enabled, then wires nsCfg.Proxy.TLS.{CertPath,KeyPath}
// to the resulting cert. Falls back to generating a self-signed cert if LE
// fails and no usable cert is present. Returns the acme.Client when LE was
// attempted (so the caller can build a renewal service), or nil otherwise.
//
// contextLabel is appended to log messages to distinguish Start vs reload flows
// (e.g. "on reload"); pass "" to suppress.
func ensureACMECert(nsCfg *namespace.Config, contextLabel string) *acme.Client {
	if !nsCfg.Proxy.TLS.Enabled || !nsCfg.Proxy.TLS.LetsEncrypt {
		return nil
	}
	host := nsCfg.Proxy.Host
	if host == "" || host == "localhost" {
		slog.Warn("Let's Encrypt requires a public hostname, skipping", "host", host, "context", contextLabel)
		return nil
	}
	acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), host)
	acmeErr := obtainACMECertIfNeeded(acmeClient, host, contextLabel)
	if acmeClient.CertMatchesHost() {
		nsCfg.Proxy.TLS.CertPath = acmeClient.CertPath()
		nsCfg.Proxy.TLS.KeyPath = acmeClient.KeyPath()
	}
	if nsCfg.Proxy.TLS.CertPath == "" {
		slog.Warn("Let's Encrypt cert not available, falling back to self-signed", "reason", acmeErr, "context", contextLabel)
		generateSelfSignedCertForConfig(nsCfg)
	}
	return acmeClient
}

// obtainACMECertIfNeeded drives a single LE obtain attempt for `acmeClient`
// when the on-disk cert doesn't match the host. Honors the persisted rate-limit
// marker (written by RenewalService on LE 429 / "too many" errors). Returns
// any obtain error (or nil if the cert is already good or rate-limit was the
// reason to skip).
func obtainACMECertIfNeeded(acmeClient *acme.Client, host, contextLabel string) error {
	if acmeClient.CertMatchesHost() {
		return nil
	}
	if limited, retryAfter, rlErr := acme.IsRateLimited(config.DataDir(), host); rlErr == nil && limited {
		slog.Warn("Let's Encrypt rate-limit marker active, skipping obtain", "host", host, "retryAfter", retryAfter, "context", contextLabel)
		return fmt.Errorf("rate-limited until %s", retryAfter.Format(time.RFC3339))
	}
	label := "Obtaining Let's Encrypt certificate"
	if contextLabel != "" {
		label += " " + contextLabel
	}
	slog.Info(label, "host", host)
	acmeCtx, acmeCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer acmeCancel()
	if err := acmeClient.ObtainCertificate(acmeCtx); err != nil {
		slog.Error("Let's Encrypt certificate obtainment failed", "err", err, "context", contextLabel)
		return fmt.Errorf("obtain LE certificate: %w", err)
	}
	slog.Info("Let's Encrypt certificate obtained", "cert", acmeClient.CertPath())
	return nil
}

// ensureSelfSignedCert generates a self-signed cert if TLS is enabled without LE and no cert is configured.
func ensureSelfSignedCert(nsCfg *namespace.Config) {
	if !nsCfg.Proxy.TLS.Enabled || nsCfg.Proxy.TLS.LetsEncrypt || nsCfg.Proxy.TLS.CertPath != "" {
		return
	}
	generateSelfSignedCertForConfig(nsCfg)
}

// selfSignedCertHosts returns the SAN list for the self-signed proxy cert.
// In desktop mode a loopback/empty host expands to localhost + 127.0.0.1 + ::1
// so both https://localhost and https://127.0.0.1 validate. Server mode (and a
// real hostname) keep the single-host behavior unchanged.
func selfSignedCertHosts(host string) []string {
	if config.IsDesktopMode() && (host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return []string{"localhost", "127.0.0.1", "::1"}
	}
	if host == "" {
		host = "localhost"
	}
	return []string{host}
}

// generateSelfSignedCertForConfig generates a self-signed cert and updates the config paths.
// Called directly as LE fallback (bypassing the LetsEncrypt guard in ensureSelfSignedCert).
func generateSelfSignedCertForConfig(nsCfg *namespace.Config) {
	hosts := selfSignedCertHosts(nsCfg.Proxy.Host)
	tlsDir := filepath.Join(config.ConfDir(), "tls")
	os.MkdirAll(tlsDir, 0o755) //nolint:gosec // G301: TLS dir needs 0o755
	certPath := filepath.Join(tlsDir, "server.crt")
	keyPath := filepath.Join(tlsDir, "server.key")
	if !isRegularFile(certPath) {
		slog.Info("Generating self-signed certificate", "hosts", hosts)
		if err := tlsutil.GenerateSelfSignedCert(certPath, keyPath, hosts, 365); err != nil {
			slog.Error("Failed to generate self-signed cert", "err", err)
		}
	}
	nsCfg.Proxy.TLS.CertPath = certPath
	nsCfg.Proxy.TLS.KeyPath = keyPath
}

// isRegularFile returns true if path exists and is a regular file.
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
