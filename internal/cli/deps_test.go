package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spf13/cobra"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/output"
)

// depsTestSetup pins the locale and disables colors so the assertions below
// compare rendered text, not ANSI escapes.
func depsTestSetup(t *testing.T) {
	t.Helper()
	initI18n("en")
	output.SetColorsEnabled(false)
}

func TestFormatDependencyStatus(t *testing.T) {
	depsTestSetup(t)
	cases := map[string]string{
		api.DependencyUpToDate:               tHelper("deps.status.upToDate"),
		api.DependencyPendingMinor:           tHelper("deps.status.pendingMinor", "version", "17.11"),
		api.DependencyUpgradeAvailable:       tHelper("deps.status.upgradeAvailable", "id", "postgres"),
		api.DependencyRequiresLauncherUpdate: tHelper("deps.status.requiresLauncherUpdate"),
	}
	for status, want := range cases {
		got := formatDependencyStatus(api.DependencyDto{ID: "postgres", Status: status, TargetVersion: "17.11"})
		assert.Equal(t, want, got, status)
		assert.NotContains(t, got, "deps.status.", "status %q rendered a bare key", status)
	}
	// An unknown status is not a crash and not a lie: it reads as up to date,
	// which is what the daemon means by "nothing held back".
	assert.Equal(t, tHelper("deps.status.upToDate"),
		formatDependencyStatus(api.DependencyDto{ID: "postgres", Status: "something-new"}))
}

func TestFormatDependencyStatus_InterpolatesTheRightField(t *testing.T) {
	depsTestSetup(t)
	// pending-minor names the VERSION it will apply; upgrade-available names
	// the ID the user has to type. Swapping them would still translate.
	assert.Contains(t,
		formatDependencyStatus(api.DependencyDto{ID: "rabbitmq", Status: api.DependencyPendingMinor, TargetVersion: "4.2.9"}),
		"4.2.9")
	up := formatDependencyStatus(api.DependencyDto{ID: "postgres", Status: api.DependencyUpgradeAvailable, TargetVersion: "18"})
	assert.Contains(t, up, "citeck deps upgrade postgres")
}

func TestVersionOr(t *testing.T) {
	// The parsed version is what the user compares; the image reference is the
	// fallback for one the launcher cannot parse, so the cell is never blank.
	assert.Equal(t, "17.5", versionOr("17.5", "postgres:17.5"))
	assert.Equal(t, "postgres:17.5", versionOr("", "postgres:17.5"))
	assert.Empty(t, versionOr("", ""))
}

func TestDependencyHintLine(t *testing.T) {
	depsTestSetup(t)
	assert.Empty(t, dependencyHintLine(nil, nil))
	assert.Empty(t, dependencyHintLine(&api.NamespaceDto{}, &api.DependenciesDto{}))

	ns := &api.NamespaceDto{DependencyUpgrades: []api.DependencyUpgradeDto{
		{ID: "postgres", From: "postgres:17.5", To: "postgres:18", Migratable: true},
		{ID: "rabbitmq", From: "rabbitmq:4.1.2-management", To: "rabbitmq:4.2.9-management"},
	}}
	line := dependencyHintLine(ns, nil)
	assert.Contains(t, line, "postgres")
	assert.Contains(t, line, "rabbitmq")
	assert.Contains(t, line, "citeck deps")
}

func TestDependencyHintLine_PendingRollbackOutranksAnUpgrade(t *testing.T) {
	depsTestSetup(t)
	ns := &api.NamespaceDto{DependencyUpgrades: []api.DependencyUpgradeDto{
		{ID: "postgres", From: "postgres:17.5", To: "postgres:18", Migratable: true},
	}}
	dto := &api.DependenciesDto{
		RollbackPending: "a previous migration of postgres left a rollback pending",
		Migration:       &api.DependencyMigrationDto{ID: "rabbitmq", Step: "dump", StepIndex: 1, StepCount: 10},
	}
	line := dependencyHintLine(ns, dto)
	assert.NotContains(t, line, "rabbitmq", "a pending rollback outranks every other dependency state")
	assert.Contains(t, line, "rollback pending")
	// The upgrade line would say "→"; the rollback is the only thing worth
	// acting on while the pin is frozen, so it must not be appended to it.
	assert.NotContains(t, line, "postgres:17.5 → postgres:18")
}

