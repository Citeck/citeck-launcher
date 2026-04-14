package cli

import (
	"errors"
	"os"

	"github.com/citeck/citeck-launcher/internal/cli/setup"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// clientOpts returns Options for connecting to the local daemon via Unix socket.
func clientOpts() client.Options {
	return client.Options{}
}

var (
	flagOutput string
	flagYes    bool
)

// Yes returns the --yes flag value.
func Yes() bool { return flagYes }

// BuildInfo holds version metadata injected via ldflags.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// NewRootCmd creates the top-level CLI command with all subcommands registered.
func NewRootCmd(info BuildInfo) *cobra.Command {
	// Setting Version on the root command makes cobra register a --version
	// flag automatically; we pair it with a custom template that matches
	// the richer output of `citeck version` (one-line summary).
	version := info.Version
	if version == "" {
		version = "dev"
	}
	root := &cobra.Command{
		Use:     "citeck",
		Short:   "Citeck Launcher CLI",
		Long:    "Citeck Launcher — manage Citeck namespaces and Docker containers",
		Version: version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if flagOutput == "json" {
				output.SetFormat(output.FormatJSON)
			}
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("Citeck CLI {{.Version}}\n")

	// Localize command descriptions and flag help on first help invocation.
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		localizeCommands(root)
		defaultHelp(cmd, args)
	})

	root.PersistentFlags().StringVar(&flagOutput, "format", "text", "Output format: text or json")
	root.PersistentFlags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompts")

	root.AddCommand(
		// Primary commands
		newVersionCmd(info),
		newInstallCmd(info),
		newUninstallCmd(),
		newStartCmd(info.Version),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newReloadCmd(),
		newUpdateCmd(),
		newUpgradeCmd(),
		newLogsCmd(),
		newExecCmd(),
		newDescribeCmd(),
		newDiagnoseCmd(),
		newHealthCmd(),
		newCleanCmd(),
		newSnapshotCmd(),
		newConfigCmd(),
		setup.NewSetupCmd(),
		newDumpSystemInfoCmd(info),
		newCompletionCmd(),
	)

	return root
}

// Execute runs the root command and exits with the appropriate code.
func Execute(info BuildInfo) {
	root := NewRootCmd(info)
	if err := root.Execute(); err != nil {
		var ece ExitCodeError
		if errors.As(err, &ece) {
			if !output.IsJSON() {
				output.Errf("Error: %v", ece.Err)
			}
			os.Exit(ece.Code)
		}
		if !output.IsJSON() {
			output.Errf("Error: %v", err)
		}
		os.Exit(ExitError)
	}
}
