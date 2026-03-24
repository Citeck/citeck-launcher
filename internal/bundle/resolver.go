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

// Resolver resolves bundle references to full bundle definitions.
type Resolver struct {
	dataDir string
}

func NewResolver(dataDir string) *Resolver {
	return &Resolver{dataDir: dataDir}
}

// Resolve fetches and parses a bundle definition.
func (r *Resolver) Resolve(ref BundleRef) (*BundleDef, error) {
	if ref.IsEmpty() {
		return &EmptyBundleDef, nil
	}

	repoDir := filepath.Join(r.dataDir, "bundles", ref.Repo)

	// Clone or pull the bundle repo
	if err := git.CloneOrPull(defaultBundlesRepo, defaultBundlesBranch, repoDir); err != nil {
		slog.Warn("Failed to update bundle repo", "err", err)
		// Continue with local copy if available
	}

	// Find the bundle file
	key := ref.Key
	if strings.EqualFold(key, "LATEST") {
		latest, err := findLatestBundle(repoDir)
		if err != nil {
			return nil, err
		}
		key = latest
	}

	bundlePath := filepath.Join(repoDir, "bundles", key+".yml")
	if _, err := os.Stat(bundlePath); err != nil {
		// Try without extension
		bundlePath = filepath.Join(repoDir, "bundles", key)
	}

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", key, err)
	}

	var def BundleDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse bundle %s: %w", key, err)
	}

	def.Key = BundleKey{Version: key}
	slog.Info("Resolved bundle", "ref", ref, "apps", len(def.Applications))

	return &def, nil
}

func findLatestBundle(repoDir string) (string, error) {
	bundlesDir := filepath.Join(repoDir, "bundles")
	entries, err := os.ReadDir(bundlesDir)
	if err != nil {
		return "", fmt.Errorf("list bundles: %w", err)
	}

	var latest string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yml")
		if latest == "" || name > latest {
			latest = name
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no bundles found in %s", bundlesDir)
	}
	return latest, nil
}
