package cli

import (
	"strings"
	"sync"

	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var localizeOnce sync.Once

// localizeCommands walks the command tree and replaces Short descriptions
// and flag usage strings with i18n translations when available.
//
// Key convention:
//
//	help.<cmd_path>.short  — command Short description
//	help.<cmd_path>.flag.<flag_name>  — flag usage text
//
// where <cmd_path> is the space-separated command path with "citeck" stripped,
// e.g. "cert webui" → "help.cert.webui.short".
func localizeCommands(root *cobra.Command) {
	localizeOnce.Do(func() {
		ensureI18n()
		walkCmd(root)
	})
}

func walkCmd(cmd *cobra.Command) {
	path := cmdKeyPath(cmd)

	if key := "help." + path + ".short"; i18n.HasKey(key) {
		cmd.Short = i18n.T(key)
	}
	if key := "help." + path + ".long"; i18n.HasKey(key) {
		cmd.Long = i18n.T(key)
	}

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		key := "help." + path + ".flag." + f.Name
		if i18n.HasKey(key) {
			f.Usage = i18n.T(key)
		}
	})

	for _, sub := range cmd.Commands() {
		walkCmd(sub)
	}
}

// cmdKeyPath returns the dot-separated command path without the root name.
// "citeck cert webui" → "cert.webui", "citeck logs" → "logs", "citeck" → "citeck".
func cmdKeyPath(cmd *cobra.Command) string {
	var parts []string
	for c := cmd; c != nil; c = c.Parent() {
		parts = append(parts, c.Name())
	}
	// Reverse; drop root ("citeck")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if len(parts) > 1 {
		parts = parts[1:]
	}
	return strings.Join(parts, ".")
}
