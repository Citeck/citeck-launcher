package bundle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/git"
	"gopkg.in/yaml.v3"
)

const (
	defaultBundlesRepo   = "https://github.com/Citeck/launcher-workspace.git"
	defaultBundlesBranch = "main"
	defaultPullPeriod    = time.Hour
)

// ImageRepo maps a short prefix like "core" to a full registry URL like "nexus.citeck.ru".
type ImageRepo struct {
	ID       string `yaml:"id"`
	URL      string `yaml:"url"`
	AuthType string `yaml:"authType,omitempty"` // "BASIC" if registry requires authentication
}

// DataSourceConfig describes a datasource with at least a URL.
type DataSourceConfig struct {
	URL string `yaml:"url"`
	XA  bool   `yaml:"xa,omitempty"`
}

// WebappDefaultProps holds default properties for a webapp from workspace config.
type WebappDefaultProps struct {
	Image        string                       `yaml:"image"`
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
	Bundle   Ref `yaml:"bundleRef,omitempty"`
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
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	URL        string `yaml:"url,omitempty"`
	Branch     string `yaml:"branch,omitempty"`
	Path       string `yaml:"path,omitempty"`
	AuthType   string `yaml:"authType,omitempty"`
	PullPeriod string `yaml:"pullPeriod,omitempty"` // e.g. "30m", "2h" — defaults to 1h
}

// SnapshotDef describes a downloadable snapshot from workspace config.
type SnapshotDef struct {
	ID     string `yaml:"id" json:"id"`
	Name   string `yaml:"name" json:"name"`
	URL    string `yaml:"url" json:"url"`
	Size   string `yaml:"size,omitempty" json:"size,omitempty"`
	SHA256 string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
}

// PostgresProps holds workspace-level overrides for the PostgreSQL container.
type PostgresProps struct {
	Image string `yaml:"image,omitempty"`
}

// KeycloakProps holds workspace-level overrides for the Keycloak container.
type KeycloakProps struct {
	Image string `yaml:"image,omitempty"`
}

// ZookeeperProps holds workspace-level overrides for the Zookeeper container.
type ZookeeperProps struct {
	Image string `yaml:"image,omitempty"`
}

// OnlyOfficeProps holds workspace-level overrides for the OnlyOffice container.
type OnlyOfficeProps struct {
	Image       string `yaml:"image,omitempty"`
	MemoryLimit string `yaml:"memoryLimit,omitempty"`
}

// PgAdminWsProps holds workspace-level overrides for the PgAdmin container.
type PgAdminWsProps struct {
	Image string `yaml:"image,omitempty"`
}

// AlfrescoProps holds workspace-level overrides for the Alfresco container.
type AlfrescoProps struct {
	Enabled bool     `yaml:"enabled,omitempty"`
	Aliases []string `yaml:"aliases,omitempty"`
}

// LicenseInstance represents a Citeck enterprise license.
type LicenseInstance struct {
	ID         string `json:"id" yaml:"id"`
	Tenant     string `json:"tenant" yaml:"tenant"`
	Priority   int64  `json:"priority" yaml:"priority"`
	IssuedTo   string `json:"issuedTo" yaml:"issuedTo"`
	IssuedAt   string `json:"issuedAt" yaml:"issuedAt"`
	ValidFrom  string `json:"validFrom" yaml:"validFrom"`
	ValidUntil string `json:"validUntil" yaml:"validUntil"`
	Content    any    `json:"content" yaml:"content"`
	Signatures []any  `json:"signatures" yaml:"signatures"`
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
	Postgres           PostgresProps       `yaml:"postgres,omitempty"`
	Keycloak           KeycloakProps       `yaml:"keycloak,omitempty"`
	Zookeeper          ZookeeperProps      `yaml:"zookeeper,omitempty"`
	OnlyOffice         OnlyOfficeProps     `yaml:"onlyoffice,omitempty"`
	PgAdmin            PgAdminWsProps      `yaml:"pgadmin,omitempty"`
	Alfresco           AlfrescoProps       `yaml:"alfresco,omitempty"`
	Licenses           []LicenseInstance   `yaml:"licenses,omitempty"`
}

