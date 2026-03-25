package bundle

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niceteck/citeck-launcher/internal/git"
	"gopkg.in/yaml.v3"
)

const (
	defaultBundlesRepo   = "https://github.com/Citeck/launcher-workspace.git"
	defaultBundlesBranch = "main"
	defaultPullPeriod    = time.Hour
)

// ImageRepo maps a short prefix like "core" to a full registry URL like "nexus.citeck.ru".
type ImageRepo struct {
	ID  string `yaml:"id"`
	URL string `yaml:"url"`
}

// DataSourceConfig describes a datasource with at least a URL.
type DataSourceConfig struct {
	URL string `yaml:"url"`
	XA  bool   `yaml:"xa,omitempty"`
}

// WebappDefaultProps holds default properties for a webapp from workspace config.
type WebappDefaultProps struct {
	HeapSize     string                       `yaml:"heapSize"`
	MemoryLimit  string                       `yaml:"memoryLimit"`
	Environments map[string]string            `yaml:"environments"`
	DataSources  map[string]DataSourceConfig  `yaml:"dataSources"`
}

// WebappConfig describes a webapp with its aliases and default props.
type WebappConfig struct {
	ID           string             `yaml:"id"`
	Aliases      []string           `yaml:"aliases"`
	DefaultProps WebappDefaultProps `yaml:"defaultProps"`
}

// ProxyConfig describes the proxy app aliases.
type ProxyConfig struct {
	Aliases []string `yaml:"aliases"`
}

// QuickStartVariant describes a quick start option from workspace config.
type QuickStartVariant struct {
	Name     string    `yaml:"name"`
	Snapshot string    `yaml:"snapshot,omitempty"`
	Bundle   BundleRef `yaml:"bundleRef,omitempty"`
	Template string    `yaml:"template,omitempty"`
}

// NamespaceTemplate describes a pre-configured namespace template.
type NamespaceTemplate struct {
	ID           string         `yaml:"id"`
	Name         string         `yaml:"name,omitempty"`
	Config       map[string]any `yaml:"config,omitempty"`
	DetachedApps []string       `yaml:"detachedApps,omitempty"`
}

// BundlesRepo describes a git repository containing bundle definitions.
type BundlesRepo struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	URL      string `yaml:"url,omitempty"`
	Branch   string `yaml:"branch,omitempty"`
	Path     string `yaml:"path,omitempty"`
	AuthType string `yaml:"authType,omitempty"`
}

// SnapshotDef describes a downloadable snapshot from workspace config.
type SnapshotDef struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Size   string `yaml:"size,omitempty"`
	SHA256 string `yaml:"sha256,omitempty"`
}

// WorkspaceConfig is the top-level workspace-v1.yml structure.
type WorkspaceConfig struct {
	QuickStartVariants []QuickStartVariant `yaml:"quickStartVariants,omitempty"`
	Snapshots          []SnapshotDef       `yaml:"snapshots,omitempty"`
	NamespaceTemplates []NamespaceTemplate `yaml:"namespaceTemplates,omitempty"`
	ImageRepos         []ImageRepo         `yaml:"imageRepos"`
	BundleRepos        []BundlesRepo       `yaml:"bundleRepos,omitempty"`
	CiteckProxy        ProxyConfig         `yaml:"citeckProxy"`
	DefaultWebappProps WebappDefaultProps  `yaml:"defaultWebappProps,omitempty"`
	Webapps            []WebappConfig      `yaml:"webapps"`
}

// TokenLookupFunc returns an auth token for a given repo auth type.
// Returns empty string if no credentials are available.
type TokenLookupFunc func(authType string) string

// Resolver resolves bundle references to full bundle definitions.
type Resolver struct {
	dataDir     string
	tokenLookup TokenLookupFunc
}

// NewResolver creates a resolver without auth support.
func NewResolver(dataDir string) *Resolver {
	return &Resolver{dataDir: dataDir}
}

// NewResolverWithAuth creates a resolver with token lookup for authenticated repos.
func NewResolverWithAuth(dataDir string, tokenLookup TokenLookupFunc) *Resolver {
	return &Resolver{dataDir: dataDir, tokenLookup: tokenLookup}
}

// ResolveResult contains the bundle definition and workspace config.
type ResolveResult struct {
	Bundle    *BundleDef
	Workspace *WorkspaceConfig
}

