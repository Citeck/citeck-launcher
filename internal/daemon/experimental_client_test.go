package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `citeck features enable|disable` sends the typed edit with ONE field set —
// the client marshals the rest as zero values — and the real handler has to
// read that as "change nothing else". This drives the real client over a unix
// socket against the production mux, so the CLI's request shape and the
// daemon's patch semantics are checked against each other rather than each
// against a fake. Use a short owned directory: t.TempDir embeds the long test
// name, which can exceed the operating system's Unix socket path limit.
func TestExperimentalFeaturesEditOverTheClient(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	runDir, dirErr := os.MkdirTemp("", "exp-")
	require.NoError(t, dirErr)
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })
	t.Setenv("CITECK_RUN", runDir)
	t.Setenv("CITECK_HOST", "")
	d, mux := newNsCrudTestDaemon(t)
	writeBetaBundle(t, "wsMain")
	require.NoError(t, d.persistNamespaceConfig("wsMain", "nsbeta", []byte(
		"id: nsbeta\nname: Beta\nbundleRef: community:2026.1\n"+
			"authentication:\n  type: BASIC\n  users: [admin]\nproxy:\n  host: beta.local\n  port: 8081\n")))

	listener, err := net.Listen("unix", config.SocketPath())
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(mux)
	require.NoError(t, srv.Listener.Close())
	srv.Listener = listener
	srv.Start()
	defer srv.Close()
	c, err := client.New(client.Options{})
	require.NoError(t, err)
	defer c.Close()

	stored := func() api.NamespaceEditDto {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.NamespaceEditPath("nsbeta"), http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var dto api.NamespaceEditDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		return dto
	}
	enabledIDs := func(dto api.NamespaceEditDto) []string {
		ids := make([]string, 0, len(dto.ExperimentalFeatures))
		for _, f := range dto.ExperimentalFeatures {
			if f.Enabled {
				ids = append(ids, f.ID)
			}
		}
		return ids
	}

	// With no namespace active in this harness, the no-query listing says the
	// bundle cannot be resolved — the sentence the CLI turns into "run citeck
	// update"; the offers themselves are reachable through the stored bundle.
	listing, err := c.GetExperimentalFeatures()
	require.NoError(t, err)
	assert.Equal(t, api.ExperimentalFeaturesBundleUnresolved, listing.Reason)
	require.Len(t, stored().ExperimentalFeatures, 2, "the stored bundle offers two features")

	t.Run("a one-field edit switches the feature and leaves the rest alone", func(t *testing.T) {
		res, err := c.PutNamespaceEdit("nsbeta", api.NamespaceEditDto{EnabledExperimentalFeatures: &[]string{"documentScanner"}})
		require.NoError(t, err)
		assert.True(t, res.Success)
		dto := stored()
		assert.Equal(t, []string{"documentScanner"}, enabledIDs(dto))
		assert.Equal(t, "Beta", dto.Name)
		assert.Equal(t, "community", dto.BundleRepo)
		assert.Equal(t, "2026.1", dto.BundleKey)
		assert.Equal(t, "BASIC", dto.AuthType)
		assert.Equal(t, []string{"admin"}, dto.Users)
		assert.Equal(t, "beta.local", dto.Host)
		assert.Equal(t, 8081, dto.Port)
		cfg, err := d.loadNamespaceConfigFromStore("wsMain", "nsbeta")
		require.NoError(t, err)
		assert.Equal(t, []string{"documentScanner"}, cfg.ExperimentalFeatures)
	})

	t.Run("an empty list switches the last one off", func(t *testing.T) {
		_, err := c.PutNamespaceEdit("nsbeta", api.NamespaceEditDto{EnabledExperimentalFeatures: &[]string{}})
		require.NoError(t, err)
		assert.Empty(t, enabledIDs(stored()))
		cfg, err := d.loadNamespaceConfigFromStore("wsMain", "nsbeta")
		require.NoError(t, err)
		assert.Nil(t, cfg.ExperimentalFeatures, "nothing on writes no key")
	})

	t.Run("the daemon's refusal reaches the caller in its own words", func(t *testing.T) {
		_, err := c.PutNamespaceEdit("nsbeta", api.NamespaceEditDto{EnabledExperimentalFeatures: &[]string{"nope"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not offer experimental feature(s) nope")
		assert.Contains(t, err.Error(), "backup-engine, documentScanner")
		assert.Empty(t, enabledIDs(stored()), "a refused edit writes nothing")
	})
}
