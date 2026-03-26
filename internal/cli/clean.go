package cli

import (
	"bufio"
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

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Clean up orphaned containers and volumes",
		Long:  "Scan for Docker containers and volume directories that belong to namespaces that no longer exist.",
		RunE: func(cmd *cobra.Command, args []string) error {
			scanCtx, scanCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer scanCancel()

			// Build set of known namespace IDs
			knownNS, err := knownNamespaceIDs()
			if err != nil {
				return fmt.Errorf("list namespaces: %w", err)
			}

			dc, err := docker.NewClient("", "")
			if err != nil {
				return fmt.Errorf("docker: %w", err)
			}
			defer dc.Close()

			// Find orphan containers
			orphans, err := findOrphanContainers(scanCtx, dc, knownNS)
			if err != nil {
				return fmt.Errorf("scan containers: %w", err)
			}

			// Find orphan volume dirs
			var orphanVols []orphanVolumeDir
			if volumes {
				orphanVols = findOrphanVolumeDirs(knownNS)
			}

			// Find orphan networks
			orphanNets, _ := findOrphanNetworks(scanCtx, dc, knownNS)

			if len(orphans) == 0 && len(orphanVols) == 0 && len(orphanNets) == 0 {
				output.PrintResult(map[string]any{"orphans": 0}, func() {
					output.PrintText("No orphaned resources found.")
				})
				return nil
			}

			// Print findings
			output.PrintResult(map[string]any{
				"containers": len(orphans),
				"volumes":    len(orphanVols),
				"networks":   len(orphanNets),
				"dryRun":     !execute,
			}, func() {
				if len(orphans) > 0 {
					output.PrintText(fmt.Sprintf("Orphaned containers: %d", len(orphans)))
					for _, o := range orphans {
						output.PrintText(fmt.Sprintf("  %-30s  ns=%-15s  %s  %s", o.Name, o.Namespace, o.State, o.Image))
					}
				}
				if len(orphanVols) > 0 {
					output.PrintText(fmt.Sprintf("Orphaned volume dirs: %d", len(orphanVols)))
					for _, v := range orphanVols {
						output.PrintText(fmt.Sprintf("  %-30s  ns=%s", v.Name, v.Namespace))
					}
				}
				if len(orphanNets) > 0 {
					output.PrintText(fmt.Sprintf("Orphaned networks: %d", len(orphanNets)))
					for _, n := range orphanNets {
						output.PrintText(fmt.Sprintf("  %s", n))
					}
				}
				if !execute {
					output.PrintText("\nRun with --execute to remove orphaned resources.")
				}
			})

			if !execute {
				return nil
			}

			if !flagYes {
				fmt.Printf("Remove %d containers, %d volume dirs, %d networks? [y/N]: ", len(orphans), len(orphanVols), len(orphanNets))
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
				if answer != "y" && answer != "yes" {
					output.PrintText("Aborted")
					return nil
				}
			}

			// Removal gets its own context — stopping containers can be slow
			execCtx, execCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer execCancel()

			// Remove orphan containers
			removed := 0
			failed := 0
			for _, o := range orphans {
				if err := dc.StopAndRemoveContainer(execCtx, o.Name, 0); err != nil {
					output.Errf("Failed to remove %s: %v", o.Name, err)
					failed++
				} else {
					removed++
				}
			}

			// Remove orphan networks
			for _, netName := range orphanNets {
				if err := dc.RemoveNetworkByName(execCtx, netName); err != nil {
					output.Errf("Failed to remove network %s: %v", netName, err)
					failed++
				} else {
					removed++
				}
			}

			// Remove orphan volume dirs
			volRemoved := 0
			for _, v := range orphanVols {
				if err := os.RemoveAll(v.Path); err != nil {
					output.Errf("Failed to remove %s: %v", v.Path, err)
					failed++
				} else {
					volRemoved++
				}
			}

			output.PrintResult(map[string]any{
				"removed":    removed,
				"volRemoved": volRemoved,
				"failed":     failed,
			}, func() {
				output.PrintText(fmt.Sprintf("Removed %d containers, %d volume dirs (%d failed)", removed, volRemoved, failed))
			})
			return nil
		},
	}

	cmd.Flags().BoolVar(&execute, "execute", false, "Actually remove resources (dry run by default)")
	cmd.Flags().BoolVar(&volumes, "volumes", false, "Also scan/remove orphaned volume directories")

	return cmd
}

func knownNamespaceIDs() (map[string]bool, error) {
	known := make(map[string]bool)

	if config.IsDesktopMode() {
		namespaces, err := config.ListAllNamespaces()
		if err != nil {
			return nil, err
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
		return nil, err
	}

	var orphans []orphanContainer
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
		return nil, err
	}
	var orphans []string
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
	var result []orphanVolumeDir

	if config.IsDesktopMode() {
		// In desktop mode, scan ws/*/ns/*/rtfiles/volumes/
		workspaces, err := config.ListWorkspaces()
		if err != nil {
			return nil
		}
		for _, ws := range workspaces {
			for _, nsID := range ws.Namespaces {
				if knownNS[nsID] {
					continue
				}
				// This namespace has no config but has a directory — scan for volume dirs
				rtDir := config.NamespaceRtfilesDir(ws.ID, nsID)
				volDir := filepath.Join(rtDir, "volumes")
				if entries, err := os.ReadDir(volDir); err == nil {
					for _, e := range entries {
						if e.IsDir() {
							result = append(result, orphanVolumeDir{
								Path:      filepath.Join(volDir, e.Name()),
								Namespace: nsID,
								Name:      e.Name(),
							})
						}
					}
				}
			}
		}
	} else {
		// Server mode: scan data/runtime/*/volumes/
		runtimeDir := filepath.Join(config.DataDir(), "runtime")
		nsDirs, err := os.ReadDir(runtimeDir)
		if err != nil {
			return nil
		}
		for _, nsDir := range nsDirs {
			if !nsDir.IsDir() || knownNS[nsDir.Name()] {
				continue
			}
			volDir := filepath.Join(runtimeDir, nsDir.Name(), "volumes")
			if entries, err := os.ReadDir(volDir); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						result = append(result, orphanVolumeDir{
							Path:      filepath.Join(volDir, e.Name()),
							Namespace: nsDir.Name(),
							Name:      e.Name(),
						})
					}
				}
			}
		}
	}
	return result
}