// Resolve fetches and parses a bundle definition along with workspace config.
func (r *Resolver) Resolve(ref BundleRef) (*ResolveResult, error) {
	if ref.IsEmpty() {
		return &ResolveResult{Bundle: &EmptyBundleDef, Workspace: &WorkspaceConfig{}}, nil
	}

	// Step 1: Sync the default workspace repo to get workspace-v1.yml (bundleRepos, aliases, etc.)
	// This is a shared cache that contains workspace config for all bundle repos.
	defaultRepoDir := filepath.Join(r.dataDir, "bundles", "_workspace")
	if err := git.CloneOrPullWithAuth(git.RepoOpts{
		URL: defaultBundlesRepo, Branch: defaultBundlesBranch,
		DestDir: defaultRepoDir, PullPeriod: defaultPullPeriod,
	}); err != nil {
		slog.Warn("Failed to sync workspace repo", "err", err)
	}
	wsCfg := loadWorkspaceConfig(defaultRepoDir)

	// Step 2: Resolve the actual repo URL for ref.Repo from workspace config
	repoDir := filepath.Join(r.dataDir, "bundles", ref.Repo)
	repoURL := defaultBundlesRepo
	repoBranch := defaultBundlesBranch
	var repoToken string

	bundleRepo := findBundleRepo(wsCfg, ref.Repo)
	if bundleRepo != nil {
		if bundleRepo.URL != "" {
			repoURL = bundleRepo.URL
		}
		if bundleRepo.Branch != "" {
			repoBranch = bundleRepo.Branch
		}
		if bundleRepo.AuthType != "" && r.tokenLookup != nil {
			repoToken = r.tokenLookup(bundleRepo.AuthType)
		}
	}

	// Step 3: Clone or pull the actual bundle repo
	if err := git.CloneOrPullWithAuth(git.RepoOpts{
		URL: repoURL, Branch: repoBranch, DestDir: repoDir,
		Token: repoToken, PullPeriod: defaultPullPeriod,
	}); err != nil {
		slog.Warn("Failed to sync bundle repo", "repo", ref.Repo, "err", err)
	}

	// Build alias → canonical name map
	aliasMap := buildAliasMap(wsCfg)
	imageRepoMap := buildImageRepoMap(wsCfg)

	// Resolve bundle version — look in BundlesRepo.Path sub-directory if defined
	bundlesDir := repoDir
	if bundleRepo != nil && bundleRepo.Path != "" {
		bundlesDir = filepath.Join(repoDir, bundleRepo.Path)
	}
	key := ref.Key
	if strings.EqualFold(key, "LATEST") {
		latest, err := findLatestBundle(bundlesDir)
		if err != nil {
			return nil, err
		}
		key = latest
	}

	bundlePath := findBundleFile(bundlesDir, key)
	if bundlePath == "" {
		return nil, fmt.Errorf("bundle %s not found in %s", key, bundlesDir)
	}

	def, err := parseBundleFile(bundlePath, key, aliasMap, imageRepoMap)
	if err != nil {
		return nil, err
	}
	return &ResolveResult{Bundle: def, Workspace: wsCfg}, nil
}

func loadWorkspaceConfig(repoDir string) *WorkspaceConfig {
	candidates := []string{"workspace-v1.yml", "workspace-v1.yaml", "workspace.yml"}
	for _, name := range candidates {
		path := filepath.Join(repoDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg WorkspaceConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			slog.Warn("Failed to parse workspace config", "path", path, "err", err)
			continue
		}
		return &cfg
	}
	return &WorkspaceConfig{}
}

// FindSnapshot finds a SnapshotDef by ID in the workspace config.
func FindSnapshot(cfg *WorkspaceConfig, snapshotID string) *SnapshotDef {
	if cfg == nil {
		return nil
	}
	for i := range cfg.Snapshots {
		if cfg.Snapshots[i].ID == snapshotID {
			return &cfg.Snapshots[i]
		}
	}
	return nil
}

// findBundleRepo finds a BundlesRepo entry by ID in the workspace config.
func findBundleRepo(cfg *WorkspaceConfig, repoID string) *BundlesRepo {
	for i := range cfg.BundleRepos {
		if cfg.BundleRepos[i].ID == repoID {
			return &cfg.BundleRepos[i]
		}
	}
	return nil
}