func TestDependencyHintLine_ReportsARunningMigration(t *testing.T) {
	depsTestSetup(t)
	ns := &api.NamespaceDto{DependencyMigration: &api.DependencyMigrationDto{
		ID: "postgres", Step: "dump", StepIndex: 4, StepCount: 10,
	}}
	line := dependencyHintLine(ns, nil)
	assert.Contains(t, line, "postgres")
	assert.Contains(t, line, "4")
	assert.Contains(t, line, "10")
	assert.Contains(t, line, tHelper("deps.step.dump"))
}

func TestRenderMigrationEvent(t *testing.T) {
	depsTestSetup(t)
	line, terminal, failed := renderMigrationEvent(api.EventDto{
		Type: "deps_migration_progress", Phase: "dump", Current: 4, Total: 10, Percent: 42, After: "dumped 1.2 GiB",
	})
	assert.Contains(t, line, "[4/10]")
	assert.Contains(t, line, tHelper("deps.step.dump"))
	assert.Contains(t, line, "42%")
	assert.Contains(t, line, "dumped 1.2 GiB")
	assert.False(t, terminal)
	assert.False(t, failed)

	// Percent 0 is "indeterminate", not "0%".
	line, _, _ = renderMigrationEvent(api.EventDto{
		Type: "deps_migration_progress", Phase: "create-volume", Current: 6, Total: 10,
	})
	assert.Contains(t, line, "[6/10]")
	assert.NotContains(t, line, "%")

	line, terminal, failed = renderMigrationEvent(api.EventDto{
		Type: "deps_migration_start", Total: 10, After: "postgres:17.5 → postgres:18",
	})
	assert.Contains(t, line, "postgres:17.5 → postgres:18")
	assert.False(t, terminal)
	assert.False(t, failed)

	line, terminal, failed = renderMigrationEvent(api.EventDto{Type: "deps_migration_complete", After: "postgres migrated to postgres:18"})
	assert.Equal(t, "postgres migrated to postgres:18", line)
	assert.True(t, terminal)
	assert.False(t, failed)

	line, terminal, failed = renderMigrationEvent(api.EventDto{Type: "deps_migration_error", After: "boom"})
	assert.Contains(t, line, "boom")
	assert.True(t, terminal)
	assert.True(t, failed)

	line, terminal, failed = renderMigrationEvent(api.EventDto{Type: "app_status"})
	assert.Empty(t, line)
	assert.False(t, terminal)
	assert.False(t, failed)
}

// The event types this command selects on are the daemon's own constants. A
// private copy that drifts by one character is a `citeck deps upgrade` that
// prints nothing and waits for a terminal event that never matches.
func TestMigrationEventTypesAreTheAPIConstants(t *testing.T) {
	depsTestSetup(t)
	assert.True(t, isMigrationEventFor(
		api.EventDto{Type: api.EventDepsMigrationProgress, AppName: "postgres"}, "postgres"))
	for _, tc := range []struct {
		typ      string
		terminal bool
		failed   bool
	}{
		{api.EventDepsMigrationStart, false, false},
		{api.EventDepsMigrationProgress, false, false},
		{api.EventDepsMigrationComplete, true, false},
		{api.EventDepsMigrationError, true, true},
	} {
		_, terminal, failed := renderMigrationEvent(api.EventDto{Type: tc.typ, After: "x"})
		assert.Equal(t, tc.terminal, terminal, tc.typ)
		assert.Equal(t, tc.failed, failed, tc.typ)
	}
}

