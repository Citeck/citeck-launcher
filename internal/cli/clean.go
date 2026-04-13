package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

type orphanContainer struct {
	ID        string
	Name      string
	Namespace string
	Workspace string
	Image     string
	State     string
}

type orphanVolumeDir struct {
	Path      string
	Namespace string
	Name      string
}

func newCleanCmd() *cobra.Command {
	var execute bool
	var volumes bool
	var images bool

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Clean up orphaned containers and volumes",
		Long:  "Scan for Docker containers and volume directories that belong to namespaces that no longer exist.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClean(execute, volumes, images)
		},
	}

	cmd.Flags().BoolVar(&execute, "force", false, "Actually remove resources (dry run by default)")
	// Deprecated: --execute renamed to --force (standard convention).
	cmd.Flags().BoolVar(&execute, "execute", false, "Deprecated: use --force")
	_ = cmd.Flags().MarkDeprecated("execute", "use --force instead")
	cmd.Flags().BoolVar(&volumes, "volumes", false, "Also scan/remove orphaned volume directories")
	cmd.Flags().BoolVar(&images, "images", false, "Prune unused Docker images (dangling)")

	return cmd
}

// cleanScanResult holds the results of scanning for orphaned resources.
type cleanScanResult struct {
	orphans    []orphanContainer
	orphanVols []orphanVolumeDir
	orphanNets []string
}

func runClean(execute, volumes, images bool) error {
	scanCtx, scanCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer scanCancel()

	knownNS, err := knownNamespaceIDs()
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	dc, err := docker.NewClient("", "")
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer dc.Close()

	scan, err := scanOrphans(scanCtx, dc, knownNS, volumes)
	if err != nil {
		return err
	}

	if len(scan.orphans) == 0 && len(scan.orphanVols) == 0 && len(scan.orphanNets) == 0 && !images {
		output.PrintResult(map[string]any{"orphans": 0}, func() {
			output.PrintText("No orphaned resources found.")
		})
		return nil
	}

	printOrphanFindings(scan, execute, images)

	if !execute {
		return nil
	}

	if !confirmCleanRemoval(scan, images) {
		return nil
	}

	return executeCleanRemoval(dc, scan, images)
}

func scanOrphans(ctx context.Context, dc *docker.Client, knownNS map[string]bool, volumes bool) (cleanScanResult, error) {
	var scan cleanScanResult
	var err error

	scan.orphans, err = findOrphanContainers(ctx, dc, knownNS)
	if err != nil {
		return scan, fmt.Errorf("scan containers: %w", err)
	}

	if volumes {
		scan.orphanVols = findOrphanVolumeDirs(knownNS)
	}

	scan.orphanNets, _ = findOrphanNetworks(ctx, dc, knownNS)
	return scan, nil
}

func printOrphanFindings(scan cleanScanResult, execute, images bool) {
	output.PrintResult(map[string]any{
		"containers": len(scan.orphans),
		"volumes":    len(scan.orphanVols),
		"networks":   len(scan.orphanNets),
		"dryRun":     !execute,
	}, func() {
		if len(scan.orphans) > 0 {
			output.PrintText(fmt.Sprintf("Orphaned containers: %d", len(scan.orphans)))
			for _, o := range scan.orphans {
				output.PrintText(fmt.Sprintf("  %-30s  ns=%-15s  %s  %s", o.Name, o.Namespace, o.State, o.Image))
			}
		}
		if len(scan.orphanVols) > 0 {
			output.PrintText(fmt.Sprintf("Orphaned volume dirs: %d", len(scan.orphanVols)))
			for _, v := range scan.orphanVols {
				output.PrintText(fmt.Sprintf("  %-30s  ns=%s", v.Name, v.Namespace))
			}
		}
		if len(scan.orphanNets) > 0 {
			output.PrintText(fmt.Sprintf("Orphaned networks: %d", len(scan.orphanNets)))
			for _, n := range scan.orphanNets {
				output.PrintText(fmt.Sprintf("  %s", n))
			}
		}
		if !execute {
			output.PrintText("\nRun with --force to remove orphaned resources.")
			if images {
				output.PrintText("\nTo prune dangling Docker images, run with --force.")
			}
		}
	})
}

func confirmCleanRemoval(scan cleanScanResult, images bool) bool {
	what := fmt.Sprintf("%d containers, %d volume dirs, %d networks", len(scan.orphans), len(scan.orphanVols), len(scan.orphanNets))
	if images {
		what += " + dangling images"
	}
	if !promptConfirm(fmt.Sprintf("Remove %s?", what), true) {
		output.PrintText("Aborted")
		return false
	}
	return true
}

