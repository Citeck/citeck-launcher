package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// editRound is one $EDITOR session as the fake editor plays it: what the editor
// leaves in the temp file, whether that counts as a change, and (for the
// editor-failed case) the error instead.
type editRound struct {
	out     string
	changed bool
	err     error
}

// fakeEditor mimics openInEditor's OWNERSHIP contract: every round writes a
// REAL temp file and hands the path back to the caller, who is the only one
// allowed to remove it. That is what lets these tests assert which files
// survived a failure and which were cleaned up.
type fakeEditor struct {
	t      *testing.T
	dir    string
	rounds []editRound
	paths  []string
	bufs   [][]byte
}

func newFakeEditor(t *testing.T, rounds ...editRound) *fakeEditor {
	t.Helper()
	return &fakeEditor{t: t, dir: t.TempDir(), rounds: rounds}
}

func (e *fakeEditor) fn(buf []byte) (edited []byte, changed bool, tmp string, err error) {
	e.t.Helper()
	i := len(e.paths)
	require.Less(e.t, i, len(e.rounds), "editor opened more times than the test scripted")
	r := e.rounds[i]
	path := filepath.Join(e.dir, fmt.Sprintf("citeck-edit-%d.yaml", i))
	body := []byte(r.out)
	if r.err != nil {
		// The editor failed: the file holds whatever we seeded it with.
		body = buf
	}
	require.NoError(e.t, os.WriteFile(path, body, 0o600))
	e.paths = append(e.paths, path)
	e.bufs = append(e.bufs, buf)
	if r.err != nil {
		return nil, false, path, r.err
	}
	return body, r.changed, path, nil
}

func (e *fakeEditor) requireGone(round int) {
	e.t.Helper()
	require.Greater(e.t, len(e.paths), round, "round %d never opened", round)
	_, err := os.Stat(e.paths[round])
	assert.Truef(e.t, os.IsNotExist(err), "temp file of round %d should have been removed: %v", round, err)
}

func (e *fakeEditor) requireKept(round int, want string) {
	e.t.Helper()
	require.Greater(e.t, len(e.paths), round, "round %d never opened", round)
	b, err := os.ReadFile(e.paths[round])
	require.NoErrorf(e.t, err, "temp file of round %d should have been kept", round)
	assert.Equal(e.t, want, string(b))
}

// healthyStore is the stateWriteChecker finishEdit consults on the SUCCESS
// path; the failure paths never reach it.
type healthyStore struct{}

func (healthyStore) GetNamespace() (*api.NamespaceDto, error) { return &api.NamespaceDto{}, nil }

func apiErr(status int, msg string) error {
	return &client.APIError{Status: status, Message: msg}
}

// ---------------------------------------------------------------------------
// ApplicationDef editor
// ---------------------------------------------------------------------------

// The reported case: ten minutes of editing, saved into somebody else's reload.
// The daemon refuses with 409 — the operator did nothing wrong — so their text
// must still be on disk afterwards.
func TestRunEdit_AFailedApplyKeepsTheOperatorsText(t *testing.T) {
	f := &fakeConfigClient{
		getDTO:  &api.AppConfigDto{Content: "name: rabbitmq\n"},
		putErrs: []error{apiErr(http.StatusConflict, "another operation is in progress")},
	}
	ed := newFakeEditor(t, editRound{out: "name: rabbitmq\nmemory: 4g\n", changed: true})

	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.Error(t, err)
	ed.requireKept(0, "name: rabbitmq\nmemory: 4g\n")

	var kept *keptEditError
	require.ErrorAs(t, err, &kept, "a failed apply must report where the text was kept")
	assert.Equal(t, ed.paths[0], kept.path)
	assert.Equal(t, "citeck edit rabbitmq --from "+ed.paths[0], kept.reapply)
}

// The whole point of keeping it: the printed command has to be the real one.
func TestRunEdit_KeptEditPrintsBothRecoveryLinesToStderr(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	f := &fakeConfigClient{
		getDTO:  &api.AppConfigDto{Content: "name: rabbitmq\n"},
		putErrs: []error{apiErr(http.StatusServiceUnavailable, "docker is not available")},
	}
	ed := newFakeEditor(t, editRound{out: "name: rabbitmq\n# edited\n", changed: true})

	var runErr error
	stderr := captureStderr(t, func() {
		res, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})
		runErr = finishEdit(healthyStore{}, res, err)
	})

	require.Error(t, runErr)
	assert.Contains(t, stderr, ed.paths[0])
	assert.Contains(t, stderr, "citeck edit rabbitmq --from "+ed.paths[0])
}

