package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/output"
)

// okRollbackPreflight is what the daemon answers for a rollback that may go
// ahead: nothing measured (a rollback creates nothing, so SpaceChecked stays
// false and every size is a zero that means "not measured"), and the three
// consequence sentences the confirm screen exists for, as WARNINGS — built by
// migrate.warnRollbackConsequences, in English, exactly like every other
// preflight sentence.
func okRollbackPreflight() *migrate.PreflightResult {
	pre := migrate.NewPreflightResult("postgres:18.6", "postgres:17.5")
	pre.OK = true
	pre.WasRunning = true
	pre.Warnings = append(pre.Warnings,
		"the namespace will run postgres:17.5 again, on the data in volume citeck_postgres2_default "+
			"as it was when the migration to postgres:18.6 finished",
		"everything written since then is in volume citeck_postgres3_default: the launcher keeps it "+
			"and will not read it again, so that data becomes unreachable",
		"there is no roll-forward: to go back to postgres:18.6 you would migrate again, from the "+
			"postgres:17.5 data, into a new volume")
	return &pre
}

// rollbackListDto is the dependency list a namespace that has migrated shows:
// one row carrying the offer the command reads its date out of.
func rollbackListDto(migratedAt int64) *api.DependenciesDto {
	return &api.DependenciesDto{Items: []api.DependencyDto{{
		ID: "postgres", CurrentImage: "postgres:18.6", CurrentVersion: "18.6",
		Status: api.DependencyUpToDate,
		Rollback: &api.DependencyRollbackDto{
			ToImage: "postgres:17.5", ToVersion: "17.5",
			Volume: "citeck_postgres2_default", FrozenVolume: "citeck_postgres3_default",
			MigratedAt: migratedAt, Available: true,
		},
	}}}
}

// A rollback measures nothing — it creates nothing — so every size on its
// preflight is a zero meaning "not measured". Printed verbatim they claim the
// namespace holds no data and the disk is full, above three warnings about
// data the operator is about to make unreachable.
func TestRollbackPreflightLines_MeasureNothingAndSayNothingAboutSpace(t *testing.T) {
	depsTestSetup(t)
	pre := okRollbackPreflight()
	lines := strings.Join(rollbackPreflightLines(pre, nil), "\n")

	assert.NotContains(t, lines, "0 B")
	assert.NotContains(t, lines, tHelper("deps.preflight.data", "size", "0 B"))
	assert.NotContains(t, lines, tHelper("deps.preflight.host", "need", "0 B", "free", "0 B"))
	assert.NotContains(t, lines, tHelper("deps.preflight.volume", "need", "0 B", "free", "0 B"))
	// It is a rollback, not a migration: the title must not say the launcher
	// is about to migrate 18.6 → 17.5, which it will never do.
	assert.Contains(t, lines, tHelper("deps.rollback.title", "from", "postgres:18.6", "to", "postgres:17.5"))
	assert.NotContains(t, lines, tHelper("deps.preflight.title", "from", "postgres:18.6", "to", "postgres:17.5"))
	// The three consequences reach the operator VERBATIM, from the daemon.
	assert.Contains(t, lines, "everything written since then is in volume citeck_postgres3_default")
	assert.Contains(t, lines, "there is no roll-forward")
	assert.Contains(t, lines, tHelper("deps.preflight.willStop"))
}

// The date is the ONE fact the preflight deliberately leaves out — it has no
// result record to read it from and says so — and it is the fact that turns
// "the data as it was when the migration finished" into something an operator
// can weigh. It comes from the offer, and a namespace whose result slot has
// moved on carries none, which must render as nothing rather than as 1970.
func TestRollbackPreflightLines_TheMigrationDateComesFromTheOffer(t *testing.T) {
	depsTestSetup(t)
	at := time.Date(2026, 9, 9, 12, 41, 0, 0, time.Local).UnixMilli()
	offer := rollbackListDto(at).Items[0].Rollback

	dated := strings.Join(rollbackPreflightLines(okRollbackPreflight(), offer), "\n")
	assert.Contains(t, dated, tHelper("deps.rollback.migratedAt", "time", formatEpochMillis(at)))

	offer.MigratedAt = 0
	undated := strings.Join(rollbackPreflightLines(okRollbackPreflight(), offer), "\n")
	assert.NotContains(t, undated, tHelper("deps.rollback.migratedAt", "time", ""))
	assert.NotContains(t, undated, "1970")
	// …and no offer at all (the preflight is about to refuse) is not a crash.
	assert.NotEmpty(t, rollbackPreflightLines(okRollbackPreflight(), nil))
}

// --yes means "do not ask", not "cancel": promptConfirm returns its default
// under the global flag, so the rollback prompt's default must be yes — the
// same rule confirmMigration follows, and the same trap.
func TestConfirmRollback_YesFlagAccepts(t *testing.T) {
	depsTestSetup(t)
	prev := flagYes
	flagYes = true
	defer func() { flagYes = prev }()

	assert.True(t, confirmRollback("postgres", okRollbackPreflight()))
}

