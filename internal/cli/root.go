package cli

import (
	"errors"
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

func NewRootCmd(version string, extra ...string) *cobra.Command {
	commit, buildDate := "", ""
	if len(extra) > 0 {
		commit = extra[0]
	}
	if len(extra) > 1 {
		buildDate = extra[1]
	}
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
		newVersionCmd(version, commit, buildDate),
		newStartCmd(version),
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

func Execute(version string, extra ...string) {
	commit, buildDate := "", ""
	if len(extra) > 0 {
		commit = extra[0]
	}
	if len(extra) > 1 {
		buildDate = extra[1]
	}
	root := NewRootCmd(version, commit, buildDate)
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
