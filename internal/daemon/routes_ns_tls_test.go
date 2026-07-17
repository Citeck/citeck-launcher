package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
)

func TestApplySelfSignedTLSDefaults_DesktopFillsHostAndPort(t *testing.T) {
	config.SetDesktopMode(true)
	defer config.ResetDesktopMode()

	cfg := &namespace.Config{}
	cfg.Proxy.Port = 80
	cfg.Proxy.TLS.Enabled = true

	applySelfSignedTLSDefaults(cfg)

	assert.Equal(t, "localhost", cfg.Proxy.Host)
	assert.Equal(t, 443, cfg.Proxy.Port)
}

func TestApplySelfSignedTLSDefaults_DesktopPreservesCustomHostPort(t *testing.T) {
	config.SetDesktopMode(true)
	defer config.ResetDesktopMode()

	cfg := &namespace.Config{}
	cfg.Proxy.Host = "myhost"
	cfg.Proxy.Port = 8443
	cfg.Proxy.TLS.Enabled = true

	applySelfSignedTLSDefaults(cfg)

	assert.Equal(t, "myhost", cfg.Proxy.Host)
	assert.Equal(t, 8443, cfg.Proxy.Port)
}

func TestApplySelfSignedTLSDefaults_ServerIsNoOp(t *testing.T) {
	config.SetDesktopMode(false) // explicit server mode; ignores env
	defer config.ResetDesktopMode()

	cfg := &namespace.Config{}
	cfg.Proxy.Port = 80
	cfg.Proxy.TLS.Enabled = true

	applySelfSignedTLSDefaults(cfg)

	assert.Empty(t, cfg.Proxy.Host)
	assert.Equal(t, 80, cfg.Proxy.Port)
}

func TestApplySelfSignedTLSDefaults_TLSDisabledNoChange(t *testing.T) {
	config.SetDesktopMode(true)
	defer config.ResetDesktopMode()

	cfg := &namespace.Config{}
	cfg.Proxy.Port = 80
	cfg.Proxy.TLS.Enabled = false

	applySelfSignedTLSDefaults(cfg)

	assert.Empty(t, cfg.Proxy.Host)
	assert.Equal(t, 80, cfg.Proxy.Port)
}
