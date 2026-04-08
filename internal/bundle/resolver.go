package bundle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	offline     bool // skip all git operations, fail if local data missing
}

// NewResolver creates a resolver without auth support.
func NewResolver(dataDir string) *Resolver {
	return &Resolver{dataDir: dataDir}
}

// NewResolverWithAuth creates a resolver with token lookup for authenticated repos.
func NewResolverWithAuth(dataDir string, tokenLookup TokenLookupFunc) *Resolver {
	return &Resolver{dataDir: dataDir, tokenLookup: tokenLookup}
}

// SetOffline enables offline mode: all git operations are skipped,
// and the resolver returns an error if required data is not available locally.
func (r *Resolver) SetOffline(offline bool) {
	r.offline = offline
}

// ResolveResult contains the bundle definition and workspace config.
type ResolveResult struct {
	Bundle    *Def
	Workspace *WorkspaceConfig
}

// resolveWorkspace loads workspace config from local repo/ dir or clones the default workspace repo.
// Returns (config, repoDir) where repoDir is the directory the config was loaded from.
func (r *Resolver) resolveWorkspace() (cfg *WorkspaceConfig, repoDir string) {
	// Priority 1: local repo/ dir (manual setup or workspace import)
	localRepoDir := filepath.Join(r.dataDir, "repo")
	if wsCfg := loadWorkspaceConfig(localRepoDir); wsCfg != nil {
		return wsCfg, localRepoDir
	}

	// Priority 2: cloned workspace repo (git pull if online)
	defaultRepoDir := filepath.Join(r.dataDir, "bundles", "workspace")
	if !r.offline {
		gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
			URL: defaultBundlesRepo, Branch: defaultBundlesBranch,
			DestDir: defaultRepoDir, PullPeriod: defaultPullPeriod,
		})
		gitCancel()
		if err != nil {
			slog.Warn("Failed to sync workspace repo", "err", err)
		}
	}
	if wsCfg := loadWorkspaceConfig(defaultRepoDir); wsCfg != nil {
		return wsCfg, defaultRepoDir
	}
	return &WorkspaceConfig{}, ""
}

// ResolveWorkspaceOnly loads workspace config without resolving a bundle.
// Used by the daemon to provide workspace data (templates, quick starts, bundle repos)
// even when no namespace is configured yet (e.g. fresh server before wizard).
func (r *Resolver) ResolveWorkspaceOnly() *WorkspaceConfig {
	wsCfg, _ := r.resolveWorkspace()
	return wsCfg
}

// Resolve fetches and parses a bundle definition along with workspace config.
func (r *Resolver) Resolve(ref Ref) (*ResolveResult, error) {
	if ref.IsEmpty() {
		return &ResolveResult{Bundle: &EmptyDef, Workspace: &WorkspaceConfig{}}, nil
	}

	wsCfg, wsRepoDir := r.resolveWorkspace()

	// Step 2: Resolve the actual repo URL for ref.Repo from workspace config
	bundleRepo := findBundleRepo(wsCfg, ref.Repo)

	localBundles := shouldUseLocalBundles(wsRepoDir, bundleRepo)

	var repoDir string
	if localBundles {
		repoDir = wsRepoDir
		slog.Info("Using workspace repo for bundles (local)", "repo", ref.Repo, "dir", wsRepoDir)
	} else {
		repoDir = r.syncBundleRepo(ref.Repo, bundleRepo)
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

// shouldUseLocalBundles checks if bundle files should be read from the workspace dir
// instead of cloning a separate git repo. Returns true when:
// - bundleRepo.URL is empty (explicit "use workspace repo"), OR
// - workspace dir already contains the bundle path on disk
//   (covers downloaded zip archives where bundleRepo.URL points to the source repo).
func shouldUseLocalBundles(wsRepoDir string, bundleRepo *BundlesRepo) bool {
	if wsRepoDir == "" || bundleRepo == nil {
		return false
	}
	if bundleRepo.URL == "" {
		return true
	}
	bundlePath := wsRepoDir
	if bundleRepo.Path != "" {
		bundlePath = filepath.Join(wsRepoDir, bundleRepo.Path)
	}
	info, err := os.Stat(bundlePath)
	return err == nil && info.IsDir()
}

// syncBundleRepo clones or pulls the bundle git repository and returns its local directory.
func (r *Resolver) syncBundleRepo(repoID string, bundleRepo *BundlesRepo) string {
	repoDir := filepath.Join(r.dataDir, "bundles", repoID)
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
		repoToken = r.lookupRepoToken(bundleRepo)
	}

	if !r.offline {
		pullPeriod := defaultPullPeriod
		if bundleRepo != nil && bundleRepo.PullPeriod != "" {
			if d, err := time.ParseDuration(bundleRepo.PullPeriod); err == nil && d > 0 {
				pullPeriod = d
			}
		}
		gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
			URL: repoURL, Branch: repoBranch, DestDir: repoDir,
			Token: repoToken, PullPeriod: pullPeriod,
		})
		gitCancel()
		if err != nil {
			slog.Warn("Failed to sync bundle repo", "repo", repoID, "err", err)
		}
	}
	return repoDir
}

