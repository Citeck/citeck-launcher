package cli

import (
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// `citeck jcmd` / `jstack` / `jmap` — the JDK tool names on purpose.
//
// A generic `diagnostics <app>` would promise more than it delivers (not every
// app is a JVM), while these names say "this is a JVM thing" without inventing
// vocabulary, and `citeck jcmd <app> help` prints the command list the JVM
// itself reports — so nothing here needs maintaining across JDK versions.
//
// None of it requires a JDK in the image: the launcher speaks the HotSpot
// attach protocol, from the host where it can and from inside the container
// otherwise.

func newJcmdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "jcmd <app> <command> [args...]",
		Short: "Run a jcmd command against an app's JVM (try: jcmd <app> help)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runJVMCommand(args[0], args[1], args[2:])
		},
	}
}

func newJstackCmd() *cobra.Command {
	var locks bool
	cmd := &cobra.Command{
		Use:   "jstack <app>",
		Short: "Print a thread dump of an app's JVM",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var extra []string
			if locks {
				extra = append(extra, "-l")
			}
			return runJVMCommand(args[0], "Thread.print", extra)
		},
	}
	cmd.Flags().BoolVarP(&locks, "locks", "l", false, "Include ownable synchronizers (java.util.concurrent locks)")
	return cmd
}

func newJmapCmd() *cobra.Command {
	var out string
	var histo bool
	var keep bool

	cmd := &cobra.Command{
		Use:   "jmap <app>",
		Short: "Take a heap dump of an app's JVM and bring it here",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			appName := args[0]
			if histo {
				// A histogram is an answer, not an artifact: it goes to stdout
				// and nothing is written anywhere.
				return runJVMCommand(appName, "GC.class_histogram", nil)
			}
			return runHeapDump(appName, out, keep)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Write the dump to this path (default: its own name in the current directory)")
	cmd.Flags().BoolVar(&histo, "histo", false, "Print a class histogram instead of writing a dump")
	cmd.Flags().BoolVar(&keep, "keep", false, "Leave the dump in the app's export directory instead of downloading it")
	return cmd
}

func runJVMCommand(appName, command string, args []string) error {
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()

	res, err := c.JVMCommand(appName, command, args)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	output.PrintResult(res, func() {
		fmt.Print(res.Output)
		if !strings.HasSuffix(res.Output, "\n") {
			fmt.Println()
		}
	})
	return nil
}

// runHeapDump composes the whole flow: the daemon suspends the app's liveness
// probe, writes a gzipped dump into the export directory, and resumes; the
// file is then handed over here and removed from the export directory, because
// a heap dump left lying around is heap-sized and full of secrets.
func runHeapDump(appName, out string, keep bool) error {
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()

	dump, err := c.HeapDump(appName)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	if keep {
		output.PrintResult(dump, func() {
			output.PrintText("Heap dump written to the export directory of %s: %s (%s)",
				appName, dump.File, formatExportSize(dump.Size))
			output.PrintText("Fetch it with: citeck export get %s %s", appName, dump.File)
		})
		return nil
	}

	dest := out
	if dest == "" {
		dest = dump.File
	}
	if destErr := refuseExistingDest(dest, false); destErr != nil {
		// The dump exists and is named in the message — refusing to overwrite
		// must not also mean losing it.
		return fmt.Errorf("%w (the dump is in the export directory as %s)", destErr, dump.File)
	}

	files, err := c.ListAppExports(appName)
	if err != nil {
		return fmt.Errorf("list exports: %w", err)
	}
	meta, ok := findExportFile(files, dump.File)
	if !ok {
		return fmt.Errorf("the dump %q is not in %s's export directory", dump.File, appName)
	}

	how, err := fetchExport(c, appName, meta, dest, true)
	if err != nil {
		return err
	}
	output.PrintResult(map[string]any{
		"app": appName, "file": dump.File, "path": dest, "size": meta.Size, "method": how,
	}, func() {
		output.PrintText("Heap dump of %s (%s) saved to %s", appName, formatExportSize(meta.Size), dest)
		output.PrintText("This file may contain passwords and personal data in the clear — it was written with mode 0600, keep it that way.")
	})
	return nil
}
