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
		return nil, errors.New("no terminal for the interactive editor; pipe YAML with `--file -` or use `--reset`")
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
	var reset bool
	var file string

	cmd := &cobra.Command{
		Use:   "edit <app>",
		Short: "Edit an app's config (memory limit, image, env, ports, …)",
		Long: "Open an app's effective ApplicationDef in $EDITOR and save the change as a per-app\n" +
			"override patch (like `kubectl edit`). The change is persisted and the container\n" +
			"recreated — or applied on next start if the namespace is stopped.\n\n" +
			"Use --reset to drop the override and restore the generated default, or\n" +
			"--file - to pipe YAML from stdin without launching an editor.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("app name required (e.g. `citeck edit rabbitmq`); to view namespace.yml use `citeck config`")
			}
			if reset && file != "" {
				return errors.New("--reset and --file are mutually exclusive")
			}

			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			o := editOptions{
				app:   args[0],
				reset: reset,
				isTTY: output.IsTTY(),
				cl:    c,
				edit:  func(initial []byte) ([]byte, bool, error) { return openInEditor(initial, ".yaml") },
			}
			if file != "" {
				r, closeFn, ferr := openFileInput(file, cmd.InOrStdin())
				if ferr != nil {
					return ferr
				}
				defer closeFn()
				o.file = r
			}

			res, err := runEdit(o)
			if err != nil {
				if errors.Is(err, errNoChanges) {
					output.PrintText("No changes, edit canceled.")
					return nil
				}
				return err
			}
			output.PrintResult(res, func() { output.PrintText(res.Message) })
			return nil
		},
	}

	cmd.Flags().BoolVar(&reset, "reset", false, "Clear the app's config override, restoring the generated default")
	cmd.Flags().StringVar(&file, "file", "", "Read new YAML from a file (or - for stdin) instead of launching an editor")
	return cmd
}
