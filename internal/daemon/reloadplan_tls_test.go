package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The reload plan re-reads namespace.yml, where a self-signed / Let's Encrypt
// namespace stores NO cert paths — the daemon fills them in memory when it
// provisions the cert. Without resolving them the plan generates an HTTP proxy
// and tells the operator that `citeck reload` will strip ENABLE_HTTPS,
// SERVER_TLS_CERT and the 443 binding. Measured on the test stand: the proxy was
// the only app the plan wanted to recreate, on a namespace where nothing had
// changed and where a real reload provably kept HTTPS.
func TestResolveProxyTLSPathsForPlan(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CITECK_HOME", confDir)
	tlsDir := filepath.Join(confDir, "conf", "tls")
	require.NoError(t, os.MkdirAll(tlsDir, 0o755))
	certPath := filepath.Join(tlsDir, "server.crt")
	keyPath := filepath.Join(tlsDir, "server.key")

	t.Run("self-signed pair on disk is picked up", func(t *testing.T) {
		require.NoError(t, os.WriteFile(certPath, []byte("cert"), 0o600))
		require.NoError(t, os.WriteFile(keyPath, []byte("key"), 0o600))

		cfg := &namespace.Config{}
		cfg.Proxy.Host = "45.15.158.227"
		cfg.Proxy.TLS.Enabled = true

		resolveProxyTLSPathsForPlan(cfg)
		assert.Equal(t, certPath, cfg.Proxy.TLS.CertPath)
		assert.Equal(t, keyPath, cfg.Proxy.TLS.KeyPath)
	})

	t.Run("TLS off is left alone", func(t *testing.T) {
		cfg := &namespace.Config{}
		resolveProxyTLSPathsForPlan(cfg)
		assert.Empty(t, cfg.Proxy.TLS.CertPath)
	})

	t.Run("an explicitly configured path wins", func(t *testing.T) {
		cfg := &namespace.Config{}
		cfg.Proxy.TLS.Enabled = true
		cfg.Proxy.TLS.CertPath = "/operator/own.crt"
		resolveProxyTLSPathsForPlan(cfg)
		assert.Equal(t, "/operator/own.crt", cfg.Proxy.TLS.CertPath)
		assert.Empty(t, cfg.Proxy.TLS.KeyPath, "we must not invent the other half of someone else's pair")
	})

	t.Run("no cert on disk yet: nothing is invented", func(t *testing.T) {
		require.NoError(t, os.Remove(certPath))
		cfg := &namespace.Config{}
		cfg.Proxy.TLS.Enabled = true
		resolveProxyTLSPathsForPlan(cfg)
		assert.Empty(t, cfg.Proxy.TLS.CertPath,
			"the plan cannot know what an obtain would produce; a first run recreates the proxy anyway")
	})

	// It must also not create anything — the plan is read-only by contract.
	t.Run("nothing is written", func(t *testing.T) {
		require.NoError(t, os.RemoveAll(tlsDir))
		cfg := &namespace.Config{}
		cfg.Proxy.TLS.Enabled = true
		resolveProxyTLSPathsForPlan(cfg)
		_, err := os.Stat(tlsDir)
		assert.True(t, os.IsNotExist(err), "the plan must not provision a cert")
	})
}
