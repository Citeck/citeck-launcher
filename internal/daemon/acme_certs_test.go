package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestSelfSignedCertHosts_DesktopLocalhostExpandsSANs(t *testing.T) {
	config.SetDesktopMode(true)
	defer config.ResetDesktopMode()

	for _, h := range []string{"", "localhost", "127.0.0.1", "::1"} {
		assert.Equal(t, []string{"localhost", "127.0.0.1", "::1"}, selfSignedCertHosts(h),
			"host %q should expand to loopback SANs in desktop mode", h)
	}
}

func TestSelfSignedCertHosts_DesktopRealHostUnchanged(t *testing.T) {
	config.SetDesktopMode(true)
	defer config.ResetDesktopMode()

	assert.Equal(t, []string{"example.com"}, selfSignedCertHosts("example.com"))
}

func TestSelfSignedCertHosts_ServerSingleHost(t *testing.T) {
	config.SetDesktopMode(false)
	defer config.ResetDesktopMode()

	assert.Equal(t, []string{"localhost"}, selfSignedCertHosts(""))
	assert.Equal(t, []string{"localhost"}, selfSignedCertHosts("localhost"))
	assert.Equal(t, []string{"example.com"}, selfSignedCertHosts("example.com"))
}
