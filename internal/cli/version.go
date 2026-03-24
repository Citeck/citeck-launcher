package cli

import (
	"runtime"

	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := map[string]string{
				"version": version,
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
				"go":      runtime.Version(),
			}

			output.PrintResult(info, func() {
				output.PrintText("Citeck CLI %s", version)
				output.PrintText("OS:   %s/%s", runtime.GOOS, runtime.GOARCH)
				output.PrintText("Go:   %s", runtime.Version())
			})
			return nil
		},
	}
}