// The daemon drops an event rather than block a full subscriber channel, and a
// migration's stop/start burst is what fills it. Without the poll a dropped
// terminal event left the command waiting six hours on a finished migration.
func TestPollMigrationVerdict(t *testing.T) {
	depsTestSetup(t)
	clickedAt := time.Now()
	fetchOf := func(dto *api.DependenciesDto, err error) func() (*api.DependenciesDto, error) {
		return func() (*api.DependenciesDto, error) { return dto, err }
	}
	result := func(id string, success bool, finished time.Time) *api.DependencyMigrationResultDto {
		return &api.DependencyMigrationResultDto{
			ID: id, From: "postgres:17", To: "postgres:18",
			Success: success, Error: "boom", FinishedAt: finished.UnixMilli(),
		}
	}

	t.Run("a finished migration ends the wait", func(t *testing.T) {
		done, verdict := pollMigrationVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("postgres", true, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt)
		assert.True(t, done)
		require.NoError(t, verdict)
	})
	t.Run("a failed migration ends it with the reason", func(t *testing.T) {
		done, verdict := pollMigrationVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("postgres", false, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt)
		assert.True(t, done)
		require.ErrorContains(t, verdict, "boom")
	})
	t.Run("a running migration keeps following", func(t *testing.T) {
		done, _ := pollMigrationVerdict(fetchOf(&api.DependenciesDto{
			Migration:  &api.DependencyMigrationDto{ID: "postgres", Step: "dump"},
			LastResult: result("postgres", true, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt)
		assert.False(t, done, "the daemon still reports it running")
	})
	// The result of a PREVIOUS migration of the same dependency must never be
	// reported as this run's verdict.
	t.Run("a verdict older than the click is not ours", func(t *testing.T) {
		done, _ := pollMigrationVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("postgres", true, clickedAt.Add(-time.Hour)),
		}, nil), "postgres", clickedAt)
		assert.False(t, done)
	})
	t.Run("another dependency's verdict is not ours", func(t *testing.T) {
		done, _ := pollMigrationVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("rabbitmq", true, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt)
		assert.False(t, done)
	})
	t.Run("a failed poll keeps following", func(t *testing.T) {
		done, _ := pollMigrationVerdict(fetchOf(nil, errors.New("socket closed")), "postgres", clickedAt)
		assert.False(t, done)
	})
	t.Run("no verdict at all keeps following", func(t *testing.T) {
		done, _ := pollMigrationVerdict(fetchOf(&api.DependenciesDto{}, nil), "postgres", clickedAt)
		assert.False(t, done)
	})
}

func TestIsMigrationEventFor(t *testing.T) {
	assert.True(t, isMigrationEventFor(api.EventDto{Type: "deps_migration_progress", AppName: "postgres"}, "postgres"))
	assert.True(t, isMigrationEventFor(api.EventDto{Type: "deps_migration_error", AppName: "postgres"}, "postgres"))
	// Another dependency's migration must not end this one's wait.
	assert.False(t, isMigrationEventFor(api.EventDto{Type: "deps_migration_complete", AppName: "rabbitmq"}, "postgres"))
	// The namespace is stopped and started around the migration, so its own
	// app events name the same container.
	assert.False(t, isMigrationEventFor(api.EventDto{Type: "app_status", AppName: "postgres"}, "postgres"))
	assert.False(t, isMigrationEventFor(api.EventDto{Type: "pull_progress", AppName: "postgres"}, "postgres"))
}

// The daemon publishes "preparing" with no step count while it builds the
// plan — the `du` of the data volume is minutes on a real cluster, and until
// this the command printed nothing at all for it. "[0/0]" would read as a
// broken counter, so the title carries the line on its own.
func TestRenderMigrationEvent_PreparingHasNoStepCounter(t *testing.T) {
	depsTestSetup(t)
	line, terminal, failed := renderMigrationEvent(api.EventDto{
		Type:  api.EventDepsMigrationProgress,
		Phase: api.DependencyMigrationStepPreparing,
		After: "postgres:17.5 → postgres:18",
	})
	assert.Contains(t, line, tHelper("deps.step."+api.DependencyMigrationStepPreparing))
	assert.NotContains(t, line, "[0/0]")
	assert.Contains(t, line, "postgres:17.5 → postgres:18")
	assert.False(t, terminal)
	assert.False(t, failed)
	// And it is a translated sentence, not the raw id.
	assert.NotEqual(t, api.DependencyMigrationStepPreparing,
		stepTitle(api.DependencyMigrationStepPreparing))
}

