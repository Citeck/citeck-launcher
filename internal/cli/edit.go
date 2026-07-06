package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

var errNoChanges = errors.New("no changes")

type appConfigClient interface {
	GetAppConfig(name string) (*api.AppConfigDto, error)
	PutAppConfig(name string, body []byte) (*api.ActionResultDto, error)
	ResetAppConfig(name string) (*api.ActionResultDto, error)
}

type editOptions struct {
	app   string
	reset bool
	file  io.Reader // non-nil ⇒ non-interactive: read all, then PUT
	isTTY bool
	cl    appConfigClient
	edit  func(initial []byte) (edited []byte, changed bool, err error)
}

func runEdit(o editOptions) (*api.ActionResultDto, error) {
	if o.reset {
		res, err := o.cl.ResetAppConfig(o.app)
		if err != nil {
			return nil, fmt.Errorf("reset %q: %w", o.app, err)
		}
		return res, nil
	}

	if o.file != nil {
		body, err := io.ReadAll(o.file)
		if err != nil {
			return nil, fmt.Errorf("read input: %w", err)
		}
		res, err := o.cl.PutAppConfig(o.app, body)
		if err != nil {
			return nil, fmt.Errorf("apply %q: %w", o.app, err)
		}
		return res, nil
	}

	if !o.isTTY {
		return nil, errors.New("no terminal for the interactive editor; pipe YAML with `--from -` or use `--reset`")
	}

	cfg, err := o.cl.GetAppConfig(o.app)
	if err != nil {
		return nil, fmt.Errorf("get config for %q: %w", o.app, err)
	}
	buf := []byte(editHeader(o.app) + cfg.Content)
	for {
		edited, changed, err := o.edit(buf)
		if err != nil {
			return nil, fmt.Errorf("edit %q: %w", o.app, err)
		}
		if !changed {
			return nil, errNoChanges
		}
		res, err := o.cl.PutAppConfig(o.app, edited)
		if err == nil {
			return res, nil
		}
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
			buf = []byte(editErrorHeader(apiErr.Message) + string(stripLeadingComments(edited)))
			continue
		}
		return nil, fmt.Errorf("apply %q: %w", o.app, err)
	}
}

func editHeader(app string) string {
	return fmt.Sprintf(
		"# Editing config for app %q. Save and exit to apply; leave unchanged to cancel.\n"+
			"# Lines starting with '#' are ignored and do not affect the saved config.\n",
		app)
}

func editErrorHeader(msg string) string {
	return "# ERROR applying your edit — fix and save again (or exit unchanged to cancel):\n# " +
		strings.ReplaceAll(strings.TrimSpace(msg), "\n", "\n# ") + "\n"
}

func stripLeadingComments(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "#") || strings.TrimSpace(lines[i]) == "") {
		i++
	}
	return []byte(strings.Join(lines[i:], "\n"))
}

func openFileInput(file string, stdin io.Reader) (io.Reader, func(), error) {
	if file == "-" {
		return stdin, func() {}, nil
	}
	f, err := os.Open(file) //nolint:gosec // G304: path is an operator-supplied CLI arg
	if err != nil {
		return nil, nil, fmt.Errorf("open %q: %w", file, err)
	}
	return f, func() { _ = f.Close() }, nil
}

func newEditCmd() *cobra.Command {
	var reset, listFiles, noApply bool
	var mountedFile, from string

	cmd := &cobra.Command{
		Use:   "edit <app>",
		Short: "Edit an app's config or a mounted config file (memory, image, env, application-launcher.yml, …)",
		Long: "Edit an app's effective ApplicationDef in $EDITOR and save the change as a\n" +
			"per-app override patch (like `kubectl edit`); the container is recreated (or\n" +
			"the change applies on next start when the namespace is stopped).\n\n" +
			"With --file, edit a mounted config file instead (e.g. application-launcher.yml).\n" +
			"The edit is stored as a delta over the generated content and applied via a\n" +
			"reload; use --no-apply to save without reloading.\n\n" +
			"  citeck edit <app>                       # edit the ApplicationDef\n" +
			"  citeck edit <app> --reset               # drop the ApplicationDef override\n" +
			"  citeck edit <app> --from - < def.yaml   # set the ApplicationDef from stdin\n" +
			"  citeck edit <app> --list-files          # list editable mounted files\n" +
			"  citeck edit <app> --file <path>         # edit a mounted file in $EDITOR\n" +
			"  citeck edit <app> --file <path> --reset # restore that file to default\n" +
			"  citeck edit <app> --file <path> --from - < f  # set that file from stdin",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("app name required (e.g. `citeck edit rabbitmq`); to view namespace.yml use `citeck config`")
			}
			app := args[0]
			if reset && from != "" {
				return errors.New("--reset and --from are mutually exclusive")
			}

			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			if listFiles {
				if reset || from != "" || mountedFile != "" {
					return errors.New("--list-files cannot be combined with --reset/--from/--file")
				}
				return runListFiles(c, app)
			}

			// --file selects a mounted config file as the edit target; without
			// it, the target is the ApplicationDef.
			if mountedFile != "" {
				o := editFileOptions{
					app:   app,
					path:  mountedFile,
					reset: reset,
					apply: !noApply,
					isTTY: output.IsTTY(),
					cl:    c,
					edit:  func(initial []byte) ([]byte, bool, error) { return openInEditor(initial, editorSuffixFor(mountedFile)) },
				}
				if from != "" {
					r, closeFn, ferr := openFileInput(from, cmd.InOrStdin())
					if ferr != nil {
						return ferr
					}
					defer closeFn()
					o.file = r
				}
				return finishEdit(runEditFile(o))
			}

			if noApply {
				return errors.New("--no-apply only applies with --file")
			}
			o := editOptions{
				app:   app,
				reset: reset,
				isTTY: output.IsTTY(),
				cl:    c,
				edit:  func(initial []byte) ([]byte, bool, error) { return openInEditor(initial, ".yaml") },
			}
			if from != "" {
				r, closeFn, ferr := openFileInput(from, cmd.InOrStdin())
				if ferr != nil {
					return ferr
				}
				defer closeFn()
				o.file = r
			}
			return finishEdit(runEdit(o))
		},
	}

	cmd.Flags().BoolVar(&reset, "reset", false, "Restore the target (ApplicationDef, or --file) to its generated default")
	cmd.Flags().StringVar(&from, "from", "", "Read new content from a file (or - for stdin) instead of launching an editor")
	cmd.Flags().StringVar(&mountedFile, "file", "", "Edit a mounted config file (e.g. app/<app>/props/application-launcher.yml) instead of the ApplicationDef")
	cmd.Flags().BoolVar(&listFiles, "list-files", false, "List the app's editable mounted files (with a * on edited ones)")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "With --file: save the edit without reloading (apply later with `citeck reload`)")
	return cmd
}

// finishEdit renders the shared success/no-change output for both editors.
func finishEdit(res *api.ActionResultDto, err error) error {
	if err != nil {
		if errors.Is(err, errNoChanges) {
			output.PrintText("No changes, edit canceled.")
			return nil
		}
		return err
	}
	output.PrintResult(res, func() { output.PrintText(res.Message) })
	return nil
}
