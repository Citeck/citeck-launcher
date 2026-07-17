package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// TestNamespaceEdit_SelfSignedTLSDefaults pins that the EDIT route (not just
// the create route) invokes applySelfSignedTLSDefaults: in desktop mode,
// enabling TLS on a namespace whose proxy still has the default empty
// host / port 80 rewrites them to the ergonomic localhost:443 default, and
// never turns on Let's Encrypt as a side effect.
func TestNamespaceEdit_SelfSignedTLSDefaults(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)

	require.NoError(t, d.persistNamespaceConfig("wsMain", "nsedittls", []byte(
		"id: nsedittls\nname: Editable\nbundleRef: community:LATEST\n"+
			"authentication:\n  type: BASIC\n  users: [admin]\nproxy:\n  port: 80\n")))

	body := `{"tlsEnabled":true}`
	httpReq := httptest.NewRequest("PUT", api.NamespaceEditPath("nsedittls"), strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httpReq)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	stored, err := d.loadNamespaceConfigFromStore("wsMain", "nsedittls")
	require.NoError(t, err)
	assert.True(t, stored.Proxy.TLS.Enabled)
	assert.False(t, stored.Proxy.TLS.LetsEncrypt, "edit must never enable Let's Encrypt as a side effect")
	assert.Equal(t, "localhost", stored.Proxy.Host)
	assert.Equal(t, 443, stored.Proxy.Port)
}
