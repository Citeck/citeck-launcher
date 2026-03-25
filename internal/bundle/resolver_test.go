package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadWorkspaceConfig(t *testing.T) {
	dir := t.TempDir()
	yml := `
quickStartVariants:
  - name: Quick Start With Demo Data
    snapshot: community-demo-data-2025.12
  - name: Quick Start Without Demo Data
snapshots:
  - id: community-demo-data-2025.12
    name: Demo Data
    url: https://example.com/demo-data.zip
    size: 66.4m
    sha256: abc123
namespaceTemplates:
  - id: default
    name: Default
    config:
      bundleRef: 'community:LATEST'
      authentication:
        type: KEYCLOAK
imageRepos:
  - id: core
    url: nexus.citeck.ru
bundleRepos:
  - id: community
    name: Community Bundles
    url: https://github.com/Citeck/launcher-workspace.git
    branch: main
    path: community
  - id: enterprise
    name: Enterprise Bundles
    url: https://github.com/Citeck/launcher-workspace.git
    branch: main
    path: enterprise
citeckProxy:
  aliases: [ 'EcosProxyApp' ]
defaultWebappProps:
  heapSize: "256m"
  memoryLimit: "1g"
webapps:
  - id: eproc
    aliases: [ 'EcosProcessApp' ]
    defaultProps:
      heapSize: 1g
      memoryLimit: 2g
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "workspace-v1.yml"), []byte(yml), 0o644))

	cfg := loadWorkspaceConfig(dir)

	// QuickStartVariants
	assert.Len(t, cfg.QuickStartVariants, 2)
	assert.Equal(t, "Quick Start With Demo Data", cfg.QuickStartVariants[0].Name)
	assert.Equal(t, "community-demo-data-2025.12", cfg.QuickStartVariants[0].Snapshot)
	assert.Equal(t, "Quick Start Without Demo Data", cfg.QuickStartVariants[1].Name)
	assert.Empty(t, cfg.QuickStartVariants[1].Snapshot)

	// Snapshots
	assert.Len(t, cfg.Snapshots, 1)
	assert.Equal(t, "community-demo-data-2025.12", cfg.Snapshots[0].ID)
	assert.Equal(t, "Demo Data", cfg.Snapshots[0].Name)
	assert.Equal(t, "abc123", cfg.Snapshots[0].SHA256)

	// NamespaceTemplates
	assert.Len(t, cfg.NamespaceTemplates, 1)
	assert.Equal(t, "default", cfg.NamespaceTemplates[0].ID)
	assert.Equal(t, "Default", cfg.NamespaceTemplates[0].Name)
	assert.NotNil(t, cfg.NamespaceTemplates[0].Config)

	// ImageRepos
	assert.Len(t, cfg.ImageRepos, 1)
	assert.Equal(t, "core", cfg.ImageRepos[0].ID)

	// BundleRepos
	assert.Len(t, cfg.BundleRepos, 2)
	assert.Equal(t, "community", cfg.BundleRepos[0].ID)
	assert.Equal(t, "Community Bundles", cfg.BundleRepos[0].Name)
	assert.Equal(t, "main", cfg.BundleRepos[0].Branch)
	assert.Equal(t, "community", cfg.BundleRepos[0].Path)
	assert.Equal(t, "enterprise", cfg.BundleRepos[1].ID)

	// CiteckProxy
	assert.Equal(t, []string{"EcosProxyApp"}, cfg.CiteckProxy.Aliases)

	// DefaultWebappProps
	assert.Equal(t, "256m", cfg.DefaultWebappProps.HeapSize)
	assert.Equal(t, "1g", cfg.DefaultWebappProps.MemoryLimit)

	// Webapps
	assert.Len(t, cfg.Webapps, 1)
	assert.Equal(t, "eproc", cfg.Webapps[0].ID)
}

func TestListBundleVersions(t *testing.T) {
	dir := t.TempDir()

	// Create some bundle files
	os.WriteFile(filepath.Join(dir, "1.0.yaml"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(dir, "1.1.yaml"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(dir, "2.0.yml"), []byte("test"), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	versions := ListBundleVersions(dir)
	assert.ElementsMatch(t, []string{"1.0", "1.1", "2.0"}, versions)
}

func TestListBundleVersions_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	versions := ListBundleVersions(dir)
	assert.Nil(t, versions)
}

func TestListBundleVersions_NonexistentDir(t *testing.T) {
	versions := ListBundleVersions("/nonexistent/dir")
	assert.Nil(t, versions)
}

func TestFindLatestBundle_NumericVersions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "2025.9.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "2025.10.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "2024.12.yaml"), []byte("x"), 0o644)

	got, err := findLatestBundle(dir)
	require.NoError(t, err)
	assert.Equal(t, "2025.10", got) // not "2025.9" — numeric comparison
}

func TestCompareBundleVersions(t *testing.T) {
	assert.Equal(t, 1, compareBundleVersions("2025.10", "2025.9"))
	assert.Equal(t, -1, compareBundleVersions("2025.9", "2025.10"))
	assert.Equal(t, 0, compareBundleVersions("1.0", "1.0"))
	assert.Equal(t, 1, compareBundleVersions("2.0", "1.0"))
	assert.Equal(t, 1, compareBundleVersions("1.0.1", "1.0"))
	assert.Equal(t, -1, compareBundleVersions("1.0", "1.0.1"))
}

func TestLoadWorkspaceConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := loadWorkspaceConfig(dir)
	assert.Empty(t, cfg.QuickStartVariants)
	assert.Empty(t, cfg.BundleRepos)
	assert.Empty(t, cfg.NamespaceTemplates)
}
