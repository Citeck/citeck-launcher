package namespace

import (
	"encoding/json"
	"errors"
	"sync"
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

	r.SetDependencyState(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})
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

	// The two POINTER accessors hand out clones: the daemon reads a journal or a
	// verdict, and writing through it would move runtime state nobody locked.
	// Mutate what they returned, then read the runtime again.
	r.MigrationJournal().Step = "hacked"
	r.MigrationJournal().CreatedVolume = "hacked"
	r.LastDependencyMigration().Error = "hacked"
	assert.Equal(t, "dump", r.MigrationJournal().Step)
	assert.Empty(t, r.MigrationJournal().CreatedVolume)
	assert.Equal(t, "boom", r.LastDependencyMigration().Error)

	// DependencyPins cannot be checked the same way, and does not need to be:
	// the field is map[deps.ID]deps.DependencyState while the accessor returns
	// map[deps.ID]string, so an aliasing implementation does not compile. The
	// assertion below documents the contract; it is the type that enforces it.
	pins := r.DependencyPins()
	pins[deps.Postgres] = "hacked"
	assert.Equal(t, "postgres:17.5", r.DependencyPins()[deps.Postgres])
}

// A pin is per dependency, and the map is the state file's dependency record:
// re-pinning replaces a value rather than adding one, and a second dependency
// leaves the first alone. Both mistakes would mix two dependencies' versions in
// one state record, which the generator gate then reads back.
func TestSetDependencyStateOverwritesAndCoexists(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	r.SetDependencyState(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})
	r.SetDependencyState(deps.RabbitMQ, deps.DependencyState{Image: "rabbitmq:4.1.2-management"})
	r.SetDependencyState(deps.Postgres, deps.DependencyState{Image: "postgres:18"})

	assert.Equal(t, map[deps.ID]string{
		deps.Postgres: "postgres:18",
		deps.RabbitMQ: "rabbitmq:4.1.2-management",
	}, r.DependencyPins())
	require.Equal(t, 3, fp.callCount(), "each pin write is durable on its own")

	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, "postgres:18", st.Dependencies[deps.Postgres].Image)
	assert.Equal(t, "rabbitmq:4.1.2-management", st.Dependencies[deps.RabbitMQ].Image)
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
	require.NoError(t, r.CommitMigration(deps.Postgres, deps.DependencyState{Image: "postgres:18", VolumeGen: 2}, res))

	require.Equal(t, 1, fp.callCount(), "commit must be exactly one persist")
	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, deps.DependencyState{Image: "postgres:18", VolumeGen: 2,
		PrevImage: "postgres:17.5", PrevVolumeGen: 1}, st.Dependencies[deps.Postgres],
		"the image and the generation move together, in the same write — the generation is "+
			"what names the volume the migrated data is actually in — and the state they "+
			"moved FROM is recorded in that same write, because it is what a rollback restores")
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

// A commit whose write failed must leave NOTHING moved in memory either: the
// engine treats a failed CommitMigration as a migration failure and rolls back,
// removing the volume it created — so a runtime that already believed the pin
// had moved would generate the new version's layout onto a volume that no
// longer exists, an empty cluster beside the intact old data.
func TestCommitMigrationDoesNotMoveMemoryWhenThePersistFails(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	journal := &deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		Step: "restore", CreatedVolume: "postgres3"}
	last := &deps.MigrationResult{ID: deps.Postgres, Error: "an older attempt"}
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, journal, last)
	r.SetStatePersister(failingPersister{})

	err := r.CommitMigration(deps.Postgres, deps.DependencyState{Image: "postgres:18", VolumeGen: 2},
		deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18", OldVolume: "postgres2"})
	require.ErrorIs(t, err, errPersistFailed)

	assert.Equal(t, map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		r.DependencyStates(), "no part of the pin may move on a commit that was never written — "+
			"including the rollback target, which would otherwise offer a volume nothing migrated to")
	j := r.MigrationJournal()
	require.NotNil(t, j, "the journal must survive so the rollback still has its record")
	assert.Equal(t, "restore", j.Step)
	assert.Equal(t, "postgres3", j.CreatedVolume)
	lm := r.LastDependencyMigration()
	require.NotNil(t, lm)
	assert.Equal(t, "an older attempt", lm.Error, "the verdict must not be published either")
}