// lookupRepoToken returns an auth token for the given bundle repo using the token lookup func.
func (r *Resolver) lookupRepoToken(bundleRepo *BundlesRepo) string {
	if r.tokenLookup == nil {
		return ""
	}
	var token string
	if bundleRepo.AuthType != "" {
		token = r.tokenLookup(bundleRepo.AuthType)
	}
	// Fallback: try GIT_TOKEN type (covers Kotlin-migrated secrets with scope ws:{wsId}:repo)
	if token == "" {
		token = r.tokenLookup("GIT_TOKEN")
	}
	return token
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
	// Sort newest first — callers expect versions[0] to be the latest
	slices.SortFunc(versions, func(a, b string) int {
		return compareBundleVersions(b, a) // descending
	})
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

// compareBundleVersions compares version strings matching Kotlin BundleKey.compareTo:
// 1. Parse scope (path before last '/'), version parts, and suffix
// 2. Compare scope (prefer no scope), then version parts, then suffix parts
// 3. Trailing zeros stripped from version (2025.12.0 == 2025.12)
// 4. No suffix > has suffix (2025.12 > 2025.12-beta1)
// 5. Suffix groups: digits+dots as version numbers, strings lexicographically
func compareBundleVersions(a, b string) int {
	ak, bk := parseBundleKey(a), parseBundleKey(b)

	// 1. Compare scope: prefer empty (no-scope ranks higher)
	if c := compareStringSlices(ak.scope, bk.scope, true); c != 0 {
		return c
	}
	// 2. Compare version parts numerically
	if c := compareIntSlices(ak.versionParts, bk.versionParts, false); c != 0 {
		return c
	}
	// 3. Compare suffix parts: prefer empty (release > pre-release)
	return compareSuffixParts(ak.suffixParts, bk.suffixParts)
}

// bundleKey mirrors Kotlin BundleKey: scope + versionParts + suffixParts.
type bundleKey struct {
	scope        []string
	versionParts []int
	suffixParts  []any // string or bundleKey (recursive)
}

// parseBundleKey parses "archive/2025.5-RC1.1" into scope=["archive"], version=[2025,5], suffix=["RC", key("1.1")].
func parseBundleKey(raw string) bundleKey {
	key := raw

	// Extract scope (everything before the last '/')
	var scope []string
	if idx := strings.LastIndex(key, "/"); idx != -1 {
		scopeStr := key[:idx]
		for s := range strings.SplitSeq(scopeStr, "/") {
			if s != "" {
				scope = append(scope, s)
			}
		}
		key = key[idx+1:]
	}

	// Split version prefix from suffix: version = leading digits+dots
	firstNonVersion := strings.IndexFunc(key, func(r rune) bool {
		return r != '.' && (r < '0' || r > '9')
	})
	versionStr := key
	suffixStr := ""
	if firstNonVersion != -1 {
		versionStr = key[:firstNonVersion]
		suffixStr = key[firstNonVersion:]
		// Strip leading separator
		if suffixStr != "" && !isLetterOrDigit(rune(suffixStr[0])) {
			suffixStr = suffixStr[1:]
		}
	}

	// Parse version parts, strip trailing zeros
	var versionParts []int
	for p := range strings.SplitSeq(versionStr, ".") {
		if n, err := strconv.Atoi(p); err == nil {
			versionParts = append(versionParts, n)
		}
	}
	for len(versionParts) > 0 && versionParts[len(versionParts)-1] == 0 {
		versionParts = versionParts[:len(versionParts)-1]
	}

	// Parse suffix parts: split by digit-or-dot vs letter groups (Kotlin splitByGroups)
	var suffixParts []any
	if suffixStr != "" {
		for _, group := range splitSuffixGroups(suffixStr) {
			if group != "" && isDigitOrDot(rune(group[0])) {
				suffixParts = append(suffixParts, parseBundleKey(group)) // recursive
			} else {
				suffixParts = append(suffixParts, group)
			}
		}
	}

	return bundleKey{scope: scope, versionParts: versionParts, suffixParts: suffixParts}
}

func isLetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isDigitOrDot(r rune) bool {
	return r == '.' || (r >= '0' && r <= '9')
}

// splitSuffixGroups splits suffix by character type: digit-or-dot vs letter.
// "RC1.1" → ["RC", "1.1"], "beta2" → ["beta", "2"].
// Matches Kotlin StringUtils.splitByGroups with predicate: digit/dot → group 1, else → group 0.
func splitSuffixGroups(s string) []string {
	if s == "" {
		return nil
	}
	var groups []string
	start := 0
	for i := 1; i < len(s); i++ {
		prevType := isDigitOrDot(rune(s[i-1]))
		curType := isDigitOrDot(rune(s[i]))
		if prevType != curType {
			groups = append(groups, s[start:i])
			start = i
		}
	}
	groups = append(groups, s[start:])
	return groups
}

// compareIntSlices compares int slices. preferEmpty: true means shorter/empty wins
// (used for scope — no scope is better), false means longer wins (used for version parts).
func compareIntSlices(a, b []int, preferEmpty bool) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] > b[i] {
			return 1
		} else if a[i] < b[i] {
			return -1
		}
	}
	if len(a) == len(b) {
		return 0
	}
	if preferEmpty {
		if len(a) < len(b) {
			return 1
		}
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return -1
}