// ImageReposByHost builds a map from registry host to ImageRepo for auth lookup.
func (w *WorkspaceConfig) ImageReposByHost() map[string]ImageRepo {
	m := make(map[string]ImageRepo)
	for _, repo := range w.ImageRepos {
		host := repo.URL
		if idx := strings.Index(host, "/"); idx > 0 {
			host = host[:idx]
		}
		m[host] = repo
	}
	return m
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
	Bundle    *Def
	Workspace *WorkspaceConfig
}

// Resolve fetches and parses a bundle definition along with workspace config.
func (r *Resolver) Resolve(ref Ref) (*ResolveResult, error) {
	if ref.IsEmpty() {
		return &ResolveResult{Bundle: &EmptyDef, Workspace: &WorkspaceConfig{}}, nil
	}

	// Step 1: Load workspace config (bundleRepos, aliases, imageRepos, etc.)
	// Priority: per-workspace repo/ dir (from Kotlin launcher or manual setup),
	// then fall back to cloning the default GitHub workspace repo.
	var wsCfg *WorkspaceConfig
	var wsRepoDir string // directory where workspace config was found (used as bundle source when URL is empty)
	localRepoDir := filepath.Join(r.dataDir, "repo")
	wsCfg = loadWorkspaceConfig(localRepoDir)
	if wsCfg != nil {
		wsRepoDir = localRepoDir
	}

	if wsCfg == nil {
		defaultRepoDir := filepath.Join(r.dataDir, "bundles", "_workspace")
		gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
			URL: defaultBundlesRepo, Branch: defaultBundlesBranch,
			DestDir: defaultRepoDir, PullPeriod: defaultPullPeriod,
		})
		gitCancel()
		if err != nil {
			slog.Warn("Failed to sync workspace repo", "err", err)
		}
		wsCfg = loadWorkspaceConfig(defaultRepoDir)
		if wsCfg != nil {
			wsRepoDir = defaultRepoDir
		}
	}
	if wsCfg == nil {
		wsCfg = &WorkspaceConfig{}
	}

	// Step 2: Resolve the actual repo URL for ref.Repo from workspace config
	bundleRepo := findBundleRepo(wsCfg, ref.Repo)

	// When bundleRepo.URL is empty, bundles are in the same repo as workspace config —
	// use wsRepoDir directly, skip git clone/pull.
	localBundles := bundleRepo != nil && bundleRepo.URL == "" && wsRepoDir != ""

	var repoDir string
	if localBundles {
		repoDir = wsRepoDir
		slog.Info("Bundle repo URL is empty, using workspace repo for bundles", "repo", ref.Repo, "dir", wsRepoDir)
	} else {
		repoDir = filepath.Join(r.dataDir, "bundles", ref.Repo)
		repoURL := defaultBundlesRepo
		repoBranch := defaultBundlesBranch
		var repoToken string

		if bundleRepo != nil {
			if bundleRepo.URL != "" {
				repoURL = bundleRepo.URL
			}
			if bundleRepo.Branch != "" {
				repoBranch = bundleRepo.Branch
			}
			if r.tokenLookup != nil {
				if bundleRepo.AuthType != "" {
					repoToken = r.tokenLookup(bundleRepo.AuthType)
				}
				// Fallback: try GIT_TOKEN type (covers Kotlin-migrated secrets with scope ws:{wsId}:repo)
				if repoToken == "" {
					repoToken = r.tokenLookup("GIT_TOKEN")
				}
			}
		}

		// Step 3: Clone or pull the actual bundle repo
		pullPeriod := defaultPullPeriod
		if bundleRepo != nil && bundleRepo.PullPeriod != "" {
			if d, err := time.ParseDuration(bundleRepo.PullPeriod); err == nil && d > 0 {
				pullPeriod = d
			}
		}
		gitCtx2, gitCancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		err := git.CloneOrPullWithAuth(gitCtx2, git.RepoOpts{
			URL: repoURL, Branch: repoBranch, DestDir: repoDir,
			Token: repoToken, PullPeriod: pullPeriod,
		})
		gitCancel2()
		if err != nil {
			slog.Warn("Failed to sync bundle repo", "repo", ref.Repo, "err", err)
		}
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
		latest, latestErr := findLatestBundle(bundlesDir)
		if latestErr != nil {
			return nil, latestErr
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
		data, err := os.ReadFile(path) //nolint:gosec // path is constructed from fixed filenames within repoDir
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
	return nil
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
	for _, alias := range cfg.Alfresco.Aliases {
		m[alias] = "alfresco"
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

func parseBundleFile(path, version string, aliasMap, imageRepoMap map[string]string) (*Def, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from internal bundle dir
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", version, err)
	}

	// Parse as generic map — needed for ecos: scope recursion
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse bundle %s: %w", version, err)
	}

	applications := make(map[string]AppDef)
	var citeckApps []AppDef

	// processApp handles one bundle entry. When appName is "ecos", it recurses
	// into sub-entries (Helm charts group core apps under an ecos: key).
	var processApp func(appName string, value map[string]any)
	processApp = func(appName string, value map[string]any) {
		if appName == "" {
			return
		}
		if appName == "ecos" {
			// Recurse into nested entries under ecos: scope
			for subName, subVal := range value {
				if subMap, ok := subVal.(map[string]any); ok {
					processApp(subName, subMap)
				}
			}
			return
		}

		image := extractBundleImage(value, imageRepoMap)
		if image == "" {
			return
		}

		// Map alias to canonical name
		canonical := appName
		if mapped, ok := aliasMap[appName]; ok {
			canonical = mapped
		}
		applications[canonical] = AppDef{Image: image}

		// Collect citeck apps (ecos-apps init containers)
		citeckApps = collectCiteckApps(value, imageRepoMap, citeckApps)
	}

	for appName, value := range raw {
		if valueMap, ok := value.(map[string]any); ok {
			processApp(appName, valueMap)
		}
	}

	def := &Def{
		Key:          Key{Version: version},
		Applications: applications,
		CiteckApps:   citeckApps,
		Content:      raw,
	}

	slog.Info("Resolved bundle", "version", version, "apps", len(applications), "citeckApps", len(citeckApps))
	return def, nil
}

// collectCiteckApps extracts ecos-apps init container images from a bundle entry.
func collectCiteckApps(value map[string]any, imageRepoMap map[string]string, citeckApps []AppDef) []AppDef {
	ecosApps, ok := value["ecosAppsImages"]
	if !ok {
		return citeckApps
	}
	ecosAppsList, ok := ecosApps.([]any)
	if !ok {
		return citeckApps
	}
	for _, ea := range ecosAppsList {
		eaMap, ok := ea.(map[string]any)
		if !ok {
			continue
		}
		citeckAppImage := resolveImageURL(strVal(eaMap, "repository"), strVal(eaMap, "tag"), imageRepoMap)
		if citeckAppImage != "" {
			citeckApps = append(citeckApps, AppDef{Image: citeckAppImage})
		}
	}
	return citeckApps
}

// extractBundleImage extracts image URL from a bundle entry's image.repository + image.tag.
func extractBundleImage(entry map[string]any, imageRepoMap map[string]string) string {
	imgObj, ok := entry["image"]
	if !ok {
		return ""
	}
	imgMap, ok := imgObj.(map[string]any)
	if !ok {
		return ""
	}
	return resolveImageURL(strVal(imgMap, "repository"), strVal(imgMap, "tag"), imageRepoMap)
}

// strVal safely extracts a string value from a map.
func strVal(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func findBundleFile(dir, key string) string {
	candidates := []string{
		filepath.Join(dir, key+".yaml"),
		filepath.Join(dir, key+".yml"),
		filepath.Join(dir, key, "values.yaml"),
		filepath.Join(dir, key, "values.yml"),
		filepath.Join(dir, key),
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// isVersionString checks if a name looks like a version (starts with a digit).
// Citeck bundle versions follow the format "YYYY.N" (e.g. "2025.10"), so all valid
// version strings start with a digit. This filters out non-version files like README.yml.
func isVersionString(name string) bool {
	return name != "" && name[0] >= '0' && name[0] <= '9'
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
		if isVersionString(name) {
			versions = append(versions, name)
		}
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
		if !isVersionString(name) {
			continue
		}
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
// "2025.10" > "2025.9", matching the Kotlin Key.compareTo behavior.
func compareBundleVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := min(len(aParts), len(bParts))
	for i := range n {
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