// The same, for a dependency that had no pin yet: restoring must remove the
// entry, not leave the new image behind under a "previous" value that never
// existed.
func TestCommitMigrationRestoresAnAbsentPinWhenThePersistFails(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.RestoreDependencyState(nil, &deps.MigrationJournal{ID: deps.Postgres, Step: "restore"}, nil)
	r.SetStatePersister(failingPersister{})

	require.ErrorIs(t, r.CommitMigration(deps.Postgres, deps.DependencyState{Image: "postgres:18", VolumeGen: 2},
		deps.MigrationResult{ID: deps.Postgres}), errPersistFailed)

	assert.Empty(t, r.DependencyPins())
	assert.Nil(t, r.LastDependencyMigration())
	require.NotNil(t, r.MigrationJournal())
}

// A rollback that failed must NOT close the migration: the leftovers it could
// not remove are still on the host, and the journal is the only record of them.
func TestRecordRollbackFailureKeepsTheJournal(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, Step: "create-volume", CreatedVolume: "postgres3"}, nil)

	require.NoError(t, r.RecordRollbackFailure(deps.MigrationResult{ID: deps.Postgres,
		Error: "restore failed; rollback failed: rm volume: busy"}))

	require.Equal(t, 1, fp.callCount(), "one write, like every other migration mutation")
	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, "postgres:17.5", st.Dependencies[deps.Postgres].Image, "the pin never moves on failure")
	require.NotNil(t, st.DependencyMigration, "the journal survives a failed rollback")
	assert.Equal(t, "postgres3", st.DependencyMigration.CreatedVolume)
	require.NotNil(t, st.LastDependencyMigration)
	assert.Contains(t, st.LastDependencyMigration.Error, "rollback failed")

	// And it is still readable in memory, so the next start's recovery sees it.
	j := r.MigrationJournal()
	require.NotNil(t, j)
	assert.Equal(t, "create-volume", j.Step)
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
	// No t.Helper() here: this is a closure, not a helper called with the
	// subtest's *testing.T, so marking it would report nothing — and it makes
	// no assertions to attribute in the first place.
	newRuntime := func() *Runtime {
		r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
		r.SetStatePersister(failingPersister{})
		return r
	}

	// Reporting the error is only half of it: the record did not reach disk,
	// so the runtime still owes the write and must say so — clearing r.dirty
	// here would additionally drop whatever unrelated change was pending.
	t.Run("SetMigrationJournal", func(t *testing.T) {
		r := newRuntime()
		err := r.SetMigrationJournal(&deps.MigrationJournal{ID: deps.Postgres, Step: "pull-image"})
		require.ErrorIs(t, err, errPersistFailed)
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})
	t.Run("CommitMigration", func(t *testing.T) {
		r := newRuntime()
		err := r.CommitMigration(deps.Postgres, deps.DependencyState{Image: "postgres:18", VolumeGen: 2},
			deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"})
		require.ErrorIs(t, err, errPersistFailed)
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})
	t.Run("RecordMigrationFailure", func(t *testing.T) {
		r := newRuntime()
		err := r.RecordMigrationFailure(deps.MigrationResult{ID: deps.Postgres, Error: "restore failed"})
		require.ErrorIs(t, err, errPersistFailed)
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})
	t.Run("RecordRollbackFailure", func(t *testing.T) {
		r := newRuntime()
		err := r.RecordRollbackFailure(deps.MigrationResult{ID: deps.Postgres, Error: "rollback failed"})
		require.ErrorIs(t, err, errPersistFailed)
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})
}