func TestStepTitle_FallsBackToTheRawStepID(t *testing.T) {
	depsTestSetup(t)
	assert.Equal(t, tHelper("deps.step.restore"), stepTitle("restore"))
	// A step id a newer daemon knows and this launcher has no key for must
	// read as the id, never as "deps.step.<id>".
	assert.Equal(t, "reindex", stepTitle("reindex"))
	assert.Empty(t, stepTitle(""))
}

// Every step id the postgres plan emits must have a locale key — a missing one
// degrades the progress output to a raw id for the whole migration.
func TestEveryPostgresStepHasATitle(t *testing.T) {
	depsTestSetup(t)
	for _, id := range migrate.PostgresStepIDs() {
		assert.NotEqual(t, id, stepTitle(id), "step %q has no deps.step.%s locale key", id, id)
	}
}

func TestPreflightLines(t *testing.T) {
	depsTestSetup(t)
	lines := strings.Join(preflightLines(&migrate.PreflightResult{
		OK: true, From: "postgres:17.5", To: "postgres:18",
		DataSizeBytes: 3 << 30, RequiredHostBytes: 3<<30 + 512<<20, FreeHostBytes: 50 << 30,
		RequiredVolumeBytes: 4 << 30, FreeVolumeBytes: 40 << 30, WasRunning: true,
		Warnings: []string{"a warning"},
	}), "\n")
	assert.Contains(t, lines, "postgres:17.5")
	assert.Contains(t, lines, "postgres:18")
	// Every number is distinct so no line can stand in for another, and all of
	// them are binary units (fsutil.FormatBytes) — the free space they are
	// compared against is reported in powers of 1024.
	assert.Contains(t, lines, "3.0 GiB")  // data size
	assert.Contains(t, lines, "3.5 GiB")  // host space needed for the dump
	assert.Contains(t, lines, "50.0 GiB") // free host space
	assert.Contains(t, lines, "4.0 GiB")  // volume space needed
	assert.Contains(t, lines, "40.0 GiB") // free volume space
	assert.Contains(t, lines, tHelper("deps.preflight.willStop"))
	assert.Contains(t, lines, "a warning")

	// A stopped namespace is not told it will be stopped.
	quiet := strings.Join(preflightLines(&migrate.PreflightResult{OK: true, From: "a", To: "b"}), "\n")
	assert.NotContains(t, quiet, tHelper("deps.preflight.willStop"))

	// Problems and an existing target volume are shown before anything is done.
	bad := strings.Join(preflightLines(&migrate.PreflightResult{
		OK: false, From: "a", To: "b", Problems: []string{"not enough space"},
		ExistingTargetVolume: &migrate.ExistingVolume{Name: "citeck_pg18", SizeBytes: 1 << 30, Version: "18"},
	}), "\n")
	assert.Contains(t, bad, "not enough space")
	assert.Contains(t, bad, "citeck_pg18")
	assert.Contains(t, bad, "1.0 GiB")
	assert.Contains(t, bad, "18")
}

// A preflight the daemon refused before it touched Docker measured nothing.
// Its zeros are not facts, and printed above the reason they claim the
// namespace holds no data and the host has no free space — which is what a
// real stand showed ("Data size: 0 B", "Host (dump): need 0 B, free 0 B").
func TestPreflightLines_UnmeasuredSizesAreNotPrinted(t *testing.T) {
	depsTestSetup(t)
	refused := migrate.RefusedPreflight("postgres:17.5", "postgres:18",
		"a previous migration of postgres left a rollback pending")
	joined := strings.Join(preflightLines(&refused), "\n")
	assert.Contains(t, joined, "rollback pending", "the reason is still the point of the block")
	assert.NotContains(t, joined, "0 B")
	assert.NotContains(t, joined, tHelper("deps.preflight.data", "size", "0 B"))

	// A preflight that DID run still reports every number.
	measured := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	measured.OK = true
	measured.DataSizeBytes = 2 << 30
	measured.RequiredHostBytes = 2<<30 + migrate.SpaceMargin
	measured.RequiredVolumeBytes = measured.RequiredHostBytes
	measured.FreeHostBytes, measured.FreeVolumeBytes = 100<<30, 100<<30
	joined = strings.Join(preflightLines(&measured), "\n")
	assert.Contains(t, joined, "2.0 GiB")
	assert.Contains(t, joined, "100.0 GiB")
}

