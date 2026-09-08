package namespace

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
)

// The migration engine drives the journal through this Runtime and nothing
// else; the seam is one-way (migrate must not import namespace), so the
// assertion lives here.
var _ migrate.JournalStore = (*Runtime)(nil)

func decodeState(t *testing.T, js string) NsPersistedState {
	t.Helper()
	var st NsPersistedState
	require.NoError(t, json.Unmarshal([]byte(js), &st))
	return st
}

func TestDependencyPinRoundTripsThroughPersistState(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	r.SetDependencyPin(deps.Postgres, "postgres:17.5")
	require.Equal(t, 1, fp.callCount())
	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, "postgres:17.5", st.Dependencies[deps.Postgres].Image)

	// An unrelated persist (what UpdateAppDef / StopApp do) must re-emit the
	// pin — persistState rebuilds the record from Runtime fields.
	r.mu.Lock()
	r.manualStoppedApps["edi"] = true
	_ = r.persistState()
	r.mu.Unlock()
	st = decodeState(t, fp.lastJSON())
	assert.Equal(t, "postgres:17.5", st.Dependencies[deps.Postgres].Image)
	assert.Contains(t, st.ManualStoppedApps, "edi")
}

func TestRestoreDependencyStateIsReadBack(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	j := &deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18", Step: "dump", WasRunning: true}
	last := &deps.MigrationResult{ID: deps.Postgres, Error: "boom"}
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, j, last)

	assert.Equal(t, map[deps.ID]string{deps.Postgres: "postgres:17.5"}, r.DependencyPins())
	require.NotNil(t, r.MigrationJournal())
	assert.Equal(t, "dump", r.MigrationJournal().Step)
	assert.Equal(t, "boom", r.LastDependencyMigration().Error)

	// Accessors return copies: mutating them must not reach the runtime.
	r.DependencyPins()[deps.Postgres] = "hacked"
	r.MigrationJournal().Step = "hacked"
	assert.Equal(t, "postgres:17.5", r.DependencyPins()[deps.Postgres])
	assert.Equal(t, "dump", r.MigrationJournal().Step)
}

func TestSetMigrationJournalPersistsAndNilClears(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	require.NoError(t, r.SetMigrationJournal(&deps.MigrationJournal{ID: deps.Postgres, Step: "pull-image"}))
	st := decodeState(t, fp.lastJSON())
	require.NotNil(t, st.DependencyMigration)
	assert.Equal(t, "pull-image", st.DependencyMigration.Step)

	require.NoError(t, r.SetMigrationJournal(nil))
	st = decodeState(t, fp.lastJSON())
	assert.Nil(t, st.DependencyMigration)
	assert.Nil(t, r.MigrationJournal())
}

func TestCommitMigrationIsOneWrite(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, Step: "stop-target"}, nil)

	res := deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		FinishedAt: time.Now(), OldVolume: "postgres2"}
	require.NoError(t, r.CommitMigration(deps.Postgres, "postgres:18", res))

	require.Equal(t, 1, fp.callCount(), "commit must be exactly one persist")
	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, "postgres:18", st.Dependencies[deps.Postgres].Image)
	assert.Nil(t, st.DependencyMigration)
	require.NotNil(t, st.LastDependencyMigration)
	assert.Equal(t, "postgres2", st.LastDependencyMigration.OldVolume)
}

func TestRecordMigrationFailureLeavesPinAlone(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, Step: "restore"}, nil)

	require.NoError(t, r.RecordMigrationFailure(deps.MigrationResult{ID: deps.Postgres, Error: "restore failed"}))
	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, "postgres:17.5", st.Dependencies[deps.Postgres].Image)
	assert.Nil(t, st.DependencyMigration)
	assert.Equal(t, "restore failed", st.LastDependencyMigration.Error)
}

func TestPersistedStateWithoutDependencyKeysStillParses(t *testing.T) {
	st := decodeState(t, `{"status":"STOPPED","manualStoppedApps":["edi"]}`)
	assert.Nil(t, st.Dependencies)
	assert.Nil(t, st.DependencyMigration)
}

// errPersistFailed is the sentinel a failingPersister answers with, so a test
// can assert that a failed write reaches the migration engine's caller.
var errPersistFailed = errors.New("save namespace state failed")

// failingPersister is an NsStatePersister whose every write fails. It exists
// because fakePersister always succeeds, which cannot tell "returns the persist
// error" apart from "returns nil".
type failingPersister struct{}

func (failingPersister) SaveNamespaceState(_, _ string) error { return errPersistFailed }