// TestDependencyStatesIsTheWholeRecord: the image-only DependencyPins view is
// what the edit gate and the CLI's item list read, but the generator needs the
// generation too, and the two views must describe the same map.
func TestDependencyStatesIsTheWholeRecord(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 2},
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
	}, nil, nil)

	assert.Equal(t, map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 2},
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
	}, r.DependencyStates())
	assert.Equal(t, map[deps.ID]string{
		deps.Postgres: "postgres:18",
		deps.RabbitMQ: "rabbitmq:4.1.2-management",
	}, r.DependencyPins())

	// The accessor hands out a copy: the daemon reads these on every namespace
	// fetch, and writing through the result would move runtime state under no
	// lock at all.
	states := r.DependencyStates()
	states[deps.Postgres] = deps.DependencyState{Image: "hacked", VolumeGen: 9}
	assert.Equal(t, deps.DependencyState{Image: "postgres:18", VolumeGen: 2},
		r.DependencyStates()[deps.Postgres])
}

// Every state file that exists today was written before the counter did, so
// none of them carries volumeGen. Reading one must answer generation 1 — the
// volume those namespaces are actually running on. A fixture rather than a
// comment, because the rule lives in a JSON tag and a zero value.
func TestAStateFileWrittenBeforeTheCounterLoadsAsGenerationOne(t *testing.T) {
	st := decodeState(t, `{"status":"STOPPED","dependencies":{"postgres":{"image":"postgres:17.5"}}}`)
	require.Contains(t, st.Dependencies, deps.Postgres)
	assert.Equal(t, 0, st.Dependencies[deps.Postgres].VolumeGen, "nothing was written, so nothing is read")
	assert.Equal(t, 1, st.Dependencies[deps.Postgres].Gen(), "and an absent counter IS generation 1")

	// And it survives the round trip through the runtime, which is what the
	// generator reads.
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.RestoreDependencyState(st.Dependencies, nil, nil)
	assert.Equal(t, 1, r.DependencyStates()[deps.Postgres].Gen())
}

// TestRunningRepinKeepsTheVolumeGeneration is the regression this whole record
// exists to prevent. The RUNNING hook re-pins a dependency to the image its
// container actually runs — which after a migration is the NEW image, on the
// NEW volume. Writing a fresh DependencyState with only the image set would
// reset the counter to 1 there, and the next start would mount the
// PRE-migration volume: the old data served by the new version, with the
// migrated copy orphaned beside it and nothing in the log to say so.
func TestRunningRepinKeepsTheVolumeGeneration(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 2,
			PrevImage: "postgres:17.5", PrevVolumeGen: 1}}, nil, nil)
	// 18.1 over 18: a non-breaking patch bump, which is exactly the case the
	// hook exists for — it is the ordinary way a pin moves after a migration.
	r.InjectAppsForTest(&AppRuntime{Name: "postgres", Status: AppStatusRunning,
		Def: appdef.ApplicationDef{Name: "postgres", Image: "postgres:18.1"}})

	r.mu.Lock()
	r.syncDependencyPinsUnderLock()
	r.mu.Unlock()

	assert.Equal(t, deps.DependencyState{Image: "postgres:18.1", VolumeGen: 2,
		PrevImage: "postgres:17.5", PrevVolumeGen: 1},
		r.DependencyStates()[deps.Postgres],
		"the image follows the container; the generation is the migration's and only a migration "+
			"moves it — and so is the rollback target, which the retained volume on disk still backs. "+
			"A patch bump of the NEW version says nothing about the OLD one, and dropping the target "+
			"here would retire the rollback offer on the first ordinary re-pin after a migration")
	assert.True(t, r.dirty.Load(), "a pin change marks the state dirty for the loop-tail persist")
}

