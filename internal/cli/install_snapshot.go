package cli

import (
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

func validateInstallSnapshot(cfg *namespace.Config, ws *bundle.WorkspaceConfig) error {
	if cfg.Snapshot == "" {
		return nil
	}
	def := bundle.FindSnapshot(ws, cfg.Snapshot)
	if def == nil {
		return fmt.Errorf("snapshot %q not found in workspace config", cfg.Snapshot)
	}
	if refusal := def.BundleVersionRefusal(cfg.BundleRef.Key); !refusal.Empty() {
		return errors.New(t(refusal.Key, refusal.Args...))
	}
	return nil
}

func compatibleInstallSnapshots(snapshots []bundle.SnapshotDef, version string) []bundle.SnapshotDef {
	var compatible []bundle.SnapshotDef
	for _, def := range snapshots {
		if def.BundleVersionRefusal(version).Empty() {
			compatible = append(compatible, def)
		}
	}
	return compatible
}
