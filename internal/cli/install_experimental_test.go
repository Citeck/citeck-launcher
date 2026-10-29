package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

const installBetaBundleYAML = "EcosModelApp:\n  image: core/ecos-model:1.0\n" +
	"experimental:\n" +
	"  documentScanner:\n    title: Document scanning\n    apps:\n" +
	"      citeck-scanner:\n        image: core/citeck-scanner:1.0\n" +
	"      scanner-postgres:\n        image: postgres:17.11\n" +
	"  backup-engine:\n    apps:\n      backup-engine:\n        image: core/backup-engine:0.3\n"

// writeInstallBetaWorkspace puts a workspace on disk the way `citeck install`
// finds one after the release picker: 2026.1 with an experimental section,
// 2026.2 without.
func writeInstallBetaWorkspace(t *testing.T) {
	t.Helper()
	t.Setenv("CITECK_HOME", t.TempDir())
	root := filepath.Join(config.DataDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n    path: community\nwebapps:\n  - id: emodel\n    aliases: [EcosModelApp]\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "community", "2026.1.yaml"), []byte(installBetaBundleYAML), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "community", "2026.2.yaml"), []byte("EcosModelApp:\n  image: core/ecos-model:1.1\n"), 0o600))
}

// The wizard asks about what the PICKED release offers, read off the disk the
// release picker just filled; a release without the section, an unreadable
// one and no release at all each offer nothing.
func TestInstallExperimentalFeaturesResolvesThePickedReleaseOffline(t *testing.T) {
	writeInstallBetaWorkspace(t)
	ref := func(key string) bundle.Ref { return bundle.Ref{Repo: "community", Key: key} }

	offered := installExperimentalFeatures(ref("2026.1"), "2.17.2")
	require.Len(t, offered, 2)
	assert.Equal(t, "backup-engine", offered[0].ID)
	assert.Equal(t, "documentScanner", offered[1].ID)
	assert.Equal(t, "Document scanning", offered[1].TitleFor(""))
	assert.Equal(t, []string{"citeck-scanner", "scanner-postgres"}, offered[1].Apps)

	assert.Empty(t, installExperimentalFeatures(ref("2026.2"), "2.17.2"), "the release that graduated them")
	assert.Empty(t, installExperimentalFeatures(ref("2099.1"), "2.17.2"), "a release that is not on disk")
	assert.Empty(t, installExperimentalFeatures(bundle.Ref{}, "2.17.2"))
}

// No question where nothing is offered; what the operator marks is stored
// normalized, and marking nothing writes nothing — like every other namespace.
func TestChooseInstallExperimentalFeatures(t *testing.T) {
	asked := 0
	answer := func(ids ...string) func([]bundle.ExperimentalFeature) []string {
		return func(offered []bundle.ExperimentalFeature) []string {
			asked++
			require.Len(t, offered, 2)
			return ids
		}
	}
	offered := []bundle.ExperimentalFeature{
		{ID: "backup-engine", Apps: []string{"backup-engine"}},
		{ID: "documentScanner", Title: bundle.PlainText("Document scanning"), Apps: []string{"citeck-scanner"}},
	}

	assert.Nil(t, chooseInstallExperimentalFeatures(nil, answer("documentScanner")))
	assert.Zero(t, asked, "no question where the release offers nothing")

	assert.Nil(t, chooseInstallExperimentalFeatures(offered, answer()), "nothing marked writes no list")
	assert.Equal(t, []string{"documentScanner", "backup-engine"},
		chooseInstallExperimentalFeatures(offered, answer(" documentScanner ", "backup-engine", "documentScanner")),
		"trimmed, deduplicated, in the order marked")
	assert.Equal(t, 2, asked)
}

// What the wizard writes is what the daemon reads: the choice survives the
// namespace.yml round trip, and no choice leaves the file without the key.
func TestInstallExperimentalFeaturesAreWrittenToNamespaceYml(t *testing.T) {
	cfg := namespace.DefaultNamespaceConfig()
	cfg.APIVersion = namespace.CurrentAPIVersion()
	cfg.BundleRef = bundle.Ref{Repo: "community", Key: "2026.1"}
	cfg.ExperimentalFeatures = chooseInstallExperimentalFeatures(nil, nil)

	data, err := namespace.MarshalNamespaceConfig(&cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "experimentalFeatures")

	cfg.ExperimentalFeatures = chooseInstallExperimentalFeatures(
		[]bundle.ExperimentalFeature{{ID: "documentScanner"}},
		func([]bundle.ExperimentalFeature) []string { return []string{"documentScanner"} })
	data, err = namespace.MarshalNamespaceConfig(&cfg)
	require.NoError(t, err)
	parsed, err := namespace.ParseNamespaceConfig(data)
	require.NoError(t, err)
	assert.Equal(t, []string{"documentScanner"}, parsed.ExperimentalFeatures)
	assert.True(t, parsed.ExperimentalEnabled()["documentScanner"])
}
