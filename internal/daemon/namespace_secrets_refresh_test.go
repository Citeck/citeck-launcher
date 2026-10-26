package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestUnlockRefreshesStoppedS3NamespaceWithoutStarting(t *testing.T) {
	d, mux := secretsTestMux(t)
	d.bgCtx = context.Background()
	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	cfg.ContentStorage = &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}
	ws := parseCSWorkspace(t)
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{appdef.AppContent: {Image: "enterprise/ecos-content:1.8.0"}},
		Dependencies: map[string]bundle.AppDef{csProvider: {Image: "chrislusf/seaweedfs:4.48"}},
	}
	// Store the real namespace values, then recreate the service as at a locked boot.
	values, err := loadNamespaceSecrets(d.secretService, "ws", "ns1", ws, nsSecretsSeed)
	require.NoError(t, err)
	d.secretService, err = storage.NewSecretService(d.store)
	require.NoError(t, err)
	require.True(t, d.secretService.IsLocked())
	locked, err := namespace.Generate(&cfg, bun, ws, namespace.SystemSecrets{}, namespace.GenerateOpts{
		NamespaceSecrets: namespaceSecretsFor(d.secretService, "ws", "ns1", ws, nsSecretsRead),
	})
	require.NoError(t, err)
	require.NotNil(t, locked.ContentStorageProblem)
	rt := namespace.NewRuntime(&cfg, nil, t.TempDir())
	t.Cleanup(rt.Shutdown)
	require.True(t, rt.RefreshStoppedApps(locked.Applications))
	d.activeNs = &activeNamespace{workspaceID: "ws", nsConfig: &cfg, workspaceConfig: ws, bundleDef: bun,
		runtime: rt, appDefs: locked.Applications, volumesBase: t.TempDir(), contentStorageProblem: locked.ContentStorageProblem}
	// Simulate a reload that captured the locked definitions before this one
	// unlock, then commits them afterwards. The queued refresh must follow it.
	require.True(t, d.longOp.TryLock(longOpRequest))
	d.reloadMu.Lock()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.SecretsUnlock, strings.NewReader(`{"password":"test-master"}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, d.active().contentStorageProblem)
	d.reloadMu.Unlock()
	d.longOp.Unlock()
	d.bgWg.Wait()
	require.Equal(t, namespace.NsStatusStopped, rt.Status(), "unlock must not start a stopped stand")
	require.Nil(t, d.active().contentStorageProblem, "stale provider warning is cleared by a real generation")
	provider := rt.FindApp(csProvider)
	require.NotNil(t, provider, "provider returns to the stopped app list")
	require.Equal(t, namespace.AppStatusStopped, provider.Status)
	restored, err := loadNamespaceSecrets(d.secretService, "ws", "ns1", ws, nsSecretsRead)
	require.NoError(t, err)
	require.Equal(t, values, restored, "unlock must reuse the stored credentials")
}
