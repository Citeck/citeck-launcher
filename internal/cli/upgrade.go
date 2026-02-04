package cli

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newUpgradeCmd() *cobra.Command {
	var list bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "upgrade [bundle-ref]",
		Short: "Upgrade to a different bundle version",
		Long:  "Change the bundle version and reload. Use --list to see available versions.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			if list || len(args) == 0 {
				return showBundleVersions(c)
			}

			if dryRun {
				return showUpgradeDryRun(c, args[0])
			}

			result, err := c.UpgradeNamespace(args[0])
			if err != nil {
				return fmt.Errorf("upgrade: %w", err)
			}
			output.PrintResult(result, func() {
				output.PrintText(result.Message)
			})
			return nil
		},
	}

	cmd.Flags().BoolVarP(&list, "list", "l", false, "List available bundle versions")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without applying")
	return cmd
}

func showBundleVersions(c *client.DaemonClient) error {
	bundles, err := c.ListBundles()
	if err != nil {
		return fmt.Errorf("list bundles: %w", err)
	}
	if len(bundles) == 0 {
		output.PrintText("No bundles available")
		return nil
	}

	// Get current bundleRef for highlighting
	ns, _ := c.GetNamespace()
	currentRef := ""
	if ns != nil {
		currentRef = ns.BundleRef
	}

	output.PrintResult(bundles, func() {
		output.PrintText("Available bundles:")
		for _, b := range bundles {
			for _, v := range b.Versions {
				ref := b.Repo + ":" + v
				marker := "  "
				if ref == currentRef {
					marker = "* "
				}
				output.PrintText("  %s%s", marker, ref)
			}
		}
	})
	return nil
}

func showUpgradeDryRun(c *client.DaemonClient, targetRef string) error {
	ns, nsErr := c.GetNamespace()
	currentRef := ""
	if nsErr == nil && ns != nil {
		currentRef = ns.BundleRef
	}
	output.PrintText("Current: %s", currentRef)
	output.PrintText("Target:  %s", targetRef)
	if currentRef == targetRef {
		output.PrintText("No change — already on this version")
		return nil
	}

	// Verify target bundle exists
	bundles, blErr := c.ListBundles()
	if blErr != nil {
		return fmt.Errorf("list bundles: %w", blErr)
	}
	found := false
	for _, b := range bundles {
		for _, v := range b.Versions {
			if b.Repo+":"+v == targetRef {
				found = true
			}
		}
	}
	if found {
		output.PrintText("Would update bundleRef and reload (containers with changed images will be recreated)")
	} else {
		output.PrintText("Warning: bundle %s not found in available versions", targetRef)
	}
	return nil
}
