package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// TestBuildNamespaceConfigFromCreate_SelfSignedTLSDefaults pins that the
// create route's config builder actually invokes applySelfSignedTLSDefaults
// (rather than that helper only being covered in isolation): desktop mode
// with TLS on rewrites host/port to the ergonomic localhost:443 default,
// server mode leaves the proxy defaults untouched, and TLS off never
// rewrites anything.
func TestBuildNamespaceConfigFromCreate_SelfSignedTLSDefaults(t *testing.T) {
	baseReq := func() api.NamespaceCreateDto {
		return api.NamespaceCreateDto{
			Name:       "Citeck #1",
			AuthType:   "KEYCLOAK",
			BundleRepo: "community",
			BundleKey:  "2026.1",
		}
	}

	t.Run("desktop TLS self-signed rewrites host and port", func(t *testing.T) {
		config.SetDesktopMode(true)
		t.Cleanup(config.ResetDesktopMode)
		t.Setenv("CITECK_HOME", t.TempDir())
		d, _ := newNsCrudTestDaemon(t)

		req := baseReq()
		req.TLSEnabled = true
		req.TLSMode = "self-signed"

		cfg, err := d.buildNamespaceConfigFromCreate(req, "wsMain")
		require.NoError(t, err)
		assert.True(t, cfg.Proxy.TLS.Enabled)
		assert.False(t, cfg.Proxy.TLS.LetsEncrypt)
		assert.Equal(t, "localhost", cfg.Proxy.Host)
		assert.Equal(t, 443, cfg.Proxy.Port)
	})

	t.Run("server mode TLS enabled does not rewrite host or port", func(t *testing.T) {
		config.SetDesktopMode(false)
		t.Cleanup(config.ResetDesktopMode)
		t.Setenv("CITECK_HOME", t.TempDir())
		d, _ := newNsCrudTestDaemon(t)

		req := baseReq()
		req.TLSEnabled = true
		req.TLSMode = "self-signed"

		cfg, err := d.buildNamespaceConfigFromCreate(req, "wsMain")
		require.NoError(t, err)
		assert.True(t, cfg.Proxy.TLS.Enabled)
		assert.Equal(t, 80, cfg.Proxy.Port, "server mode must keep the pre-existing default port")
		assert.Empty(t, cfg.Proxy.Host, "server mode must not rewrite the host")
	})

	t.Run("desktop TLS disabled leaves defaults untouched", func(t *testing.T) {
		config.SetDesktopMode(true)
		t.Cleanup(config.ResetDesktopMode)
		t.Setenv("CITECK_HOME", t.TempDir())
		d, _ := newNsCrudTestDaemon(t)

		req := baseReq()
		req.TLSEnabled = false

		cfg, err := d.buildNamespaceConfigFromCreate(req, "wsMain")
		require.NoError(t, err)
		assert.False(t, cfg.Proxy.TLS.Enabled)
		assert.Equal(t, 80, cfg.Proxy.Port)
		assert.Empty(t, cfg.Proxy.Host)
	})
}
