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

	// Create some bundle files and non-version files
	os.WriteFile(filepath.Join(dir, "1.0.yaml"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(dir, "1.1.yaml"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(dir, "2.0.yml"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.yml"), []byte("test"), 0o644) // non-version, must be excluded
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

func TestFindSnapshot(t *testing.T) {
	cfg := &WorkspaceConfig{
		Snapshots: []SnapshotDef{
			{ID: "demo-2025.12", Name: "Demo", URL: "https://example.com/demo.zip", SHA256: "abc"},
			{ID: "empty", Name: "Empty", URL: "https://example.com/empty.zip"},
		},
	}

	found := FindSnapshot(cfg, "demo-2025.12")
	require.NotNil(t, found)
	assert.Equal(t, "Demo", found.Name)
	assert.Equal(t, "abc", found.SHA256)

	found = FindSnapshot(cfg, "empty")
	require.NotNil(t, found)
	assert.Equal(t, "Empty", found.Name)

	assert.Nil(t, FindSnapshot(cfg, "nonexistent"))
	assert.Nil(t, FindSnapshot(nil, "demo-2025.12"))
	assert.Nil(t, FindSnapshot(&WorkspaceConfig{}, "demo-2025.12"))
}

func TestCompareBundleVersions_EdgeCases(t *testing.T) {
	// Same numeric value with different string representations
	assert.Equal(t, 0, compareBundleVersions("01", "1"))
	// Non-numeric parts sort as 0
	assert.Equal(t, 0, compareBundleVersions("abc", "def"))
	// Longer version is greater when prefix matches
	assert.Equal(t, 1, compareBundleVersions("1.0.0", "1.0"))
	assert.Equal(t, -1, compareBundleVersions("1.0", "1.0.0"))
	// Single-segment versions
	assert.Equal(t, 1, compareBundleVersions("2", "1"))
	assert.Equal(t, 0, compareBundleVersions("0", "0"))
}

func TestFindBundleRepo(t *testing.T) {
	cfg := &WorkspaceConfig{
		BundleRepos: []BundlesRepo{
			{ID: "community", URL: "https://github.com/repo1.git"},
			{ID: "enterprise", URL: "https://github.com/repo2.git"},
		},
	}

	found := findBundleRepo(cfg, "community")
	require.NotNil(t, found)
	assert.Equal(t, "https://github.com/repo1.git", found.URL)

	assert.Nil(t, findBundleRepo(cfg, "nonexistent"))
}

func TestParseBundleFile_EcosScopeRecursion(t *testing.T) {
	dir := t.TempDir()
	// Bundle with ecos: scope wrapping apps — Helm chart format
	yml := `
ecos:
  EcosModelApp:
    image:
      repository: core/ecos-model
      tag: "2.0"
  EcosProcessApp:
    image:
      repository: core/ecos-process
      tag: "3.0"
EcosProxyApp:
  image:
    repository: core/ecos-proxy
    tag: "1.0"
`
	path := filepath.Join(dir, "2025.1.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yml), 0o644))

	aliasMap := map[string]string{
		"EcosModelApp":   "emodel",
		"EcosProcessApp": "eproc",
		"EcosProxyApp":   "proxy",
	}
	imageRepoMap := map[string]string{
		"core": "nexus.citeck.ru",
	}

	def, err := parseBundleFile(path, "2025.1", aliasMap, imageRepoMap)
	require.NoError(t, err)

	// Apps under ecos: scope should be resolved
	assert.Contains(t, def.Applications, "emodel")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:2.0", def.Applications["emodel"].Image)
	assert.Contains(t, def.Applications, "eproc")
	assert.Equal(t, "nexus.citeck.ru/ecos-process:3.0", def.Applications["eproc"].Image)

	// Top-level apps should also work
	assert.Contains(t, def.Applications, "proxy")
	assert.Equal(t, "nexus.citeck.ru/ecos-proxy:1.0", def.Applications["proxy"].Image)
}

func TestParseBundleFile_CiteckApps(t *testing.T) {
	dir := t.TempDir()
	yml := `
eapps:
  image:
    repository: core/ecos-apps
    tag: "1.0"
  ecosAppsImages:
    - repository: core/ecos-data-app
      tag: "2.0"
    - repository: core/ecos-model-app
      tag: "3.0"
`
	path := filepath.Join(dir, "test.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yml), 0o644))

	imageRepoMap := map[string]string{"core": "nexus.citeck.ru"}
	def, err := parseBundleFile(path, "test", nil, imageRepoMap)
	require.NoError(t, err)

	assert.Contains(t, def.Applications, "eapps")
	assert.Len(t, def.CiteckApps, 2)
	assert.Equal(t, "nexus.citeck.ru/ecos-data-app:2.0", def.CiteckApps[0].Image)
	assert.Equal(t, "nexus.citeck.ru/ecos-model-app:3.0", def.CiteckApps[1].Image)
}

func TestBuildAliasMap_IncludesAlfrescoAliases(t *testing.T) {
	cfg := &WorkspaceConfig{
		Webapps: []WebappConfig{
			{ID: "emodel", Aliases: []string{"EcosModelApp"}},
		},
		CiteckProxy: ProxyConfig{Aliases: []string{"EcosProxyApp"}},
		Alfresco:    AlfrescoProps{Aliases: []string{"AlfrescoApp", "AlfApp"}},
	}

	m := buildAliasMap(cfg)
	assert.Equal(t, "emodel", m["EcosModelApp"])
	assert.Equal(t, "proxy", m["EcosProxyApp"])
	assert.Equal(t, "alfresco", m["AlfrescoApp"])
	assert.Equal(t, "alfresco", m["AlfApp"])
}