func TestRunningDependencyUpdatesPin(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	r.InjectAppsForTest(
		&AppRuntime{Name: "postgres", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.11"}},
		&AppRuntime{Name: "zookeeper", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "zookeeper", Image: "zookeeper:3.9.5"}},
		&AppRuntime{Name: "rabbitmq", Status: AppStatusStarting, Def: appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:4.2.9-management"}},
		&AppRuntime{Name: "gateway", Status: AppStatusRunning, Def: appdef.ApplicationDef{Name: "gateway", Image: "gw:1"}},
	)

	r.mu.Lock()
	r.syncDependencyPinsUnderLock()
	r.mu.Unlock()

	// The whole map, not its size: postgres re-pinned to what actually runs,
	// zookeeper pinned beside it (two dependencies coexist), rabbitmq absent
	// because STARTING is no proof the data accepted that version, and gateway
	// absent because an app outside the dependency registry is never pinned —
	// which a count would state only by arithmetic.
	assert.Equal(t, map[deps.ID]string{
		deps.Postgres:  "postgres:17.11",
		deps.Zookeeper: "zookeeper:3.9.5",
	}, r.DependencyPins())
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
	assert.Equal(t, "postgres:17.5", r.DependencyPins()[deps.Postgres],
		"a pin that already matches the running container must be left exactly as it is")
	assert.False(t, r.dirty.Load(), "and nothing may be marked for persisting")
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

// A pin the store refused exists in memory and nowhere else — and after
// SetDependencyState the in-memory pin already equals the running container's
// image, so syncDependencyPinsUnderLock (pin writer #1) finds nothing to
// re-flag. Unless the failed write stays OWED, it is dropped until the image
// itself changes, and the restart in between hands 17 data to 18.
func TestAPinWriteThatFailedIsStillOwed(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.SetStatePersister(failingPersister{})

	r.SetDependencyState(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})

	assert.Equal(t, "postgres:17.5", r.DependencyPins()[deps.Postgres],
		"the pin is held in memory whatever the store did")
	assert.True(t, r.dirty.Load(),
		"a pin write that never reached disk must leave the runtime owing it")
}

// armedFailPersister fails the next n writes and succeeds afterwards, which is
// the one sequence the retry is about: a write that does not reach disk,
// followed by a store that works again. The budget is ARMED rather than
// switched off so a test can say exactly how many attempts must fail — the
// loop tail retries on its own schedule, so a toggle would race it.
type armedFailPersister struct {
	mu     sync.Mutex
	armed  int
	json   string
	calls  int
	failed int
}

func (p *armedFailPersister) SaveNamespaceState(_, stateJSON string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.armed > 0 {
		p.armed--
		p.failed++
		return errPersistFailed
	}
	p.json = stateJSON
	return nil
}

func (p *armedFailPersister) arm(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.armed = n
}

func (p *armedFailPersister) stats() (calls, armed, failed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.armed, p.failed
}

func (p *armedFailPersister) lastJSON() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.json
}

// The call-site half of the contract above: the real runtimeLoop's dirty-flag
// tail is what makes "still owed" mean something, so this drives it. The app is
// deliberately NOT a dependency — the pin under test can then only come from
// SetDependencyState, never from syncDependencyPinsUnderLock.
func TestTheLoopTailRetriesAPinWriteThatDidNotReachDisk(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	p := &armedFailPersister{}
	r.SetStatePersister(p)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("gateway", "gw:1")}, false)
	require.True(t, waitForAppStatus(r, "gateway", AppStatusRunning, 10*time.Second),
		"gateway did not reach RUNNING")

	// Settle first, so the pin write below is the only thing left to persist:
	// an idle loop cannot re-mark r.dirty on its own, which is what makes the
	// final wait a test of the retry and not of some unrelated transition.
	require.True(t, waitUntil(5*time.Second, func() bool { return !r.dirty.Load() }),
		"the loop never went idle")
	before, _, _ := p.stats()
	time.Sleep(200 * time.Millisecond)
	idle, _, _ := p.stats()
	require.Equal(t, before, idle, "the loop must be idle before the pin write")

	p.arm(1)
	r.SetDependencyState(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})
	_, armed, failed := p.stats()
	require.Equal(t, 0, armed, "the pin write must have been attempted")
	require.Equal(t, 1, failed)

	require.True(t, waitUntil(5*time.Second, func() bool {
		var st NsPersistedState
		return json.Unmarshal([]byte(p.lastJSON()), &st) == nil &&
			st.Dependencies[deps.Postgres].Image == "postgres:17.5"
	}), "the loop tail never retried the pin write the store refused")
}

