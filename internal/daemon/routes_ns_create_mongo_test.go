package daemon

import (
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSelectsMongoFromResolvedEproc(t *testing.T) {
	for _, tag := range []string{"2.30.7", "2.33.0"} {
		t.Run(tag, func(t *testing.T) {
			t.Setenv("CITECK_HOME", t.TempDir())
			store, err := storage.NewSQLiteStore(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			writeCreatableBundle(t, "ws1", "2026.1")
			root := filepath.Join(config.BundlesDataDir("ws1"), "repo")
			require.NoError(t, os.WriteFile(filepath.Join(root, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: community\n    path: community\nwebapps:\n  - id: eproc\n    aliases: [EcosProcessApp]\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "community", "2026.1.yaml"), []byte("EcosProcessApp:\n  image: core/ecos-process:"+tag+"\n"), 0o600))
			d := testDaemon(t, store)
			d.activeNs = &activeNamespace{workspaceConfig: &bundle.WorkspaceConfig{BundleRepos: []bundle.BundlesRepo{{ID: "community"}}}}
			cfg, err := d.buildNamespaceConfigFromCreate(api.NamespaceCreateDto{Name: "test", BundleRepo: "community", BundleKey: "2026.1"}, "ws1")
			require.NoError(t, err)
			require.Equal(t, tag == "2.30.7", cfg.MongoEnabled())
			require.Equal(t, namespace.CurrentAPIVersion(), cfg.APIVersion)
		})
	}
}