func executeCleanRemoval(dc *docker.Client, scan cleanScanResult, images bool) error {
	execCtx, execCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer execCancel()

	removed := 0
	failed := 0
	for _, o := range scan.orphans {
		if err := dc.StopAndRemoveContainer(execCtx, o.Name, 0); err != nil {
			output.Errf("Failed to remove %s: %v", o.Name, err)
			failed++
		} else {
			removed++
		}
	}

	for _, netName := range scan.orphanNets {
		if err := dc.RemoveNetworkByName(execCtx, netName); err != nil {
			output.Errf("Failed to remove network %s: %v", netName, err)
			failed++
		} else {
			removed++
		}
	}

	volRemoved := 0
	for _, v := range scan.orphanVols {
		if err := os.RemoveAll(v.Path); err != nil {
			output.Errf("Failed to remove %s: %v", v.Path, err)
			failed++
		} else {
			volRemoved++
		}
	}

	var reclaimedMB float64
	if images {
		pruneCtx, pruneCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		reclaimed, pruneErr := dc.PruneUnusedImages(pruneCtx)
		pruneCancel()
		if pruneErr != nil {
			output.Errf("Image prune failed: %v", pruneErr)
			failed++
		} else {
			reclaimedMB = float64(reclaimed) / (1024 * 1024)
		}
	}

	jsonResult := map[string]any{
		"removed":    removed,
		"volRemoved": volRemoved,
		"failed":     failed,
	}
	if images {
		jsonResult["reclaimedMB"] = reclaimedMB
	}
	output.PrintResult(jsonResult, func() {
		output.PrintText(fmt.Sprintf("Removed %d containers, %d volume dirs (%d failed)", removed, volRemoved, failed))
		if images && reclaimedMB > 0 {
			output.PrintText(fmt.Sprintf("Reclaimed %.1f MB from dangling images", reclaimedMB))
		}
	})
	return nil
}

func knownNamespaceIDs() (map[string]bool, error) {
	known := make(map[string]bool)

	if config.IsDesktopMode() {
		namespaces, err := config.ListAllNamespaces()
		if err != nil {
			return nil, fmt.Errorf("list namespaces: %w", err)
		}
		for _, ns := range namespaces {
			known[ns.NamespaceID] = true
		}
	} else {
		// Server mode: read the actual namespace ID from config
		cfgPath := config.NamespaceConfigPath()
		if cfg, err := namespace.LoadNamespaceConfig(cfgPath); err == nil && cfg.ID != "" {
			known[cfg.ID] = true
		}
	}
	return known, nil
}

func findOrphanContainers(ctx context.Context, dc *docker.Client, knownNS map[string]bool) ([]orphanContainer, error) {
	containers, err := dc.ListAllLauncherContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	orphans := make([]orphanContainer, 0, len(containers))
	for _, ctr := range containers {
		ns := ctr.Labels[docker.LabelNamespace]
		if ns != "" && knownNS[ns] {
			continue // belongs to a known namespace
		}
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		orphans = append(orphans, orphanContainer{
			ID:        ctr.ID[:12],
			Name:      name,
			Namespace: ns,
			Workspace: ctr.Labels[docker.LabelWorkspace],
			Image:     ctr.Image,
			State:     ctr.State,
		})
	}
	return orphans, nil
}

func findOrphanNetworks(ctx context.Context, dc *docker.Client, knownNS map[string]bool) ([]string, error) {
	networks, err := dc.ListLauncherNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	orphans := make([]string, 0, len(networks))
	for _, net := range networks {
		ns := net.Labels[docker.LabelNamespace]
		if ns != "" && knownNS[ns] {
			continue
		}
		orphans = append(orphans, net.Name)
	}
	return orphans, nil
}

func findOrphanVolumeDirs(knownNS map[string]bool) []orphanVolumeDir {
	if config.IsDesktopMode() {
		return findOrphanVolumeDesktop(knownNS)
	}
	return findOrphanVolumeServer(knownNS)
}

func findOrphanVolumeDesktop(knownNS map[string]bool) []orphanVolumeDir {
	workspaces, err := config.ListWorkspaces()
	if err != nil {
		return nil
	}
	var result []orphanVolumeDir
	for _, ws := range workspaces {
		for _, nsID := range ws.Namespaces {
			if knownNS[nsID] {
				continue
			}
			rtDir := config.NamespaceRtfilesDir(ws.ID, nsID)
			result = appendOrphanVolumes(result, filepath.Join(rtDir, "volumes"), nsID)
		}
	}
	return result
}

func findOrphanVolumeServer(knownNS map[string]bool) []orphanVolumeDir {
	runtimeDir := filepath.Join(config.DataDir(), "runtime")
	nsDirs, err := os.ReadDir(runtimeDir)
	if err != nil {
		return nil
	}
	var result []orphanVolumeDir
	for _, nsDir := range nsDirs {
		if !nsDir.IsDir() || knownNS[nsDir.Name()] {
			continue
		}
		volDir := filepath.Join(runtimeDir, nsDir.Name(), "volumes")
		result = appendOrphanVolumes(result, volDir, nsDir.Name())
	}
	return result
}

func appendOrphanVolumes(result []orphanVolumeDir, volDir, nsID string) []orphanVolumeDir {
	entries, err := os.ReadDir(volDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			result = append(result, orphanVolumeDir{
				Path:      filepath.Join(volDir, e.Name()),
				Namespace: nsID,
				Name:      e.Name(),
			})
		}
	}
	return result
}
