package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reportFixture() migrationReport {
	start := time.Date(2026, 9, 22, 6, 40, 0, 0, time.UTC)
	return migrationReport{
		Kind: "upgrade", Namespace: "default", Dependency: "observer-postgres",
		From: "postgres:17.5", To: "postgres:18.1", Launcher: "2.14.0",
		StartedAt: start, FinishedAt: start.Add(97 * time.Second), StepCount: 10,
		Steps: []reportStep{
			{ID: "stop-namespace", Index: 1},
			{ID: "pull-image", Index: 2},
			{ID: "start-source", Index: 3},
		},
	}
}

// The failure case is the one the whole file exists for: the reason lives in a
// container the rollback has already removed, so it has to be IN the report.
func TestAFailedMigrationReportCarriesTheContainerLogs(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	rep := reportFixture()
	rep.Outcome = "failed"
	rep.Err = "step start-source: container depsmig-src did not become ready within 5m0s"
	rep.Steps = reportStepsFrom(rep.Steps, true)
	rep.Diagnostics = []migrate.Diagnostic{{
		Name: "logs of depsmig-src",
		Text: `FATAL:  role "postgres" does not exist`,
	}}

	path, err := writeMigrationReport(rep)
	require.NoError(t, err)
	body, err := os.ReadFile(path) //nolint:gosec // G304: the path the writer just returned
	require.NoError(t, err)
	text := string(body)

	assert.Contains(t, text, "observer-postgres")
	assert.Contains(t, text, "postgres:17.5 -> postgres:18.1")
	assert.Contains(t, text, "did not become ready")
	assert.Contains(t, text, `role "postgres" does not exist`,
		"without this the report says only 'timeout', which reads the same for a slow disk")
	assert.Contains(t, text, "[3/10] start-source     FAILED",
		"the step that failed must be named: the engine announces a step when it STARTS")
	assert.Contains(t, text, "[2/10] pull-image       ok")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(reportFilePerm), info.Mode().Perm(),
		"container logs can carry connection strings; the file is the launcher user's own")
}

// A migration that WORKED is reported too — it is what a later "it broke after
// the update" is read against.
func TestASuccessfulMigrationIsReportedAsWell(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	rep := reportFixture()
	rep.Outcome = "succeeded"
	path, err := writeMigrationReport(rep)
	require.NoError(t, err)

	body, err := os.ReadFile(path) //nolint:gosec // G304: the path the writer just returned
	require.NoError(t, err)
	assert.Contains(t, string(body), "outcome    : succeeded")
	assert.NotContains(t, string(body), "FAILED")
	assert.Contains(t, filepath.Base(path), "deps-upgrade-observer-postgres-20260922-064000.log")
}

// The directory is bounded, and the bound is applied by NAME because the name
// carries a UTC timestamp — no stat call, no clock skew.
func TestOldReportsArePrunedNewestKept(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	base := reportFixture()
	for i := range reportsKept + 5 {
		r := base
		r.Outcome = "succeeded"
		r.StartedAt = base.StartedAt.Add(time.Duration(i) * time.Minute)
		_, err := writeMigrationReport(r)
		require.NoError(t, err)
	}

	entries, err := os.ReadDir(filepath.Join(home, "logs", "reports"))
	require.NoError(t, err)
	assert.Len(t, entries, reportsKept)
	first := base.StartedAt.UTC().Format("20060102-150405")
	for _, e := range entries {
		assert.NotContains(t, e.Name(), first, "the oldest reports are the ones that go")
	}
}

// An id is not a path: a workspace declares its own database names.
func TestAReportFileNameCannotEscapeItsDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	rep := reportFixture()
	rep.Dependency = "../../etc/passwd"
	rep.Outcome = "failed"
	path, err := writeMigrationReport(rep)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "logs", "reports"), filepath.Dir(path))
	assert.NotContains(t, filepath.Base(path), "/")
	assert.NotContains(t, filepath.Base(path), "..")
}

// The system dump ships the directory, which is what makes "send me a dump" a
// complete answer to "it would not update".
func TestTheSystemDumpShipsTheReports(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	rep := reportFixture()
	rep.Outcome = "failed"
	rep.Err = "boom"
	_, err := writeMigrationReport(rep)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	(&Daemon{}).writeSystemDumpZip(context.Background(), rec, map[string]any{"daemon": "test"}, nil, nil)

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err)
	names := make([]string, 0, len(zr.File))
	var found bool
	for _, f := range zr.File {
		names = append(names, f.Name)
		if strings.HasPrefix(f.Name, "reports/deps-upgrade-observer-postgres-") {
			found = true
		}
	}
	assert.True(t, found, "reports/ missing from the dump: %v", names)
}

// The rendered report names the dependency in its header, where a reader looks
// first.
func TestTheRenderedReportNamesWhatItIsAbout(t *testing.T) {
	rep := reportFixture()
	rep.Outcome = "failed"
	assert.Contains(t, rep.render(), "dependency : observer-postgres")
}

// A refusal never reaches the runner, and it is the most common shape of "it
// would not update" — so it gets a report of its own.
func TestARefusedMigrationIsReportedToo(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	rep := reportFixture()
	rep.Outcome = "refused by the pre-checks"
	rep.Err = "Volume obs_postgres3 already exists; re-run with --replace-existing"
	rep.Steps = nil

	path, err := writeMigrationReport(rep)
	require.NoError(t, err)
	body, err := os.ReadFile(path) //nolint:gosec // G304: the path the writer just returned
	require.NoError(t, err)

	text := string(body)
	assert.Contains(t, text, "outcome    : refused by the pre-checks")
	assert.Contains(t, text, "already exists")
	assert.Contains(t, text, "(none reached)", "a refusal reached no step, and the report says so")
}