func TestDepsListLines_TableAndNotices(t *testing.T) {
	depsTestSetup(t)
	dto := &api.DependenciesDto{
		Items: []api.DependencyDto{
			{ID: "postgres", App: "postgres", CurrentImage: "postgres:17.5", CurrentVersion: "17.5",
				TargetImage: "postgres:18", TargetVersion: "18", Status: api.DependencyUpgradeAvailable, Migratable: true},
			{ID: "zookeeper", App: "zookeeper", CurrentImage: "zookeeper:3.9.2", TargetImage: "zookeeper:3.9.2",
				Status: api.DependencyUpToDate},
		},
	}
	out := strings.Join(depsListLines(dto), "\n")
	assert.Contains(t, out, tHelper("deps.col.dependency"))
	assert.Contains(t, out, "postgres")
	assert.Contains(t, out, "17.5")
	assert.Contains(t, out, "18")
	assert.Contains(t, out, "citeck deps upgrade postgres")
	// A dependency with no parsed version falls back to the image reference,
	// so the column is never blank.
	assert.Contains(t, out, "zookeeper:3.9.2")
	// …and one WITH a parsed version shows the version, not the image: the
	// point of the table is to compare 17.5 against 18 at a glance.
	assert.NotContains(t, out, "postgres:17.5")
	assert.NotContains(t, out, "postgres:18")

	// Nothing else to say ⇒ no stray notice lines.
	assert.NotContains(t, out, tHelper("deps.preflightFailed"))
}

func TestDepsListLines_Empty(t *testing.T) {
	depsTestSetup(t)
	out := strings.Join(depsListLines(&api.DependenciesDto{}), "\n")
	assert.Equal(t, tHelper("deps.empty"), out)
}

func TestDepsListLines_RollbackPending(t *testing.T) {
	depsTestSetup(t)
	out := strings.Join(depsListLines(&api.DependenciesDto{
		Items:           []api.DependencyDto{{ID: "postgres", CurrentImage: "postgres:17.5", TargetImage: "postgres:18"}},
		RollbackPending: "a previous migration of postgres (postgres:17.5 → postgres:18) left a rollback pending",
	}), "\n")
	assert.Contains(t, out, "left a rollback pending")
}

func TestDepsListLines_RunningMigrationAndLastResult(t *testing.T) {
	depsTestSetup(t)
	finished := time.Date(2026, 9, 8, 14, 5, 0, 0, time.Local).UnixMilli()

	running := strings.Join(depsListLines(&api.DependenciesDto{
		Items:     []api.DependencyDto{{ID: "postgres", CurrentImage: "postgres:17.5", TargetImage: "postgres:18"}},
		Migration: &api.DependencyMigrationDto{ID: "postgres", Step: "restore", StepIndex: 8, StepCount: 10},
	}), "\n")
	assert.Contains(t, running, tHelper("deps.step.restore"))
	assert.Contains(t, running, "8")
	assert.Contains(t, running, "10")

	ok := strings.Join(depsListLines(&api.DependenciesDto{
		Items: []api.DependencyDto{{ID: "postgres", CurrentImage: "postgres:18", TargetImage: "postgres:18"}},
		LastResult: &api.DependencyMigrationResultDto{
			ID: "postgres", From: "postgres:17.5", To: "postgres:18",
			FinishedAt: finished, Success: true, OldVolume: "citeck_postgres_default",
		},
	}), "\n")
	assert.Contains(t, ok, "postgres:17.5")
	assert.Contains(t, ok, formatEpochMillis(finished))
	assert.Regexp(t, `\d{4}-\d{2}-\d{2}`, ok)
	assert.Contains(t, ok, "citeck_postgres_default", "a successful migration must name the volume the old data is still in")

	failed := strings.Join(depsListLines(&api.DependenciesDto{
		Items: []api.DependencyDto{{ID: "postgres", CurrentImage: "postgres:17.5", TargetImage: "postgres:18"}},
		LastResult: &api.DependencyMigrationResultDto{
			ID: "postgres", From: "postgres:17.5", To: "postgres:18",
			FinishedAt: finished, Success: false, Error: "pg_restore died",
		},
	}), "\n")
	assert.Contains(t, failed, "pg_restore died")
	assert.NotContains(t, failed, tHelper("deps.lastSucceeded", "id", "postgres", "from", "postgres:17.5",
		"to", "postgres:18", "time", formatEpochMillis(finished)))
}

