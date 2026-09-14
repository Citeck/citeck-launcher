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
	"github.com/citeck/citeck-launcher/internal/storage"
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

// orphanNamedVolume is a Docker NAMED volume belonging to a namespace the store
// no longer has. Desktop mode keeps every app's data in these (server mode uses
// bind directories, which orphanVolumeDir covers), so without them `citeck
// clean --volumes` could not reclaim a desktop namespace's data at all.
type orphanNamedVolume struct {
	Name      string
	Namespace string
	Workspace string
	// OrigName is the volume as the GENERATOR spells it ("postgres2"), which is
	// the part of the mangled `citeck_volume_<orig>_<ns>_<ws>` name that says
	// what the operator is about to delete.
	OrigName string
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
	orphans        []orphanContainer
	orphanVols     []orphanVolumeDir
	orphanNamedVol []orphanNamedVolume
	orphanNets     []string
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

	if len(scan.orphans) == 0 && len(scan.orphanVols) == 0 && len(scan.orphanNamedVol) == 0 &&
		len(scan.orphanNets) == 0 && !images {
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
		scan.orphanNamedVol, err = findOrphanNamedVolumes(ctx, dc, knownNS)
		if err != nil {
			return scan, fmt.Errorf("scan named volumes: %w", err)
		}
	}

	scan.orphanNets, _ = findOrphanNetworks(ctx, dc, knownNS)
	return scan, nil
}