func buildAliasMap(cfg *WorkspaceConfig) map[string]string {
	m := make(map[string]string)
	for _, app := range cfg.Webapps {
		for _, alias := range app.Aliases {
			m[alias] = app.ID
		}
	}
	for _, alias := range cfg.CiteckProxy.Aliases {
		m[alias] = "proxy"
	}
	return m
}

func buildImageRepoMap(cfg *WorkspaceConfig) map[string]string {
	m := make(map[string]string)
	for _, repo := range cfg.ImageRepos {
		m[repo.ID] = repo.URL
	}
	return m
}

func resolveImageURL(repository, tag string, imageRepoMap map[string]string) string {
	if repository == "" || tag == "" {
		return ""
	}
	// Check if repository has a prefix that maps to a registry
	parts := strings.SplitN(repository, "/", 2)
	if len(parts) == 2 {
		if registryURL, ok := imageRepoMap[parts[0]]; ok {
			return registryURL + "/" + parts[1] + ":" + tag
		}
	}
	return repository + ":" + tag
}

// rawBundleEntry is a single app entry in the bundle YAML.
type rawBundleEntry struct {
	Image struct {
		Repository string `yaml:"repository"`
		Tag        string `yaml:"tag"`
	} `yaml:"image"`
	EcosAppsImages []struct {
		Repository string `yaml:"repository"`
		Tag        string `yaml:"tag"`
	} `yaml:"ecosAppsImages"`
}

func parseBundleFile(path, version string, aliasMap map[string]string, imageRepoMap map[string]string) (*BundleDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", version, err)
	}

	// Parse as map of raw entries
	var raw map[string]rawBundleEntry
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse bundle %s: %w", version, err)
	}

	applications := make(map[string]BundleAppDef)
	var citeckApps []BundleAppDef

	for alias, entry := range raw {
		image := resolveImageURL(entry.Image.Repository, entry.Image.Tag, imageRepoMap)
		if image == "" {
			continue
		}

		// Map alias to canonical name
		appName := alias
		if canonical, ok := aliasMap[alias]; ok {
			appName = canonical
		}

		applications[appName] = BundleAppDef{Image: image}

		// Collect citeck apps (ecos-apps init containers)
		for _, ecosApp := range entry.EcosAppsImages {
			citeckAppImage := resolveImageURL(ecosApp.Repository, ecosApp.Tag, imageRepoMap)
			if citeckAppImage != "" {
				citeckApps = append(citeckApps, BundleAppDef{Image: citeckAppImage})
			}
		}
	}

	def := &BundleDef{
		Key:          BundleKey{Version: version},
		Applications: applications,
		CiteckApps:   citeckApps,
	}

	slog.Info("Resolved bundle", "version", version, "apps", len(applications), "citeckApps", len(citeckApps))
	return def, nil
}

func findBundleFile(dir, key string) string {
	candidates := []string{
		filepath.Join(dir, key+".yaml"),
		filepath.Join(dir, key+".yml"),
		filepath.Join(dir, key),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// ListBundleVersions lists available bundle version keys in a given bundles sub-directory.
func ListBundleVersions(bundlesDir string) []string {
	entries, err := os.ReadDir(bundlesDir)
	if err != nil {
		return nil
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		name = strings.TrimSuffix(name, ".yaml")
		name = strings.TrimSuffix(name, ".yml")
		versions = append(versions, name)
	}
	return versions
}

func findLatestBundle(bundlesDir string) (string, error) {
	entries, err := os.ReadDir(bundlesDir)
	if err != nil {
		return "", fmt.Errorf("list bundles in %s: %w", bundlesDir, err)
	}

	var latest string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		name = strings.TrimSuffix(name, ".yaml")
		name = strings.TrimSuffix(name, ".yml")
		if latest == "" || compareBundleVersions(name, latest) > 0 {
			latest = name
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no bundles found in %s", bundlesDir)
	}
	return latest, nil
}

// compareBundleVersions compares two dot-separated version strings numerically.
// "2025.10" > "2025.9", matching the Kotlin BundleKey.compareTo behaviour.
func compareBundleVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		ai, _ := strconv.Atoi(aParts[i])
		bi, _ := strconv.Atoi(bParts[i])
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return len(aParts) - len(bParts)
}
