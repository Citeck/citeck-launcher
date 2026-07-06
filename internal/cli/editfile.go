package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
)

// editableFileExtensions mirrors the web UI's EDITABLE_FILE_EXTENSIONS
// (web/src/lib/files.ts) / Kotlin EDITABLE_FILE_EXTENSIONS: only these mounted
// files are offered for editing, so binary mounts (certs, fonts, jars) don't
// show up as edit targets. "Dockerfile" is matched by base name (no extension).
var editableFileExtensions = map[string]bool{
	"yaml": true, "yml": true, "json": true, "kt": true, "java": true,
	"js": true, "lua": true, "sh": true, "txt": true, "conf": true,
}

// isEditableFile reports whether a mounted file path is a textual, editable
// target. Go port of web/src/lib/files.ts isEditableFile.
func isEditableFile(path string) bool {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if base == "Dockerfile" {
		return true
	}
	dot := strings.LastIndex(base, ".")
	if dot < 0 {
		return false
	}
	return editableFileExtensions[base[dot+1:]]
}

// appFileClient is the daemon surface used to edit a single mounted file. Split
// from appConfigClient so tests can stub each independently.
type appFileClient interface {
	GetAppFile(name, path string) (*api.AppFileContentDto, error)
	PutAppFile(name, path string, body []byte) (*api.ActionResultDto, error)
	ResetAppFile(name, path string) (*api.ActionResultDto, error)
	ReloadNamespace() (*api.ActionResultDto, error)
}

type editFileOptions struct {
	app   string
	path  string
	reset bool
	apply bool      // reload the namespace after a successful save
	file  io.Reader // non-nil ⇒ non-interactive: read all, then PUT
	isTTY bool
	cl    appFileClient
	edit  func(initial []byte) (edited []byte, changed bool, err error)
}

// runEditFile edits a single mounted file (e.g. application-launcher.yml).
// Unlike the ApplicationDef editor, no comment header is injected into the
// buffer — the file may be JSON/Lua/etc. where "#" is not a comment.
func runEditFile(o editFileOptions) (*api.ActionResultDto, error) {
	if o.reset {
		res, err := o.cl.ResetAppFile(o.app, o.path) // server reloads itself
		if err != nil {
			return nil, fmt.Errorf("reset file %q of %q: %w", o.path, o.app, err)
		}
		return res, nil
	}

	if o.file != nil {
		body, err := io.ReadAll(o.file)
		if err != nil {
			return nil, fmt.Errorf("read input: %w", err)
		}
		return o.putAndApply(body)
	}

	if !o.isTTY {
		return nil, errors.New("no terminal for the interactive editor; pipe content with `--from -` or use `--reset`")
	}

	cfg, err := o.cl.GetAppFile(o.app, o.path)
	if err != nil {
		return nil, fmt.Errorf("get file %q of %q: %w", o.path, o.app, err)
	}
	buf := []byte(cfg.Content)
	for {
		edited, changed, editErr := o.edit(buf)
		if editErr != nil {
			return nil, fmt.Errorf("edit %q: %w", o.path, editErr)
		}
		if !changed {
			return nil, errNoChanges
		}
		res, putErr := o.putAndApply(edited)
		if putErr == nil {
			return res, nil
		}
		var apiErr *client.APIError
		if errors.As(putErr, &apiErr) && apiErr.Status == http.StatusBadRequest {
			// A textual file can't carry a comment header for every format, so
			// surface the validation error on the terminal and re-open the
			// same content for another pass.
			output.PrintText("Error applying your edit: %s", strings.TrimSpace(apiErr.Message))
			buf = edited
			continue
		}
		return nil, putErr
	}
}

// putAndApply saves the file and — unless apply is off — reloads so the change
// reaches the running container. The raw PutAppFile error is returned unwrapped
// so the 400 retry in runEditFile can still match *client.APIError.
func (o editFileOptions) putAndApply(body []byte) (*api.ActionResultDto, error) {
	res, err := o.cl.PutAppFile(o.app, o.path, body)
	if err != nil {
		// %w keeps the *client.APIError reachable for the 400 retry in runEditFile.
		return nil, fmt.Errorf("save file %q: %w", o.path, err)
	}
	if o.apply {
		if _, rerr := o.cl.ReloadNamespace(); rerr != nil {
			return nil, fmt.Errorf("file %s saved but reload failed (run `citeck reload`): %w", o.path, rerr)
		}
		res.Message = fmt.Sprintf("File %s saved and applied", o.path)
		return res, nil
	}
	res.Message = fmt.Sprintf("File %s saved; run `citeck reload` to apply", o.path)
	return res, nil
}

// editorSuffixFor returns the temp-file suffix that gives the editor the right
// syntax highlighting for a mounted file path.
func editorSuffixFor(path string) string {
	if ext := filepath.Ext(path); ext != "" {
		return ext
	}
	return "" // e.g. Dockerfile — no extension
}

// runListFiles prints an app's editable mounted files, marking edited ones.
func runListFiles(cl interface {
	ListAppFiles(name string) ([]api.AppFileDto, error)
}, app string) error {
	files, err := cl.ListAppFiles(app)
	if err != nil {
		return fmt.Errorf("list files for %q: %w", app, err)
	}
	shown := 0
	for _, f := range files {
		if !isEditableFile(f.Path) {
			continue
		}
		shown++
		if f.Edited {
			output.PrintText("%s %s", f.Path, output.Colorize(output.Dim, "*"))
		} else {
			output.PrintText("%s", f.Path)
		}
	}
	if shown == 0 {
		output.PrintText("App %q has no editable mounted files.", app)
	}
	return nil
}