// A pending rollback (an interrupted migration whose journal is still open)
// freezes the pin and the daemon refuses everything; asking for the preflight
// first would read a data volume to say so.
func TestDepsRollback_PendingRollbackIsRefusedBeforeThePreflight(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{
		list:        &api.DependenciesDto{RollbackPending: "a rollback is pending"},
		rollbackPre: okRollbackPreflight(),
	}
	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "a rollback is pending")
	assert.Equal(t, depsOutcomeRefused, rep.Outcome)
	assert.Zero(t, f.rollbackPreCalls, "the preflight reads a data volume — it must not run")
	assert.Zero(t, f.rollbackCalls)
}

func TestDepsRollback_AFailedPreflightStartsNothing(t *testing.T) {
	depsTestSetup(t)
	bad := migrate.NewPreflightResult("postgres:18.6", "postgres:17.5")
	bad.Problems = append(bad.Problems,
		"volume citeck_postgres2_default is gone, so there is no postgres data from postgres:17.5 to go back to")
	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: &bad}

	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(false, false, true))
	require.Error(t, err)
	assert.Equal(t, depsOutcomeRefused, rep.Outcome)
	assert.Zero(t, f.rollbackCalls)
	require.NotNil(t, rep.Preflight)
	assert.Contains(t, rep.Preflight.Problems[0], "citeck_postgres2_default")
}

func TestDepsRollback_DecliningStartsNothing(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}

	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(false, false, false))
	require.NoError(t, err, "declining is not a failure")
	assert.Equal(t, depsOutcomeCanceled, rep.Outcome)
	assert.Zero(t, f.rollbackCalls)
	assert.Zero(t, f.streamCalls, "nothing to follow")
}

// --detach returns as soon as the daemon accepts: the rollback runs there, and
// interrupting this command stops the output, not the work.
func TestDepsRollback_DetachReportsStarted(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}

	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(true, false, true))
	require.NoError(t, err)
	assert.Equal(t, depsOutcomeStarted, rep.Outcome)
	assert.Equal(t, "postgres:18.6", rep.From)
	assert.Equal(t, "postgres:17.5", rep.To)
	assert.Equal(t, 1, f.rollbackCalls)
	assert.Zero(t, f.migrateCalls, "a rollback is not a migration")
	assert.Zero(t, f.streamCalls, "--detach does not follow")
}

// The rollback rides the MIGRATION's progress channel with three steps, so it
// needs no rendering path of its own — which is only true if the follow loop
// really does end on the shared terminal event.
func TestDepsRollback_FollowsTheSharedProgressChannel(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationStart, AppName: "postgres", After: "postgres:18.6 → postgres:17.5"},
		api.EventDto{Type: api.EventDepsMigrationProgress, AppName: "postgres", Phase: "switch-generation", Current: 2, Total: 3},
		api.EventDto{Type: api.EventDepsMigrationComplete, AppName: "postgres", After: "postgres rolled back to postgres:17.5"},
	)

	var err error
	var rep *depsActionReport
	out := captureStdout(t, func() { rep, err = depsRollbackSteps(f, "postgres", testActionOpts(false, false, true)) })
	require.NoError(t, err)
	assert.Equal(t, depsOutcomeRolledBack, rep.Outcome)
	assert.Contains(t, out, tHelper("deps.step.switch-generation"))
	assert.Contains(t, out, "[2/3]")
}

func TestDepsRollback_TheErrorEventIsTheVerdict(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationError, AppName: "postgres", After: "stop namespace: timed out"},
	)
	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "timed out")
	assert.Equal(t, depsOutcomeFailed, rep.Outcome)
}

// A rollback and a migration share the ONE result slot a namespace has, so the
// poll that catches a dropped terminal event has to check WHICH of the two it
// is looking at: a migration's verdict would otherwise end a rollback's follow
// (and vice versa) and report the wrong direction as this command's answer.
func TestDepsRollback_ThePollDiscriminatesOnTheResultKind(t *testing.T) {
	depsTestSetup(t)
	migrated := &api.DependencyMigrationResultDto{
		ID: "postgres", From: "postgres:17.5", To: "postgres:18.6", Success: true,
		FinishedAt: time.Now().Add(time.Minute).UnixMilli(), Kind: deps.ResultKindMigration,
	}
	rolled := &api.DependencyMigrationResultDto{
		ID: "postgres", From: "postgres:18.6", To: "postgres:17.5", Success: true,
		FinishedAt: time.Now().Add(time.Minute).UnixMilli(), Kind: deps.ResultKindRollback,
	}

	// A rollback's follow ignores a migration verdict…
	done, verdict := pollActionVerdict(
		func() (*api.DependenciesDto, error) { return &api.DependenciesDto{LastResult: migrated}, nil },
		"postgres", time.Now(), deps.ResultKindRollback)
	assert.False(t, done, "a migration's verdict is not this rollback's")
	require.NoError(t, verdict)

	// …and a migration's follow ignores a rollback verdict.
	done, verdict = pollActionVerdict(
		func() (*api.DependenciesDto, error) { return &api.DependenciesDto{LastResult: rolled}, nil },
		"postgres", time.Now(), deps.ResultKindMigration)
	assert.False(t, done, "a rollback's verdict is not this migration's")
	require.NoError(t, verdict)

	// Each one still ends on its own.
	done, verdict = pollActionVerdict(
		func() (*api.DependenciesDto, error) { return &api.DependenciesDto{LastResult: rolled}, nil },
		"postgres", time.Now(), deps.ResultKindRollback)
	assert.True(t, done)
	require.NoError(t, verdict)
}