func TestRunEdit_SuccessRemovesTheTempFile(t *testing.T) {
	f := &fakeConfigClient{getDTO: &api.AppConfigDto{Content: "name: rabbitmq\n"}}
	ed := newFakeEditor(t, editRound{out: "name: rabbitmq\nmemory: 4g\n", changed: true})

	res, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.NoError(t, err)
	require.NotNil(t, res)
	ed.requireGone(0)
}

// "Leave unchanged to cancel" is an answer, not a loss.
func TestRunEdit_CancelRemovesTheTempFile(t *testing.T) {
	f := &fakeConfigClient{getDTO: &api.AppConfigDto{Content: "name: rabbitmq\n"}}
	ed := newFakeEditor(t, editRound{out: "name: rabbitmq\n", changed: false})

	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.ErrorIs(t, err, errNoChanges)
	ed.requireGone(0)
	var kept *keptEditError
	assert.NotErrorAs(t, err, &kept, "a cancel keeps nothing and says nothing")
}

// The 400 loop is unchanged — and must not leave a file per round behind.
func TestRunEdit_The400LoopKeepsNoFilePerRound(t *testing.T) {
	f := &fakeConfigClient{
		getDTO:  &api.AppConfigDto{Content: "name: rabbitmq\n"},
		putErrs: []error{apiErr(http.StatusBadRequest, "invalid YAML"), nil},
	}
	ed := newFakeEditor(t,
		editRound{out: "name: [\n", changed: true},
		editRound{out: "name: rabbitmq\n", changed: true},
	)

	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.NoError(t, err)
	require.Len(t, ed.paths, 2)
	ed.requireGone(0)
	ed.requireGone(1)
}

// ...and when the retry round fails for a reason that is NOT the content, the
// file left behind is exactly one: the last round's.
func TestRunEdit_A400ThenAFailureKeepsExactlyOneFile(t *testing.T) {
	f := &fakeConfigClient{
		getDTO:  &api.AppConfigDto{Content: "name: rabbitmq\n"},
		putErrs: []error{apiErr(http.StatusBadRequest, "invalid YAML"), apiErr(http.StatusConflict, "busy")},
	}
	ed := newFakeEditor(t,
		editRound{out: "name: [\n", changed: true},
		editRound{out: "name: rabbitmq\nmemory: 4g\n", changed: true},
	)

	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.Error(t, err)
	ed.requireGone(0)
	ed.requireKept(1, "name: rabbitmq\nmemory: 4g\n")
}

// An editor that never ran holds nothing of the operator's: the buffer in the
// file is the daemon's own config, so there is nothing to keep and nothing to
// re-apply.
func TestRunEdit_AnEditorThatFailedBeforeAnyTextKeepsNothing(t *testing.T) {
	f := &fakeConfigClient{getDTO: &api.AppConfigDto{Content: "name: rabbitmq\n"}}
	ed := newFakeEditor(t, editRound{err: errors.New("executable file not found in $PATH")})

	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.Error(t, err)
	ed.requireGone(0)
	var kept *keptEditError
	assert.NotErrorAs(t, err, &kept)
}

// But on a retry round the buffer IS the operator's text — the launcher seeded
// the file with it — so an editor failure there must not throw it away.
func TestRunEdit_AnEditorFailureOnTheRetryRoundKeepsTheText(t *testing.T) {
	f := &fakeConfigClient{
		getDTO:  &api.AppConfigDto{Content: "name: rabbitmq\n"},
		putErrs: []error{apiErr(http.StatusBadRequest, "invalid YAML")},
	}
	ed := newFakeEditor(t,
		editRound{out: "name: rabbitmq\nmemory: 4g\n", changed: true},
		editRound{err: errors.New("exit status 1")},
	)

	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: true, cl: f, edit: ed.fn})

	require.Error(t, err)
	ed.requireGone(0)
	var kept *keptEditError
	require.ErrorAs(t, err, &kept)
	assert.Equal(t, ed.paths[1], kept.path)
	assert.Contains(t, string(mustRead(t, ed.paths[1])), "memory: 4g")
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a temp file this test created
	require.NoError(t, err)
	return b
}

// ---------------------------------------------------------------------------
// Mounted-file editor
// ---------------------------------------------------------------------------

func TestRunEditFile_AFailedSaveKeepsTheTextWithItsOwnReapplyForm(t *testing.T) {
	f := &fakeFileClient{
		getDTO:  &api.AppFileContentDto{Content: "logging:\n  level: INFO\n"},
		putErrs: []error{apiErr(http.StatusConflict, "another operation is in progress")},
	}
	ed := newFakeEditor(t, editRound{out: "logging:\n  level: DEBUG\n", changed: true})
	o := editFileOptions{
		app: "uiserv", path: "app/uiserv/props/application-launcher.yml",
		apply: true, isTTY: true, cl: f, edit: ed.fn,
	}

	_, err := runEditFile(o)

	require.Error(t, err)
	ed.requireKept(0, "logging:\n  level: DEBUG\n")
	var kept *keptEditError
	require.ErrorAs(t, err, &kept)
	assert.Equal(t,
		"citeck edit uiserv --file app/uiserv/props/application-launcher.yml --from "+ed.paths[0],
		kept.reapply)
	assert.Zero(t, f.reloadHits)
}

