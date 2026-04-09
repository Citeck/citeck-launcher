package cli

import (
	"runtime"

	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newVersionCmd(info BuildInfo) *cobra.Command {
	version, commit, buildDate := info.Version, info.Commit, info.BuildDate
	var short bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if short {
				output.PrintText("%s", version)
				return nil
			}

			data := map[string]string{
				"version": version,
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
				"go":      runtime.Version(),
			}
			if commit != "" {
				data["commit"] = commit
			}
			if buildDate != "" {
				data["buildDate"] = buildDate
			}

			output.PrintResult(data, func() {
				output.PrintText("Citeck CLI %s", version)
				if commit != "" {
					output.PrintText("Commit: %s", commit)
				}
				if buildDate != "" {
					output.PrintText("Built:  %s", buildDate)
				}
				output.PrintText("OS:     %s/%s", runtime.GOOS, runtime.GOARCH)
				output.PrintText("Go:     %s", runtime.Version())
			})
			return nil
		},
	}

	cmd.Flags().BoolVar(&short, "short", false, "Print version number only")
	return cmd
}
