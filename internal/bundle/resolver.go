package bundle

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/niceteck/citeck-launcher/internal/git"
	"gopkg.in/yaml.v3"
)

const (
	defaultBundlesRepo   = "https://github.com/Citeck/launcher-workspace.git"
	defaultBundlesBranch = "main"
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

// WorkspaceConfig is the top-level workspace-v1.yml structure (subset we need).
type WorkspaceConfig struct {
	ImageRepos  []ImageRepo    `yaml:"imageRepos"`
	Webapps     []WebappConfig `yaml:"webapps"`
	CiteckProxy ProxyConfig    `yaml:"citeckProxy"`
}

// Resolver resolves bundle references to full bundle definitions.
type Resolver struct {
	dataDir string
}

func NewResolver(dataDir string) *Resolver {
	return &Resolver{dataDir: dataDir}
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

	repoDir := filepath.Join(r.dataDir, "bundles", ref.Repo)

	// Clone or pull the bundle repo
	if err := git.CloneOrPull(defaultBundlesRepo, defaultBundlesBranch, repoDir); err != nil {
		slog.Warn("Failed to update bundle repo", "err", err)
	}

	// Load workspace config for alias mapping + image repos
	wsCfg := loadWorkspaceConfig(repoDir)

	// Build alias → canonical name map
	aliasMap := buildAliasMap(wsCfg)
	imageRepoMap := buildImageRepoMap(wsCfg)

	// Resolve bundle version
	bundlesDir := filepath.Join(repoDir, ref.Repo)
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
		if latest == "" || name > latest {
			latest = name
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no bundles found in %s", bundlesDir)
	}
	return latest, nil
}