// compareStringSlices compares string slices with preferEmpty semantics.
func compareStringSlices(a, b []string, preferEmpty bool) int {
	n := min(len(a), len(b))
	for i := range n {
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	if len(a) == len(b) {
		return 0
	}
	if preferEmpty {
		if len(a) < len(b) {
			return 1
		}
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return -1
}

// compareSuffixParts compares parsed suffix part lists (any = string or bundleKey).
// Empty suffix list is preferred (release > pre-release).
func compareSuffixParts(a, b []any) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	// Empty suffix > non-empty (release > pre-release)
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	n := min(len(a), len(b))
	for i := range n {
		c := compareSuffixPart(a[i], b[i])
		if c != 0 {
			return c
		}
	}
	// Fewer parts = better (prefer shorter suffix)
	if len(a) < len(b) {
		return 1
	} else if len(a) > len(b) {
		return -1
	}
	return 0
}

func compareSuffixPart(a, b any) int {
	aKey, aIsKey := a.(bundleKey)
	bKey, bIsKey := b.(bundleKey)
	aStr, aIsStr := a.(string)
	bStr, bIsStr := b.(string)

	switch {
	case aIsKey && bIsKey:
		if c := compareIntSlices(aKey.versionParts, bKey.versionParts, false); c != 0 {
			return c
		}
		return compareSuffixParts(aKey.suffixParts, bKey.suffixParts)
	case aIsStr && bIsStr:
		return strings.Compare(aStr, bStr)
	default:
		// bundleKey > string
		if aIsKey {
			return 1
		}
		return -1
	}
}