// The whole point of the poll: the daemon DROPS an event rather than block a
// full subscriber, and a rollback's own namespace stop/start burst is exactly
// what fills the channel.
func TestDepsRollback_PollCatchesADroppedTerminalEvent(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{rollbackPre: okRollbackPreflight()}
	f.events = make(chan api.EventDto) // open and silent: every event was dropped
	f.onList = func(call int) *api.DependenciesDto {
		if call == 1 {
			return rollbackListDto(0)
		}
		return &api.DependenciesDto{LastResult: &api.DependencyMigrationResultDto{
			ID: "postgres", From: "postgres:18.6", To: "postgres:17.5", Success: true,
			OldVolume: "citeck_postgres3_default", Kind: deps.ResultKindRollback,
			FinishedAt: time.Now().Add(time.Minute).UnixMilli(),
		}}
	}

	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(false, false, true))
	require.NoError(t, err)
	assert.Equal(t, depsOutcomeRolledBack, rep.Outcome)
	assert.Greater(t, f.listCalls, 1, "the verdict came from the poll, not from an event")
}

// A rollback this command lost sight of is NOT a failed one: the daemon runs
// it on its own context and finishes it whether or not anyone is listening.
func TestDepsRollback_ALostStreamIsUnknownNotFailed(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}
	f.events = make(chan api.EventDto)
	close(f.events)

	rep, err := depsRollbackSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "citeck deps")
	assert.Equal(t, depsOutcomeUnknown, rep.Outcome)
}

// AGENTS.md promises --format json on any command, and for a command whose
// whole point is the verdict that means ONE object and nothing else: no
// preflight prose, no accept message, no per-step progress.
func TestDepsRollback_JSONPrintsOnlyTheVerdict(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationProgress, AppName: "postgres", Phase: "switch-generation", Current: 2, Total: 3},
		api.EventDto{Type: api.EventDepsMigrationComplete, AppName: "postgres", After: "postgres rolled back to postgres:17.5"},
	)

	var err error
	out := captureStdout(t, func() { err = depsRollback(f, "postgres", testActionOpts(false, false, true)) })
	require.NoError(t, err)

	var rep depsActionReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep), "the whole of stdout must be one JSON object:\n%s", out)
	assert.Equal(t, depsOutcomeRolledBack, rep.Outcome)
	assert.Equal(t, "postgres", rep.ID)
	assert.Equal(t, "postgres:18.6", rep.From)
	assert.Equal(t, "postgres:17.5", rep.To)
	assert.NotContains(t, out, tHelper("deps.rollback.title", "from", "postgres:18.6", "to", "postgres:17.5"))
	assert.NotContains(t, out, tHelper("deps.step.switch-generation"), "no per-step progress prose")
}

// A failure has to be IN the object: Execute() prints no "Error:" line in JSON
// mode, so a report without the reason leaves a script with an exit code and
// nothing else.
func TestDepsRollback_JSONCarriesTheFailure(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	f := &fakeDepsDaemon{list: rollbackListDto(0), rollbackPre: okRollbackPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationError, AppName: "postgres", After: "stop namespace: timed out"},
	)

	var err error
	out := captureStdout(t, func() { err = depsRollback(f, "postgres", testActionOpts(false, false, true)) })
	require.Error(t, err, "a failed rollback must still exit non-zero")

	var rep depsActionReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep), out)
	assert.Equal(t, depsOutcomeFailed, rep.Outcome)
	assert.Contains(t, rep.Error, "timed out")
	assert.Equal(t, err.Error(), rep.Error, "the object and the exit code must tell the same story")
}

func TestDepsRollbackCommandWiring(t *testing.T) {
	depsTestSetup(t)
	rollback := findSubCommand(newDepsCmd(), "rollback")
	require.NotNil(t, rollback, "`citeck deps rollback` must exist")
	require.NotNil(t, rollback.Flags().Lookup("detach"), "--detach")
	assert.Equal(t, "d", rollback.Flags().Lookup("detach").Shorthand)
	// A rollback creates no volume, so there is nothing to replace: the flag
	// would promise a decision this command never makes.
	assert.Nil(t, rollback.Flags().Lookup("replace-existing"))
	require.Error(t, rollback.Args(rollback, []string{}), "rollback takes exactly one dependency id")
	require.Error(t, rollback.Args(rollback, []string{"postgres", "rabbitmq"}))
	require.NoError(t, rollback.Args(rollback, []string{"postgres"}))
}
