package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/citeck/citeck-launcher/internal/output"
)

// Recovery for an interactive edit the launcher could not deliver.
//
// `citeck edit <app>` and `citeck edit <app> --file <path>` hand the operator's
// text to the daemon in one PUT. When the daemon REJECTS the content (400) the
// editor re-opens with the error on top, which is the right answer: the text is
// wrong and only the operator can fix it. Every other failure is one where they
// did nothing wrong — a 409 while an Update & Start, a snapshot or a dependency
// migration holds the long-op lock, a 503 with Docker down, a 500, a socket
// dropped because the daemon was restarted mid-edit — and the temp file used to
// be removed on the way out (`defer os.Remove` inside openInEditor), taking ten
// minutes of editing application-launcher.yml with it.
//
// So the temp file's OWNER is the caller of openInEditor, not openInEditor: it
// is removed on the outcomes where nothing of the operator's is in it (a
// successful apply, "leave unchanged to cancel", an editor that failed before
// it ever produced their text) and kept on the rest, with the path and the real
// re-apply command printed to stderr. There is deliberately no list of
// "retryable" status codes to maintain — 400 loops, everything else keeps the
// file.

// keptEditError is a failed edit whose text is still on disk: path names the
// temp file that holds it and reapply is the command that sends it again.
type keptEditError struct {
	err     error
	path    string
	reapply string
}

func (e *keptEditError) Error() string { return e.err.Error() }
func (e *keptEditError) Unwrap() error { return e.err }

// keepEdit marks err as "the text survived, here is the way back". A caller
// with no temp file to offer (openInEditor never got as far as creating one)
// gets its error back unchanged, so the recovery lines are never printed for a
// path that does not exist.
func keepEdit(err error, path, reapply string) error {
	if path == "" {
		return err
	}
	return &keptEditError{err: err, path: path, reapply: reapply}
}

// discardTempEdit removes a temp file the caller owns. Best-effort: it runs on
// paths that already succeeded or were already canceled, and a leftover file in
// the OS temp directory must never turn either into a failure.
func discardTempEdit(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// reportKeptEdit prints where a failed edit was kept and how to re-apply it.
//
// STDERR, like the "applied, but not saved" warning next door and for the same
// two reasons: `--format json` writes the command's answer to stdout and a
// stray text line would corrupt it for a scripted caller, and this is a note
// about the operator's own copy rather than part of that answer.
func reportKeptEdit(err error) {
	var kept *keptEditError
	if !errors.As(err, &kept) {
		return
	}
	ensureI18n()
	output.Errf("%s", t("cli.edit.kept", "path", kept.path))
	output.Errf("%s", t("cli.edit.keptReapply", "cmd", kept.reapply))
}

// reapplyAppConfigCmd renders the way back for the ApplicationDef editor.
func reapplyAppConfigCmd(app, tmp string) string {
	return fmt.Sprintf("citeck edit %s --from %s", app, tmp)
}

// reapplyCmd renders the way back for the mounted-file editor: the same
// invocation that failed, with --from pointing at the kept text. --no-apply is
// part of it when it was part of the original command, so what the operator
// pastes does exactly what they asked for the first time.
func (o editFileOptions) reapplyCmd(tmp string) string {
	cmd := fmt.Sprintf("citeck edit %s --file %s --from %s", o.app, o.path, tmp)
	if !o.apply {
		cmd += " --no-apply"
	}
	return cmd
}

// savedEditError marks the one failure that is NOT a lost edit: the daemon
// accepted the content and only the reload that carries it into the container
// failed. Re-applying the temp copy would re-save what is already saved, so the
// file is discarded and the message's own `citeck reload` is the way forward.
type savedEditError struct{ err error }

func (e *savedEditError) Error() string { return e.err.Error() }
func (e *savedEditError) Unwrap() error { return e.err }

// editWasSaved reports whether err happened after the daemon took the content.
func editWasSaved(err error) bool {
	var saved *savedEditError
	return errors.As(err, &saved)
}