// --no-apply is part of the invocation that failed, so it is part of the way back.
func TestRunEditFile_TheReapplyLineCarriesNoApply(t *testing.T) {
	f := &fakeFileClient{
		getDTO:  &api.AppFileContentDto{Content: "a: 1\n"},
		putErrs: []error{apiErr(http.StatusServiceUnavailable, "docker is not available")},
	}
	ed := newFakeEditor(t, editRound{out: "a: 2\n", changed: true})
	o := editFileOptions{app: "uiserv", path: "p.yml", apply: false, isTTY: true, cl: f, edit: ed.fn}

	_, err := runEditFile(o)

	var kept *keptEditError
	require.ErrorAs(t, err, &kept)
	assert.Equal(t, "citeck edit uiserv --file p.yml --from "+ed.paths[0]+" --no-apply", kept.reapply)
}

func TestRunEditFile_SuccessRemovesTheTempFileAndCancelDoesToo(t *testing.T) {
	ok := &fakeFileClient{getDTO: &api.AppFileContentDto{Content: "a: 1\n"}}
	saved := newFakeEditor(t, editRound{out: "a: 2\n", changed: true})
	_, err := runEditFile(editFileOptions{app: "uiserv", path: "p.yml", apply: true, isTTY: true, cl: ok, edit: saved.fn})
	require.NoError(t, err)
	saved.requireGone(0)

	canceled := newFakeEditor(t, editRound{out: "a: 1\n", changed: false})
	_, err = runEditFile(editFileOptions{app: "uiserv", path: "p.yml", apply: true, isTTY: true, cl: ok, edit: canceled.fn})
	require.ErrorIs(t, err, errNoChanges)
	canceled.requireGone(0)
}

func TestRunEditFile_The400LoopKeepsNoFilePerRound(t *testing.T) {
	f := &fakeFileClient{
		getDTO:  &api.AppFileContentDto{Content: "a: 1\n"},
		putErrs: []error{apiErr(http.StatusBadRequest, "invalid YAML"), nil},
	}
	ed := newFakeEditor(t,
		editRound{out: "a: [\n", changed: true},
		editRound{out: "a: 2\n", changed: true},
	)

	_, err := runEditFile(editFileOptions{app: "uiserv", path: "p.yml", apply: true, isTTY: true, cl: f, edit: ed.fn})

	require.NoError(t, err)
	ed.requireGone(0)
	ed.requireGone(1)
}

// A reload that failed is NOT a lost edit — the daemon already has the content,
// and the message says `citeck reload`. Telling the operator to re-apply their
// temp copy would send them after work that is already saved.
func TestRunEditFile_ASavedFileWhoseReloadFailedIsNotAKeptEdit(t *testing.T) {
	f := &fakeFileClient{
		getDTO:    &api.AppFileContentDto{Content: "a: 1\n"},
		reloadErr: apiErr(http.StatusConflict, "another operation is in progress"),
	}
	ed := newFakeEditor(t, editRound{out: "a: 2\n", changed: true})

	_, err := runEditFile(editFileOptions{app: "uiserv", path: "p.yml", apply: true, isTTY: true, cl: f, edit: ed.fn})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "citeck reload")
	ed.requireGone(0)
	var kept *keptEditError
	assert.NotErrorAs(t, err, &kept, "the content is on the daemon; there is nothing to re-apply")
}

// The 400 loop must not confuse the file-editor's terminal error report with a
// kept edit either.
func TestRunEditFile_The400ReportGoesToTheTerminalNotTheRecoveryLines(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	f := &fakeFileClient{
		getDTO:  &api.AppFileContentDto{Content: "a: 1\n"},
		putErrs: []error{apiErr(http.StatusBadRequest, "invalid YAML"), nil},
	}
	ed := newFakeEditor(t,
		editRound{out: "a: [\n", changed: true},
		editRound{out: "a: 2\n", changed: true},
	)

	var runErr error
	stderr := captureStderr(t, func() {
		res, err := runEditFile(editFileOptions{app: "uiserv", path: "p.yml", apply: true, isTTY: true, cl: f, edit: ed.fn})
		runErr = finishEdit(healthyStore{}, res, err)
	})

	require.NoError(t, runErr)
	assert.NotContains(t, stderr, "--from")
	assert.NotContains(t, stderr, ed.dir, "no kept-file line on a successful retry")
}
