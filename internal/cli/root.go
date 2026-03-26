package cli

import (
	"os"

	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

var (
	flagOutput string
	flagHost   string
	flagToken  string
	flagYes    bool
)

func Host() string  { return flagHost }
func Token() string { return flagToken }
func Yes() bool     { return flagYes }

func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "citeck",
		Short: "Citeck Launcher CLI",
		Long:  "Citeck Launcher — manage Citeck ECOS namespaces and Docker containers",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if flagOutput == "json" {
				output.SetFormat(output.FormatJSON)
			}
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", "text", "Output format: text or json")
	root.PersistentFlags().StringVar(&flagHost, "host", "", "Remote daemon host:port")
	root.PersistentFlags().StringVar(&flagToken, "token", "", "Auth token for remote connections")
	root.PersistentFlags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompts")

	root.AddCommand(
		newVersionCmd(version),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newHealthCmd(),
		newConfigCmd(),
		newApplyCmd(),
		newDiffCmd(),
		newWaitCmd(),
		newReloadCmd(),
		newDiagnoseCmd(),
		newDescribeCmd(),
		newLogsCmd(),
		newExecCmd(),
		newRestartCmd(),
		newTokenCmd(),
		newCertCmd(),
		newCleanCmd(),
		newMigrateCmd(),
		newInstallCmd(),
		newUninstallCmd(),
		newSnapshotCmd(),
	)

	return root
}

func Execute(version string) {
	root := NewRootCmd(version)
	if err := root.Execute(); err != nil {
		if !output.IsJSON() {
			output.Errf("Error: %v", err)
		}
		os.Exit(ExitError)
	}
}
