package cli

import (
	"errors"
	"os"

	"github.com/citeck/citeck-launcher/internal/cli/setup"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// clientOpts returns Options from the global CLI flags.
func clientOpts() client.Options {
	return client.Options{
		Host:       flagHost,
		TLSCert:    flagTLSCert,
		TLSKey:     flagTLSKey,
		ServerCert: flagServerCert,
		Insecure:   flagInsecure,
	}
}

var (
	flagOutput     string
	flagHost       string
	flagYes        bool
	flagTLSCert    string
	flagTLSKey     string
	flagServerCert string
	flagInsecure   bool
)

// Host returns the --host flag value.
func Host() string { return flagHost }

// TLSCert returns the --tls-cert flag value.
func TLSCert() string { return flagTLSCert }

// TLSKey returns the --tls-key flag value.
func TLSKey() string { return flagTLSKey }

// ServerCert returns the --server-cert flag value.
func ServerCert() string { return flagServerCert }

// Insecure returns the --insecure flag value.
func Insecure() bool { return flagInsecure }

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

	root.PersistentFlags().StringVar(&flagOutput, "format", "text", "Output format: text or json")
	root.PersistentFlags().StringVar(&flagHost, "host", "", "Remote daemon host:port")
	root.PersistentFlags().StringVar(&flagTLSCert, "tls-cert", "", "Client certificate for mTLS")
	root.PersistentFlags().StringVar(&flagTLSKey, "tls-key", "", "Client private key for mTLS")
	root.PersistentFlags().StringVar(&flagServerCert, "server-cert", "", "Pin server certificate (adds to TLS roots)")
	root.PersistentFlags().BoolVar(&flagInsecure, "insecure", false, "Skip server certificate verification")
	root.PersistentFlags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompts")

	root.AddCommand(
		newVersionCmd(info),
		newStartCmd(info.Version),
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
		newCertCmd(),
		newCleanCmd(),
		newMigrateCmd(),
		newInstallCmd(info),
		newUninstallCmd(),
		newSnapshotCmd(),
		newUpgradeCmd(),

		newValidateCmd(),
		newWebUICmd(),
		newWorkspaceCmd(),
		newCompletionCmd(),
		setup.NewSetupCmd(),
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