// A rollback is the mirror of CommitMigration and needs the same atomicity: the
// pin goes back to the state the migration moved away from, the verdict is
// recorded, and the OFFER IS WITHDRAWN — all in one write. Clearing the target
// is not tidiness: there is no roll-forward action, so an offer left standing
// would point at a volume the namespace has stopped writing to, and taking it
// would silently discard everything written since the rollback.
func TestRollbackIsOneWriteAndClearsTheTarget(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {
		Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:17.5", PrevVolumeGen: 1}},
		&deps.MigrationJournal{ID: deps.Postgres, Step: "switch-generation"}, nil)

	prev, ok := r.DependencyStates()[deps.Postgres].Previous()
	require.True(t, ok)
	// The offer is withdrawn by the METHOD, not by the caller: prev is handed
	// over carrying a stale target of its own (what a caller that rebuilt the
	// state by hand, or a deeper chain, would pass), and it must not come back
	// out. Nothing above the persist may decide whether an offer survives — an
	// offer the launcher cannot keep is worse than none.
	prev.PrevImage, prev.PrevVolumeGen = "postgres:16", 1
	res := deps.MigrationResult{ID: deps.Postgres, From: "postgres:18.6", To: "postgres:17.5",
		Kind: deps.ResultKindRollback, FinishedAt: time.Now()}
	require.NoError(t, r.RollbackDependencyState(deps.Postgres, prev, res))

	assert.Equal(t, deps.DependencyState{Image: "postgres:17.5", VolumeGen: 1},
		r.DependencyStates()[deps.Postgres], "in memory as well as on disk")

	require.Equal(t, 1, fp.callCount(), "a rollback must be exactly one persist")
	st := decodeState(t, fp.lastJSON())
	assert.Equal(t, deps.DependencyState{Image: "postgres:17.5", VolumeGen: 1},
		st.Dependencies[deps.Postgres],
		"the pin goes back whole, and carries no further offer")
	assert.Nil(t, st.DependencyMigration)
	require.NotNil(t, st.LastDependencyMigration)
	assert.Equal(t, deps.ResultKindRollback, st.LastDependencyMigration.Kind,
		"the two share one result slot, so the verdict has to say which it was")
}

// The same argument as TestCommitMigrationDoesNotMoveMemoryWhenThePersistFails,
// from the other direction: a rollback whose write never landed is a FAILED
// rollback, and a runtime that already believed the pin had gone back would
// generate the old version onto the old volume while the state file — and the
// next daemon — still say the new one. Nothing may move until the write does.
func TestRollbackDoesNotMoveMemoryWhenThePersistFails(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	pin := deps.DependencyState{Image: "postgres:18.6", VolumeGen: 2,
		PrevImage: "postgres:17.5", PrevVolumeGen: 1}
	journal := &deps.MigrationJournal{ID: deps.Postgres, Step: "switch-generation"}
	last := &deps.MigrationResult{ID: deps.Postgres, Error: "an older attempt"}
	r.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: pin}, journal, last)
	r.SetStatePersister(failingPersister{})

	prev, _ := pin.Previous()
	err := r.RollbackDependencyState(deps.Postgres, prev,
		deps.MigrationResult{ID: deps.Postgres, Kind: deps.ResultKindRollback})
	require.ErrorIs(t, err, errPersistFailed)

	assert.Equal(t, map[deps.ID]deps.DependencyState{deps.Postgres: pin}, r.DependencyStates(),
		"the pin, the generation and the offer all stay where they were")
	require.NotNil(t, r.MigrationJournal())
	lm := r.LastDependencyMigration()
	require.NotNil(t, lm)
	assert.Equal(t, "an older attempt", lm.Error, "the verdict must not be published either")
}