func printOrphanFindings(scan cleanScanResult, execute, images bool) {
	output.PrintResult(map[string]any{
		"containers":   len(scan.orphans),
		"volumes":      len(scan.orphanVols),
		"namedVolumes": len(scan.orphanNamedVol),
		"networks":     len(scan.orphanNets),
		"dryRun":       !execute,
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
		if len(scan.orphanNamedVol) > 0 {
			output.PrintText(fmt.Sprintf("Orphaned named volumes (DATA): %d", len(scan.orphanNamedVol)))
			for _, v := range scan.orphanNamedVol {
				output.PrintText(fmt.Sprintf("  %-40s  ns=%-15s  ws=%-10s  %s",
					v.Name, v.Namespace, v.Workspace, v.OrigName))
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
	what := fmt.Sprintf("%d containers, %d volume dirs, %d named volumes, %d networks",
		len(scan.orphans), len(scan.orphanVols), len(scan.orphanNamedVol), len(scan.orphanNets))
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

	namedVolRemoved := 0
	for _, v := range scan.orphanNamedVol {
		if err := dc.RemoveVolume(execCtx, v.Name); err != nil {
			output.Errf("Failed to remove volume %s: %v", v.Name, err)
			failed++
		} else {
			namedVolRemoved++
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
		"removed":         removed,
		"volRemoved":      volRemoved,
		"namedVolRemoved": namedVolRemoved,
		"failed":          failed,
	}
	if images {
		jsonResult["reclaimedMB"] = reclaimedMB
	}
	output.PrintResult(jsonResult, func() {
		output.PrintText(fmt.Sprintf("Removed %d containers, %d volume dirs, %d named volumes (%d failed)",
			removed, volRemoved, namedVolRemoved, failed))
		if images && reclaimedMB > 0 {
			output.PrintText(fmt.Sprintf("Reclaimed %.1f MB from dangling images", reclaimedMB))
		}
	})
	return nil
}

func knownNamespaceIDs() (map[string]bool, error) {
	if config.IsDesktopMode() {
		return desktopKnownNamespaceIDs()
	}

	// Server mode: read the actual namespace ID from config
	known := make(map[string]bool)
	cfgPath := config.NamespaceConfigPath()
	if cfg, err := namespace.LoadNamespaceConfig(cfgPath); err == nil && cfg.ID != "" {
		known[cfg.ID] = true
	}
	return known, nil
}

// desktopKnownNamespaceIDs enumerates namespace IDs across all workspaces from
// the SQLite store (a freshly-created namespace has a store row before any
// directory exists, so a directory scan would miss it).
func desktopKnownNamespaceIDs() (map[string]bool, error) {
	store, err := storage.NewSQLiteStore(config.HomeDir())
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer store.Close()
	return knownNamespaceIDsFromStore(store)
}

// knownNamespaceIDsFromStore reads EVERY stored namespace, not a walk of
// workspaces → their namespaces: a namespace row can outlive its workspace row,
// and this set is what protects a namespace from being offered for deletion.
func knownNamespaceIDsFromStore(store storage.Store) (map[string]bool, error) {
	refs, err := store.ListAllNamespaceRefs()
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	known := make(map[string]bool, len(refs))
	for _, ref := range refs {
		known[ref.NsID] = true
	}
	return known, nil
}

// belongsToThisProfile answers whether a launcher-labeled resource was created
// by THIS installation, from its workspace label alone.
//
// The label is decisive because of how it is written: `Client.workspace` is the
// workspace id in desktop mode and the EMPTY STRING in server mode
// (`docker/client.go`). So a resource with no workspace came from a server
// install and one with a workspace came from a desktop profile, and a host that
// runs both — the ordinary developer machine — has each of them looking at the
// other's stand through a keep set that cannot possibly contain it.
//
// Without this, `citeck clean --force` on such a host offered the OTHER
// installation's live containers and network for deletion, under a confirmation
// the operator gave about their own leftovers. That is the same mistake the
// startup sweep made (`docker.collectOrphanTargets` skips an empty workspace for
// exactly this reason), except typed rather than automatic — which makes it more
// convincing, not less dangerous.
//
// What it does NOT solve: two DESKTOP profiles (different CITECK_HOME) are
// indistinguishable by label, so the second one's namespaces still read as
// orphans here. Nothing in the labels can separate them, and `citeck clean` is
// at least dry-run and confirmed; the startup sweep has the same limit.
//
// A resource labeled by a launcher old enough not to write the workspace at all
// reads as server-mode. The cost is that a desktop `clean` stops offering it —
// keeping something that could have been reclaimed, which is the safe direction.
func belongsToThisProfile(workspaceLabel string) bool {
	if config.IsDesktopMode() {
		return workspaceLabel != ""
	}
	return workspaceLabel == ""
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
		if !belongsToThisProfile(ctr.Labels[docker.LabelWorkspace]) {
			continue // another installation's container — not ours to offer
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
			State:     string(ctr.State),
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
		if !belongsToThisProfile(net.Labels[docker.LabelWorkspace]) {
			continue // another installation's network — not ours to offer
		}
		orphans = append(orphans, net.Name)
	}
	return orphans, nil
}

// findOrphanNamedVolumes lists every launcher-labeled Docker named volume on
// the host and keeps those whose namespace the store no longer has.
//
// It answers NOTHING in server mode, and that is a safety rule, not an
// optimization. A server install never creates a launcher-labeled named volume
// at all — `buildContainerConfig` converts every plain volume source to a bind
// mount under the runtime directory (`docker/client.go`), and only the desktop
// path goes through `CreateVolume`, which is what applies the labels. So every
// named volume this scan can see on a server host belongs to some OTHER
// launcher profile, while `knownNamespaceIDs` in server mode knows exactly one
// namespace id — the one in `namespace.yml`. The two together turned
// `citeck clean --volumes --force` into an offer to delete every desktop
// stand's PostgreSQL data on the same host, under a confirmation obtained on a
// false premise. Same mistake the startup sweep made with an empty workspace
// label, and the same answer: what this profile cannot account for is not
// therefore garbage.
func findOrphanNamedVolumes(ctx context.Context, dc *docker.Client, knownNS map[string]bool) ([]orphanNamedVolume, error) {
	if !config.IsDesktopMode() {
		return nil, nil
	}
	vols, err := dc.ListAllLauncherVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	return collectOrphanNamedVolumes(vols, knownNS), nil
}

// collectOrphanNamedVolumes is the decision, kept pure so it is testable without
// Docker — which is why it does NOT ask belongsToThisProfile the way the
// container and network scans do: that answer comes from process-wide mode
// state, and a pure function that reads it is neither pure nor testable. Its
// CALLER is the gate, and a stronger one — the whole scan is skipped outside
// desktop mode, where no launcher-labeled named volume can exist in the first
// place.
//
// A volume with no namespace label is skipped for the same reason the container
// scan skips one: it cannot be attributed, and this list is about to be offered
// for deletion.
func collectOrphanNamedVolumes(vols []docker.LauncherVolume, knownNS map[string]bool) []orphanNamedVolume {
	orphans := make([]orphanNamedVolume, 0, len(vols))
	for _, v := range vols {
		if v.Namespace == "" || knownNS[v.Namespace] {
			continue
		}
		orphans = append(orphans, orphanNamedVolume{
			Name:      v.Name,
			Namespace: v.Namespace,
			Workspace: v.Workspace,
			OrigName:  v.OrigName,
		})
	}
	return orphans
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
