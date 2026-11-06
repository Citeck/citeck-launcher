package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
)

func TestInstallSnapshotSelectionUsesPickedBundleVersion(t *testing.T) {
	snapshots := []bundle.SnapshotDef{
		{ID: "legacy"}, {ID: "new", MinBundleVersion: "2026.3"},
		{ID: "invalid", MinBundleVersion: "invalid"},
	}
	require.Equal(t, snapshots[:1], compatibleInstallSnapshots(snapshots, "2026.2"))
	require.Equal(t, snapshots[:2], compatibleInstallSnapshots(snapshots, "2026.3"))
	require.Equal(t, snapshots[:2], compatibleInstallSnapshots(snapshots, "2026.10"))
}

func TestInstallSnapshotRefusesBeforeDownloadAndDocker(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	repo := filepath.Join(config.DataDir(), "repo")
	require.NoError(t, os.MkdirAll(repo, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "workspace-v1.yml"),
		[]byte("snapshots:\n  - id: demo\n    url: http://should-not-be-requested.invalid/snapshot.zip\n    minBundleVersion: 2026.3\n"), 0o600))
	cfg := &namespace.Config{Snapshot: "demo", BundleRef: bundle.Ref{Repo: "community", Key: "2026.2"}}
	err := importInstallSnapshot(context.Background(), cfg)
	require.ErrorContains(t, err, "requires bundle 2026.3")
	_, statErr := os.Stat(config.WorkspaceSnapshotsDir("daemon"))
	require.True(t, os.IsNotExist(statErr), "refusal must not create the snapshot cache")
}

func TestInstallSnapshotValidationBeforeSavingConfig(t *testing.T) {
	ws := &bundle.WorkspaceConfig{Snapshots: []bundle.SnapshotDef{{ID: "demo", MinBundleVersion: "2026.3"}}}
	cfg := &namespace.Config{BundleRef: bundle.Ref{Key: "2026.2"}}
	require.NoError(t, validateInstallSnapshot(cfg, ws), "a clean install has no snapshot requirement")
	cfg.Snapshot = "unknown"
	require.ErrorContains(t, validateInstallSnapshot(cfg, ws), "not found")
	cfg.Snapshot = "demo"
	require.Error(t, validateInstallSnapshot(cfg, ws))
	cfg.BundleRef.Key = "2026.3"
	require.NoError(t, validateInstallSnapshot(cfg, ws))
}
