package cli

import (
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSelectsMongoFromFinalRelease(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	root := filepath.Join(config.DataDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: community\n    path: community\nwebapps:\n  - id: eproc\n    aliases: [EcosProcessApp]\n"), 0o600))
	for _, tag := range []string{"2.30.7", "2.33.0"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "community", tag+".yaml"), []byte("EcosProcessApp:\n  image: core/ecos-process:"+tag+"\n"), 0o600))
		cfg := namespace.DefaultNamespaceConfig()
		cfg.APIVersion = namespace.CurrentAPIVersion()
		cfg.BundleRef = bundle.Ref{Repo: "community", Key: tag}
		require.NoError(t, applyInstallMongoDefault(&cfg))
		require.Equal(t, tag == "2.30.7", cfg.MongoEnabled())
	}
	cfg := namespace.DefaultNamespaceConfig()
	cfg.BundleRef = bundle.Ref{Repo: "community", Key: "missing"}
	require.Error(t, applyInstallMongoDefault(&cfg))
}