// A migration write that did not reach disk must be reported: the engine rolls
// back on it, and a swallowed error would leave a journal-less migration in
// flight (or a pin the next boot never sees) with the UI reporting success.
func TestMigrationWritesReportAFailedPersist(t *testing.T) {
	newRuntime := func() *Runtime {
		t.Helper()
		r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
		r.SetStatePersister(failingPersister{})
		return r
	}

	t.Run("SetMigrationJournal", func(t *testing.T) {
		err := newRuntime().SetMigrationJournal(&deps.MigrationJournal{ID: deps.Postgres, Step: "pull-image"})
		require.ErrorIs(t, err, errPersistFailed)
	})
	t.Run("CommitMigration", func(t *testing.T) {
		err := newRuntime().CommitMigration(deps.Postgres, "postgres:18",
			deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"})
		require.ErrorIs(t, err, errPersistFailed)
	})
	t.Run("RecordMigrationFailure", func(t *testing.T) {
		err := newRuntime().RecordMigrationFailure(deps.MigrationResult{ID: deps.Postgres, Error: "restore failed"})
		require.ErrorIs(t, err, errPersistFailed)
	})
}

func TestRunningDependencyUpdatesPin(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	r.InjectAppsForTest(
		&AppRuntime{Name: "postgres", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.11"}},
		&AppRuntime{Name: "rabbitmq", Status: AppStatusStarting, Def: appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:4.2.9-management"}},
		&AppRuntime{Name: "gateway", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "gateway", Image: "gw:1"}},
	)

	r.mu.Lock()
	r.syncDependencyPinsUnderLock()
	r.mu.Unlock()

	pins := r.DependencyPins()
	assert.Equal(t, "postgres:17.11", pins[deps.Postgres], "RUNNING dependency re-pins to what actually runs")
	_, hasRabbit := pins[deps.RabbitMQ]
	assert.False(t, hasRabbit, "a STARTING dependency must not be pinned yet")
	// gateway is RUNNING with an image, and is not a registered dependency:
	// only the pin the dependency registry knows about may appear.
	assert.Len(t, pins, 1, "an app outside the dependency registry is never pinned")
	assert.True(t, r.dirty.Load(), "a pin change marks the state dirty for the loop-tail persist")
	assert.Equal(t, 0, fp.callCount(), "the hook never persists itself; the loop tail drains r.dirty")
}

func TestSyncPinsIsIdempotent(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	r.InjectAppsForTest(&AppRuntime{Name: "postgres", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}})
	r.mu.Lock()
	r.syncDependencyPinsUnderLock()
	r.mu.Unlock()
	assert.False(t, r.dirty.Load())
}

// A RUNNING container whose def carries no image tells us nothing about what
// the data runs on. Overwriting the pin with "" would lose the only record of
// the version the volume was created by, and the generator gate reads it.
func TestSyncPinsIgnoresAnEmptyImage(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	r.InjectAppsForTest(&AppRuntime{Name: "postgres", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "postgres"}})

	r.mu.Lock()
	r.syncDependencyPinsUnderLock()
	r.mu.Unlock()

	assert.Equal(t, "postgres:17.5", r.DependencyPins()[deps.Postgres], "an empty image must not clear the pin")
	assert.False(t, r.dirty.Load())
}

// An open migration owns its dependency's pin: mid-flight the target container
// may legitimately be running either version, and CommitMigration is the single
// write that makes the move real. The hook must stand aside until then.
func TestSyncPinsStandsAsideForAnOpenMigration(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.SetStatePersister(&fakePersister{})
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	require.NoError(t, r.SetMigrationJournal(&deps.MigrationJournal{
		ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18", Step: "restore"}))
	r.InjectAppsForTest(&AppRuntime{Name: "postgres", Status: AppStatusRunning,
		Def: appdef.ApplicationDef{Name: "postgres", Image: "postgres:18"}})

	r.mu.Lock()
	r.syncDependencyPinsUnderLock()
	r.mu.Unlock()

	assert.Equal(t, "postgres:17.5", r.DependencyPins()[deps.Postgres],
		"a pin under an open migration is the migration's to move")
	assert.False(t, r.dirty.Load())
}

// TestLoopTailRePinsARunningDependency pins the CALL SITE, not the hook: it
// drives the real runtimeLoop (mock Docker, same harness as
// TestStartAfterStopReinitializesShutdownChan) and asserts both halves of the
// contract — the pin follows the container, and the loop tail's dirty-flag
// persist writes it. Deleting the syncDependencyPinsUnderLock call from
// runtimeLoop fails this test and nothing else.
func TestLoopTailRePinsARunningDependency(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("postgres", "postgres:17.11")}, false)
	require.True(t, waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second),
		"postgres did not reach RUNNING")

	require.True(t, waitUntil(5*time.Second, func() bool {
		return r.DependencyPins()[deps.Postgres] == "postgres:17.11"
	}), "the loop tail never re-pinned postgres to the image it actually runs — "+
		"is syncDependencyPinsUnderLock still called in runtimeLoop?")

	require.True(t, waitUntil(5*time.Second, func() bool {
		var st NsPersistedState
		return json.Unmarshal([]byte(fp.lastJSON()), &st) == nil &&
			st.Dependencies[deps.Postgres].Image == "postgres:17.11"
	}), "the tail's dirty-flag persist never wrote the new pin to the state record")
}
