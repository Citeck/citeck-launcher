package cli

import (
	"errors"
	"os"

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

func Host() string       { return flagHost }
func TLSCert() string    { return flagTLSCert }
func TLSKey() string     { return flagTLSKey }
func ServerCert() string { return flagServerCert }
func Insecure() bool     { return flagInsecure }
func Yes() bool          { return flagYes }

// BuildInfo holds version metadata injected via ldflags.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

func NewRootCmd(info BuildInfo) *cobra.Command {
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
		newInstallCmd(),
		newUninstallCmd(),
		newSnapshotCmd(),
		newValidateCmd(),
		newCompletionCmd(),
	)

	return root
}

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