func TestFormatEpochMillis(t *testing.T) {
	ms := time.Date(2026, 9, 8, 14, 5, 0, 0, time.Local).UnixMilli()
	got := formatEpochMillis(ms)
	assert.Contains(t, got, "2026-09-08")
	assert.Contains(t, got, "14:05")
	assert.NotEqual(t, got, formatEpochMillis(ms+2*3600*1000))
	assert.Empty(t, formatEpochMillis(0), "a missing timestamp renders as nothing, not as 1970")
}

func TestPrintDependencies_JSONIsTheDTO(t *testing.T) {
	depsTestSetup(t)
	dto := &api.DependenciesDto{
		Items: []api.DependencyDto{{
			ID: "postgres", App: "postgres", CurrentImage: "postgres:17.5", CurrentVersion: "17.5",
			TargetImage: "postgres:18", TargetVersion: "18", Status: api.DependencyUpgradeAvailable, Migratable: true,
		}},
		RollbackPending: "rollback pending",
	}

	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	defer output.SetFormat(prev)

	out := captureStdout(t, func() { printDependencies(dto) })
	var back api.DependenciesDto
	require.NoError(t, json.Unmarshal([]byte(out), &back))
	assert.Equal(t, *dto, back)
	// The human table must not leak into the machine output.
	assert.NotContains(t, out, tHelper("deps.col.dependency"))
}

func TestPrintDependencies_TextRendersTheTable(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatText)
	defer output.SetFormat(prev)

	out := captureStdout(t, func() {
		printDependencies(&api.DependenciesDto{Items: []api.DependencyDto{
			{ID: "postgres", CurrentImage: "postgres:17.5", TargetImage: "postgres:18", Status: api.DependencyUpgradeAvailable},
		}})
	})
	assert.Contains(t, out, tHelper("deps.col.dependency"))
	assert.Contains(t, out, "postgres:18")
	assert.NotContains(t, out, `"items"`)
}

// --yes means "do not ask", not "cancel": promptConfirm returns its default
// under the global flag, so the migration prompt's default must be yes.
func TestConfirmMigration_YesFlagAccepts(t *testing.T) {
	depsTestSetup(t)
	prev := flagYes
	flagYes = true
	defer func() { flagYes = prev }()

	assert.True(t, confirmMigration("postgres", &migrate.PreflightResult{From: "postgres:17.5", To: "postgres:18"}))
}

func TestDepsCommandWiring(t *testing.T) {
	depsTestSetup(t)
	cmd := newDepsCmd()
	assert.Equal(t, "deps", cmd.Name())
	require.NotNil(t, cmd.RunE, "bare `citeck deps` must list, not print help")

	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	assert.True(t, names["list"])
	assert.True(t, names["upgrade"])

	upgrade := findSubCommand(cmd, "upgrade")
	require.NotNil(t, upgrade)
	require.NotNil(t, upgrade.Flags().Lookup("detach"), "--detach")
	require.NotNil(t, upgrade.Flags().Lookup("replace-existing"), "--replace-existing")
	assert.Equal(t, "d", upgrade.Flags().Lookup("detach").Shorthand)
	require.Error(t, upgrade.Args(upgrade, []string{}), "upgrade takes exactly one dependency id")
	require.Error(t, upgrade.Args(upgrade, []string{"postgres", "rabbitmq"}))
	require.NoError(t, upgrade.Args(upgrade, []string{"postgres"}))
}

func TestRootRegistersDeps(t *testing.T) {
	root := NewRootCmd(BuildInfo{Version: "test"})
	assert.NotNil(t, findSubCommand(root, "deps"))
}

func findSubCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}
