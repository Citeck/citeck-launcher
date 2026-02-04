package cli

import (
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate shell completion script for citeck.

To load completions:

Bash:
  $ source <(citeck completion bash)
  # Or add to ~/.bashrc:
  $ citeck completion bash > /etc/bash_completion.d/citeck

Zsh:
  $ citeck completion zsh > "${fpath[1]}/_citeck"
  $ compinit

Fish:
  $ citeck completion fish | source
  $ citeck completion fish > ~/.config/fish/completions/citeck.fish

PowerShell:
  PS> citeck completion powershell | Out-String | Invoke-Expression
`,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return nil
		},
	}
	return cmd
}
