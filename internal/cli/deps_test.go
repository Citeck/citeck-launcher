package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spf13/cobra"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/output"
)

// depsTestSetup pins the locale and disables colors so the assertions below
// compare rendered text, not ANSI escapes. The colors flag is a PROCESS
// global: it is restored on cleanup, or every test that runs after this one
// silently inherits colorless output it never asked for.
func depsTestSetup(t *testing.T) {
	t.Helper()
	initI18n("en")
	prevColors := colorsAreEnabled()
	output.SetColorsEnabled(false)
	t.Cleanup(func() { output.SetColorsEnabled(prevColors) })
}

// A helper that flips a global and does not put it back is not a local
// convenience — it is a change to every later test in the package.
func TestDepsTestSetupRestoresTheColorsFlag(t *testing.T) {
	prev := colorsAreEnabled()
	output.SetColorsEnabled(true)
	t.Cleanup(func() { output.SetColorsEnabled(prev) })

	t.Run("inner", func(t *testing.T) {
		depsTestSetup(t)
		assert.False(t, colorsAreEnabled(), "the helper turns colors off for its own test")
	})
	assert.True(t, colorsAreEnabled(), "…and puts back what it found when that test ends")
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

// upgrade-blocked and bundle-older are the two statuses whose EXPLANATION is a
// full English sentence the daemon builds (StatusDetail). Neither may borrow
// the words of an upgrade: "requires a newer launcher" is a lie for both — no
// launcher lifts a vendor refusal and none moves data backwards.
func TestFormatDependencyStatus_TheTwoHeldBackStatusesBorrowNoUpgradeWords(t *testing.T) {
	depsTestSetup(t)
	blocked := formatDependencyStatus(api.DependencyDto{
		ID: "rabbitmq", Status: api.DependencyUpgradeBlocked, TargetVersion: "4.3.5",
		StatusDetail: "rabbitmq does not support 4.1.8 → 4.3.5 in one step: upgrade to 4.2 first.",
	})
	older := formatDependencyStatus(api.DependencyDto{
		ID: "postgres", Status: api.DependencyBundleOlder,
		CurrentVersion: "18.6", TargetVersion: "17.5",
		StatusDetail: "the bundle offers postgres:17.5, which is older than the postgres:18.6 …",
	})
	for name, got := range map[string]string{"upgrade-blocked": blocked, "bundle-older": older} {
		assert.NotContains(t, got, tHelper("deps.status.requiresLauncherUpdate"), name)
		assert.NotContains(t, got, tHelper("deps.status.upgradeAvailable", "id", "postgres"), name)
		assert.NotContains(t, got, "citeck deps upgrade", name)
		assert.NotContains(t, got, "deps.status.", "%s rendered a bare key", name)
	}
	// bundle-older names the version the data STAYS on — the whole point is
	// that nothing is going to happen.
	assert.Contains(t, older, "18.6")
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

// A bundle offering something OLDER than the data is not an upgrade: there is
// nothing to migrate and nothing to wait for. Folded into "Upgrades available"
// it would send an operator to `citeck deps upgrade`, which refuses it. It
// still has to be VISIBLE in `citeck status` (ruling 8) — an operator who
// never opens `citeck deps` must learn the bundle is offering an older version.
func TestDependencyHintLine_ABackwardsHoldIsNotAnUpgrade(t *testing.T) {
	depsTestSetup(t)
	older := &api.NamespaceDto{DependencyUpgrades: []api.DependencyUpgradeDto{
		{ID: "postgres", From: "postgres:18.6", To: "postgres:17.5", BundleOlder: true},
	}}
	line := dependencyHintLine(older, nil)
	assert.NotContains(t, line, tHelper("deps.statusHint", "list", "postgres postgres:18.6 → postgres:17.5"))
	assert.Contains(t, line, tHelper("deps.bundleOlderHint", "list", "postgres postgres:18.6 → postgres:17.5"))
	assert.Contains(t, line, "citeck deps", "the pointer is what makes it actionable")

	// Both at once: two clauses on ONE line (`citeck status` prints it after a
	// "Deps:" label), and the upgrade must not swallow the backwards hold.
	both := &api.NamespaceDto{DependencyUpgrades: []api.DependencyUpgradeDto{
		{ID: "rabbitmq", From: "rabbitmq:4.1.2-management", To: "rabbitmq:4.2.9-management", Migratable: true},
		{ID: "postgres", From: "postgres:18.6", To: "postgres:17.5", BundleOlder: true},
	}}
	line = dependencyHintLine(both, nil)
	assert.NotContains(t, line, "\n", "citeck status prints this after a label — it is ONE line")
	assert.Contains(t, line, tHelper("deps.statusHint", "list", "rabbitmq rabbitmq:4.1.2-management → rabbitmq:4.2.9-management"))
	assert.Contains(t, line, tHelper("deps.bundleOlderHint", "list", "postgres postgres:18.6 → postgres:17.5"))
	assert.Equal(t, 1, strings.Count(line, "citeck deps"), "the pointer is added once:\n%s", line)
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
		done, verdict := pollActionVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("postgres", true, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt, deps.ResultKindMigration)
		assert.True(t, done)
		require.NoError(t, verdict)
	})
	t.Run("a failed migration ends it with the reason", func(t *testing.T) {
		done, verdict := pollActionVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("postgres", false, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt, deps.ResultKindMigration)
		assert.True(t, done)
		require.ErrorContains(t, verdict, "boom")
	})
	t.Run("a running migration keeps following", func(t *testing.T) {
		done, _ := pollActionVerdict(fetchOf(&api.DependenciesDto{
			Migration:  &api.DependencyMigrationDto{ID: "postgres", Step: "dump"},
			LastResult: result("postgres", true, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt, deps.ResultKindMigration)
		assert.False(t, done, "the daemon still reports it running")
	})
	// The result of a PREVIOUS migration of the same dependency must never be
	// reported as this run's verdict.
	t.Run("a verdict older than the click is not ours", func(t *testing.T) {
		done, _ := pollActionVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("postgres", true, clickedAt.Add(-time.Hour)),
		}, nil), "postgres", clickedAt, deps.ResultKindMigration)
		assert.False(t, done)
	})
	t.Run("another dependency's verdict is not ours", func(t *testing.T) {
		done, _ := pollActionVerdict(fetchOf(&api.DependenciesDto{
			LastResult: result("rabbitmq", true, clickedAt.Add(time.Minute)),
		}, nil), "postgres", clickedAt, deps.ResultKindMigration)
		assert.False(t, done)
	})
	t.Run("a failed poll keeps following", func(t *testing.T) {
		done, _ := pollActionVerdict(fetchOf(nil, errors.New("socket closed")), "postgres", clickedAt, deps.ResultKindMigration)
		assert.False(t, done)
	})
	t.Run("no verdict at all keeps following", func(t *testing.T) {
		done, _ := pollActionVerdict(fetchOf(&api.DependenciesDto{}, nil), "postgres", clickedAt, deps.ResultKindMigration)
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

// Every step id any of the three plans emits must have a locale key, in ALL
// EIGHT files — a missing one degrades the progress output to a raw id for the
// whole operation, and a key present only in en degrades it for everyone else.
//
// The three lists are read from the migrate package rather than copied: a step
// renamed there without its key is exactly the defect this pins, and a private
// copy of the list here could not see it.
func TestEveryStepIdHasALocaleKey(t *testing.T) {
	depsTestSetup(t)
	ids := migrate.PostgresStepIDs()
	ids = append(ids, migrate.CopyStepIDs()...)
	ids = append(ids, migrate.RollbackStepIDs()...)
	// The daemon publishes this one before either plan exists, so it belongs
	// to no list and would otherwise be the one step id nothing checks.
	ids = append(ids, api.DependencyMigrationStepPreparing)

	for _, id := range ids {
		assert.NotEqual(t, id, stepTitle(id), "step %q has no deps.step.%s locale key", id, id)
	}
	for _, loc := range SupportedLocales {
		data, err := i18n.LocaleFS.ReadFile("locales/" + loc.Code + ".json")
		require.NoError(t, err, loc.Code)
		var keys map[string]string
		require.NoError(t, json.Unmarshal(data, &keys), loc.Code)
		for _, id := range ids {
			title, ok := keys["deps.step."+id]
			assert.True(t, ok, "%s.json has no deps.step.%s", loc.Code, id)
			assert.NotEmpty(t, title, "%s.json leaves deps.step.%s empty", loc.Code, id)
		}
	}
}

// Every `deps.*` key the commands name in their source must exist in ALL EIGHT
// files. A missing one renders as the bare lookup key — "deps.rollback.confirm"
// as the confirmation prompt — and nothing else catches it: TestLocaleCompleteness
// only compares the eight files with each other, so a key no file has is
// perfectly consistent, and a t() call is not a compile-time reference to
// anything. (This test was written after exactly that happened.)
//
// It reads the SOURCE rather than a hand-kept list because a hand-kept list is
// the same drift, one indirection further away. Keys built at runtime from a
// prefix ("deps.step." + id) end in a dot and are covered by
// TestEveryStepIdHasALocaleKey instead.
func TestEveryLocaleKeyTheDepsCommandsNameExists(t *testing.T) {
	depsTestSetup(t)
	src, err := os.ReadFile("deps.go")
	require.NoError(t, err)
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`"((?:help\.)?deps\.[A-Za-z0-9.\-]+)"`).FindAllStringSubmatch(string(src), -1) {
		if !strings.HasSuffix(m[1], ".") {
			keys[m[1]] = true
		}
	}
	require.NotEmpty(t, keys, "the regexp stopped matching — this test would pass on anything")

	for _, loc := range SupportedLocales {
		data, readErr := i18n.LocaleFS.ReadFile("locales/" + loc.Code + ".json")
		require.NoError(t, readErr, loc.Code)
		var have map[string]string
		require.NoError(t, json.Unmarshal(data, &have), loc.Code)
		for key := range keys {
			value, ok := have[key]
			assert.True(t, ok, "%s.json has no %s", loc.Code, key)
			assert.NotEmpty(t, value, "%s.json leaves %s empty", loc.Code, key)
		}
	}
}

// A copy-upgrade plan writes NO host file at all — it copies a volume — so its
// preflight requires zero bytes on the host. Rendering that half anyway says
// the migration needs no space and the disk is empty, beside a volume line
// that carries the real requirement.
func TestSpaceRequirementLines_SkipTheHostHalfWhenNothingIsWrittenThere(t *testing.T) {
	depsTestSetup(t)
	pre := migrate.NewPreflightResult("rabbitmq:4.1.8-management", "rabbitmq:4.2.9-management")
	pre.OK = true
	pre.SpaceChecked = true
	pre.DataSizeBytes = 2 << 30
	pre.RequiredVolumeBytes, pre.FreeVolumeBytes = 3<<30, 30<<30
	// The copy plan measures no host filesystem, so both host numbers are zero.
	pre.RequiredHostBytes, pre.FreeHostBytes = 0, 0

	lines := strings.Join(preflightLines(&pre), "\n")
	assert.Contains(t, lines, tHelper("deps.preflight.volume", "need", "3.0 GiB", "free", "30.0 GiB"))
	assert.NotContains(t, lines, tHelper("deps.preflight.host", "need", "0 B", "free", "0 B"))
	assert.NotContains(t, lines, "0 B")
	// The data size is still a measurement, and it is what the requirement is
	// derived from.
	assert.Contains(t, lines, tHelper("deps.preflight.data", "size", "2.0 GiB"))
}

func TestPreflightLines(t *testing.T) {
	depsTestSetup(t)
	lines := strings.Join(preflightLines(&migrate.PreflightResult{
		OK: true, From: "postgres:17.5", To: "postgres:18",
		DataSizeBytes: 3 << 30, RequiredHostBytes: 3<<30 + 512<<20, FreeHostBytes: 50 << 30,
		RequiredVolumeBytes: 4 << 30, FreeVolumeBytes: 40 << 30, WasRunning: true,
		SpaceChecked: true,
		Warnings:     []string{"a warning"},
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
	measured.SpaceChecked = true
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

// The sentence behind a held-back status is a full English paragraph the
// daemon built (StatusDetail): it is rendered VERBATIM, once, below the table
// — never in the status cell, which pads every other row to its width — and it
// names its row, or a namespace with two of them would print two anonymous
// paragraphs.
func TestDepsListLines_StatusDetailIsRenderedVerbatimBelowTheTable(t *testing.T) {
	depsTestSetup(t)
	blocked := "rabbitmq does not support 4.1.8 → 4.3.5 in one step: upgrade to 4.2 first."
	older := "the bundle offers postgres:17.5, which is older than the postgres:18.6 this namespace's data runs on."
	out := strings.Join(depsListLines(&api.DependenciesDto{Items: []api.DependencyDto{
		{ID: "rabbitmq", CurrentVersion: "4.1.8", TargetVersion: "4.3.5",
			Status: api.DependencyUpgradeBlocked, StatusDetail: blocked},
		{ID: "postgres", CurrentVersion: "18.6", TargetVersion: "17.5",
			Status: api.DependencyBundleOlder, StatusDetail: older},
		{ID: "zookeeper", CurrentVersion: "3.9.2", TargetVersion: "3.9.2", Status: api.DependencyUpToDate},
	}}), "\n")

	assert.Contains(t, out, tHelper("deps.statusDetail", "id", "rabbitmq", "detail", blocked))
	assert.Contains(t, out, tHelper("deps.statusDetail", "id", "postgres", "detail", older))
	// Neither may reach the table cell: the column is padded to its widest
	// value, so one 300-character sentence there ruins every other row.
	rows := strings.Split(out, "\n")
	assert.NotContains(t, rows[1], blocked, "the sentence is not a table cell")
	// A row with nothing to explain adds no line.
	assert.NotContains(t, out, "zookeeper:")
	assert.NotContains(t, out, "citeck deps upgrade", "neither status offers an upgrade")
}

// The rollback is invisible unless the list says it exists: nothing else in
// the CLI mentions it, and a namespace that migrated last week has no other
// way to learn it can go back.
func TestDepsListLines_AnAvailableRollbackIsOffered(t *testing.T) {
	depsTestSetup(t)
	out := strings.Join(depsListLines(&api.DependenciesDto{Items: []api.DependencyDto{
		{ID: "postgres", CurrentVersion: "18.6", TargetVersion: "18.6", Status: api.DependencyUpToDate,
			Rollback: &api.DependencyRollbackDto{
				ToImage: "postgres:17.5", ToVersion: "17.5",
				Volume: "citeck_postgres2_default", FrozenVolume: "citeck_postgres3_default", Available: true,
			}},
	}}), "\n")
	assert.Contains(t, out, tHelper("deps.rollbackAvailable", "list", "postgres → 17.5"))

	// An offer the launcher cannot USE is not advertised. The volume is gone
	// because `deps.oldVolume` told the operator to delete it, so nagging
	// about it on every list would be the launcher arguing with itself; the
	// reason is still there for anyone who runs `citeck deps rollback`.
	unavailable := strings.Join(depsListLines(&api.DependenciesDto{Items: []api.DependencyDto{
		{ID: "postgres", Status: api.DependencyUpToDate, Rollback: &api.DependencyRollbackDto{
			ToImage: "postgres:17.5", ToVersion: "17.5", Volume: "citeck_postgres2_default",
			Available: false, Problem: "volume citeck_postgres2_default is gone",
		}},
	}}), "\n")
	assert.NotContains(t, unavailable, tHelper("deps.rollbackAvailable", "list", "postgres → 17.5"))
	assert.NotContains(t, unavailable, "is gone")
}

// A rollback and a migration share the ONE result slot a namespace has, so
// "18.6 → 17.5 succeeded" would otherwise be reported as a migration — which
// is the one thing the launcher will never do to data.
func TestDepsListLines_ARollbackResultIsNotReportedAsAMigration(t *testing.T) {
	depsTestSetup(t)
	finished := time.Date(2026, 9, 9, 12, 41, 0, 0, time.Local).UnixMilli()
	out := strings.Join(depsListLines(&api.DependenciesDto{
		Items: []api.DependencyDto{{ID: "postgres", CurrentVersion: "17.5", TargetVersion: "18.6"}},
		LastResult: &api.DependencyMigrationResultDto{
			ID: "postgres", From: "postgres:18.6", To: "postgres:17.5", Success: true,
			FinishedAt: finished, OldVolume: "citeck_postgres3_default", Kind: "rollback",
		},
	}), "\n")
	assert.Contains(t, out, tHelper("deps.rollback.done", "id", "postgres",
		"from", "postgres:18.6", "to", "postgres:17.5", "time", formatEpochMillis(finished)))
	assert.NotContains(t, out, tHelper("deps.lastSucceeded", "id", "postgres",
		"from", "postgres:18.6", "to", "postgres:17.5", "time", formatEpochMillis(finished)))
	// The volume it names is the one the namespace LEFT — kept and never read
	// again — which is not what deps.oldVolume says about a migration ("delete
	// it once you trust the new version": there is no new version here).
	assert.Contains(t, out, tHelper("deps.rollback.frozenVolume", "volume", "citeck_postgres3_default"))
	assert.NotContains(t, out, tHelper("deps.oldVolume", "volume", "citeck_postgres3_default"))
}

// The same discriminator on the LIVE channel: a running rollback reported as a
// migration sends the operator looking for one they did not start.
func TestDepsListLines_ARunningRollbackSaysRollback(t *testing.T) {
	depsTestSetup(t)
	out := strings.Join(depsListLines(&api.DependenciesDto{
		Items:     []api.DependencyDto{{ID: "postgres", CurrentVersion: "18.6"}},
		Migration: &api.DependencyMigrationDto{ID: "postgres", Step: "switch-generation", StepIndex: 2, StepCount: 3, Kind: "rollback"},
	}), "\n")
	assert.Contains(t, out, tHelper("deps.rollbackRunning", "id", "postgres",
		"step", tHelper("deps.step.switch-generation"), "index", "2", "total", "3"))
	assert.NotContains(t, out, tHelper("deps.migrationRunning", "id", "postgres",
		"step", tHelper("deps.step.switch-generation"), "index", "2", "total", "3"))
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

// One filesystem carrying both writes is ONE line quoting the SUM. Printed as
// the two independent lines, each half looks satisfiable on its own while the
// daemon refuses the migration for wanting them together — the confirm screen
// would be arguing with the error it is about to produce. Same rule as the web
// dialog (DependenciesDialog.tsx).
func TestPreflightLines_SharedFilesystemIsOneLineWithTheSum(t *testing.T) {
	depsTestSetup(t)
	shared := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	shared.OK = true
	shared.DataSizeBytes = 3 << 30
	shared.RequiredHostBytes = 3<<30 + 512<<20   // 3.5 GiB
	shared.RequiredVolumeBytes = 3<<30 + 512<<20 // 3.5 GiB
	shared.FreeHostBytes, shared.FreeVolumeBytes = 20<<30, 20<<30
	shared.SharedFilesystem = true
	shared.RequiredTotalBytes = 7 << 30
	shared.SpaceChecked = true

	lines := strings.Join(preflightLines(&shared), "\n")
	assert.Contains(t, lines, tHelper("deps.preflight.shared", "need", "7.0 GiB", "free", "20.0 GiB"))
	// The pair must be GONE, not merely joined by a third line: quoting two
	// requirements smaller than the one the daemon enforces is the defect.
	assert.NotContains(t, lines, tHelper("deps.preflight.host", "need", "3.5 GiB", "free", "20.0 GiB"))
	assert.NotContains(t, lines, tHelper("deps.preflight.volume", "need", "3.5 GiB", "free", "20.0 GiB"))
	// The data size stays: it is what the two requirements are derived from.
	assert.Contains(t, lines, tHelper("deps.preflight.data", "size", "3.0 GiB"))
}

// A zero free measurement is a measurement that FAILED — it has already
// produced its own problem line — so it must not be taken as the minimum:
// that renders a failed probe as a full disk.
func TestPreflightLines_SharedFreeSkipsAFailedMeasurement(t *testing.T) {
	depsTestSetup(t)
	pre := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	pre.RequiredHostBytes = 1 << 30
	pre.RequiredTotalBytes = 2 << 30
	pre.SharedFilesystem = true
	pre.SpaceChecked = true
	pre.FreeHostBytes, pre.FreeVolumeBytes = 0, 40<<30

	assert.Contains(t, strings.Join(preflightLines(&pre), "\n"),
		tHelper("deps.preflight.shared", "need", "2.0 GiB", "free", "40.0 GiB"))

	// The other way round, and with both halves measured (the smaller wins).
	pre.FreeHostBytes, pre.FreeVolumeBytes = 40<<30, 0
	assert.Contains(t, strings.Join(preflightLines(&pre), "\n"),
		tHelper("deps.preflight.shared", "need", "2.0 GiB", "free", "40.0 GiB"))
	pre.FreeHostBytes, pre.FreeVolumeBytes = 40<<30, 30<<30
	assert.Contains(t, strings.Join(preflightLines(&pre), "\n"),
		tHelper("deps.preflight.shared", "need", "2.0 GiB", "free", "30.0 GiB"))
}

// Two filesystems keep the two lines: a sum across two disks means nothing,
// and SharedFilesystem — never the zero in RequiredTotalBytes — is what tells
// the two layouts apart.
func TestPreflightLines_SeparateFilesystemsKeepTheTwoLines(t *testing.T) {
	depsTestSetup(t)
	pre := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	pre.RequiredHostBytes, pre.FreeHostBytes = 3<<30, 50<<30
	pre.RequiredVolumeBytes, pre.FreeVolumeBytes = 4<<30, 40<<30
	pre.SpaceChecked = true

	lines := strings.Join(preflightLines(&pre), "\n")
	assert.Contains(t, lines, tHelper("deps.preflight.host", "need", "3.0 GiB", "free", "50.0 GiB"))
	assert.Contains(t, lines, tHelper("deps.preflight.volume", "need", "4.0 GiB", "free", "40.0 GiB"))
	assert.NotContains(t, lines, tHelper("deps.preflight.shared", "need", "0 B", "free", "40.0 GiB"))
}

// fakeDepsDaemon is the daemon surface `citeck deps upgrade` drives, so the
// orchestration — the refusals, the confirmation, the follow loop, the verdict
// and the exit code — can be tested without a socket.
type fakeDepsDaemon struct {
	list         *api.DependenciesDto
	listErr      error
	listCalls    int
	onList       func(call int) *api.DependenciesDto
	pre          *migrate.PreflightResult
	preErr       error
	preCalls     int
	migrateRes   *api.ActionResultDto
	migrateErr   error
	migrateCalls int
	gotReplace   bool
	// The rollback half is counted separately from the migration half on
	// purpose: "a rollback is not a migration" is an assertion, and one shared
	// counter could not make it.
	rollbackPre      *migrate.PreflightResult
	rollbackPreErr   error
	rollbackPreCalls int
	rollbackRes      *api.ActionResultDto
	rollbackErr      error
	rollbackCalls    int
	events           chan api.EventDto
	streamErr        error
	streamCalls      int
}

func (f *fakeDepsDaemon) GetDependencies() (*api.DependenciesDto, error) {
	f.listCalls++
	if f.onList != nil {
		return f.onList(f.listCalls), f.listErr
	}
	return f.list, f.listErr
}

func (f *fakeDepsDaemon) DependencyPreflight(string) (*migrate.PreflightResult, error) {
	f.preCalls++
	return f.pre, f.preErr
}

func (f *fakeDepsDaemon) MigrateDependency(_ string, replaceExisting bool) (*api.ActionResultDto, error) {
	f.migrateCalls++
	f.gotReplace = replaceExisting
	if f.migrateRes == nil && f.migrateErr == nil {
		return &api.ActionResultDto{Success: true, Message: "migration of postgres started"}, nil
	}
	return f.migrateRes, f.migrateErr
}

func (f *fakeDepsDaemon) DependencyRollbackPreflight(string) (*migrate.PreflightResult, error) {
	f.rollbackPreCalls++
	return f.rollbackPre, f.rollbackPreErr
}

func (f *fakeDepsDaemon) RollbackDependency(string) (*api.ActionResultDto, error) {
	f.rollbackCalls++
	if f.rollbackRes == nil && f.rollbackErr == nil {
		return &api.ActionResultDto{Success: true, Message: "Rollback of postgres to postgres:17.5 started"}, nil
	}
	return f.rollbackRes, f.rollbackErr
}

func (f *fakeDepsDaemon) StreamEvents(context.Context) (<-chan api.EventDto, error) {
	f.streamCalls++
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	if f.events == nil {
		f.events = make(chan api.EventDto)
	}
	return f.events, nil
}

// okPreflight is a preflight that passes, with the two names the report and
// the confirmation quote.
func okPreflight() *migrate.PreflightResult {
	pre := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	pre.OK = true
	pre.DataSizeBytes = 1 << 30
	pre.RequiredHostBytes = 1<<30 + migrate.SpaceMargin
	pre.RequiredVolumeBytes = pre.RequiredHostBytes
	pre.FreeHostBytes, pre.FreeVolumeBytes = 50<<30, 50<<30
	pre.SpaceChecked = true
	return &pre
}

// testActionOpts drives the follow loop in milliseconds instead of the
// production 15s/6h, and answers the confirmation without a terminal.
func testActionOpts(detach, replaceExisting, confirm bool) depsActionOpts {
	return depsActionOpts{
		detach:          detach,
		replaceExisting: replaceExisting,
		confirm:         func(string, *migrate.PreflightResult) bool { return confirm },
		follow:          depsFollow{poll: 5 * time.Millisecond, timeout: 2 * time.Second},
	}
}

func bufferedEvents(evts ...api.EventDto) chan api.EventDto {
	ch := make(chan api.EventDto, len(evts)+1)
	for _, e := range evts {
		ch <- e
	}
	return ch
}

// A pending rollback freezes the pin and the daemon refuses a new migration
// anyway; asking for the preflight first would walk the whole data volume
// before it could say so.
func TestDepsUpgrade_PendingRollbackIsRefusedBeforeThePreflight(t *testing.T) {
	depsTestSetup(t)
	// The preflight is armed and healthy: the point is that it is never ASKED
	// for, not that it would have failed.
	f := &fakeDepsDaemon{list: &api.DependenciesDto{RollbackPending: "a rollback is pending"}, pre: okPreflight()}

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "a rollback is pending")
	assert.Equal(t, depsOutcomeRefused, rep.Outcome)
	assert.Zero(t, f.preCalls, "the preflight walks the data volume — it must not run")
	assert.Zero(t, f.migrateCalls)
}

func TestDepsUpgrade_FailedPreflightStartsNothing(t *testing.T) {
	depsTestSetup(t)
	bad := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	bad.Problems = append(bad.Problems, "not enough free space")
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: &bad}

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.Error(t, err)
	assert.Equal(t, depsOutcomeRefused, rep.Outcome)
	assert.Zero(t, f.migrateCalls)
	// The reason travels with the verdict, not only on the terminal: this is
	// the whole machine-readable answer a script gets.
	require.NotNil(t, rep.Preflight)
	assert.Contains(t, rep.Preflight.Problems, "not enough free space")
}

// Deleting a volume this launcher did not create is a separate decision from
// "migrate": a refusal naming the flag, never one more prompt.
func TestDepsUpgrade_ExistingTargetVolumeNeedsTheFlag(t *testing.T) {
	depsTestSetup(t)
	pre := okPreflight()
	pre.ExistingTargetVolume = &migrate.ExistingVolume{Name: "citeck_pg18", SizeBytes: 1 << 30, Version: "18"}
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: pre}

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "citeck_pg18")
	assert.Equal(t, depsOutcomeRefused, rep.Outcome)
	assert.Zero(t, f.migrateCalls)

	// With the flag the migration runs AND the confirmation is carried to the
	// daemon, which re-checks it in the step.
	f2 := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: pre}
	rep2, err2 := depsUpgradeSteps(f2, "postgres", testActionOpts(true, true, true))
	require.NoError(t, err2)
	assert.Equal(t, depsOutcomeStarted, rep2.Outcome)
	assert.Equal(t, 1, f2.migrateCalls)
	assert.True(t, f2.gotReplace, "--replace-existing must reach the daemon")
}

func TestDepsUpgrade_DeclinedConfirmationStartsNothing(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, false))
	require.NoError(t, err, "declining is not a failure")
	assert.Equal(t, depsOutcomeCanceled, rep.Outcome)
	assert.Zero(t, f.migrateCalls)
	assert.Zero(t, f.streamCalls, "nothing to follow")
}

// --detach returns as soon as the daemon accepts: the migration runs there,
// and this command's job is done.
func TestDepsUpgrade_DetachReportsStarted(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(true, false, true))
	require.NoError(t, err)
	assert.Equal(t, depsOutcomeStarted, rep.Outcome)
	assert.Equal(t, "postgres:17.5", rep.From)
	assert.Equal(t, "postgres:18", rep.To)
	assert.Equal(t, "migration of postgres started", rep.Message)
	assert.Zero(t, f.streamCalls, "--detach does not follow")
}

// A 400/409 refusal is already an error from the client; a 202 that reports
// failure is one too, because nothing would follow it.
func TestDepsUpgrade_ADaemonRefusalIsNotFollowed(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{
		list: &api.DependenciesDto{}, pre: okPreflight(),
		migrateRes: &api.ActionResultDto{Success: false, Message: "another long operation is running"},
	}
	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "another long operation is running")
	assert.Equal(t, depsOutcomeRefused, rep.Outcome)
}

func TestDepsUpgrade_FollowsToTheTerminalEvent(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationStart, AppName: "postgres", After: "postgres:17.5 → postgres:18"},
		// Another dependency's terminal event must not end this wait.
		api.EventDto{Type: api.EventDepsMigrationComplete, AppName: "rabbitmq", After: "rabbitmq done"},
		api.EventDto{Type: api.EventDepsMigrationProgress, AppName: "postgres", Phase: "dump", Current: 4, Total: 10},
		api.EventDto{Type: api.EventDepsMigrationComplete, AppName: "postgres", After: "postgres migrated to postgres:18"},
	)

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.NoError(t, err)
	assert.Equal(t, depsOutcomeMigrated, rep.Outcome)
	assert.Equal(t, 1, f.streamCalls)
}

func TestDepsUpgrade_TheErrorEventIsTheVerdict(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationError, AppName: "postgres", After: "restore failed"},
	)

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "restore failed")
	assert.Equal(t, depsOutcomeFailed, rep.Outcome)
}

// The daemon DROPS an event rather than block a full subscriber channel, and a
// migration's own stop/start burst is the most likely thing to fill it. The
// follow loop therefore polls the recorded verdict: without it a dropped
// terminal event leaves the command waiting six hours on a finished migration.
func TestDepsUpgrade_PollCatchesADroppedTerminalEvent(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{pre: okPreflight()}
	f.events = make(chan api.EventDto) // open and silent: every event was dropped
	f.onList = func(call int) *api.DependenciesDto {
		if call == 1 {
			return &api.DependenciesDto{} // the pre-flight rollback check
		}
		return &api.DependenciesDto{LastResult: &api.DependencyMigrationResultDto{
			ID: "postgres", From: "postgres:17.5", To: "postgres:18",
			Success: true, FinishedAt: time.Now().Add(time.Minute).UnixMilli(),
		}}
	}

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.NoError(t, err)
	assert.Equal(t, depsOutcomeMigrated, rep.Outcome)
	assert.Greater(t, f.listCalls, 1, "the verdict came from the poll, not from an event")
}

// A migration this command lost sight of is NOT a failed one: the daemon runs
// it on its own context and finishes it whether or not anyone is listening.
func TestDepsUpgrade_ALostStreamIsUnknownNotFailed(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = make(chan api.EventDto)
	close(f.events)

	rep, err := depsUpgradeSteps(f, "postgres", testActionOpts(false, false, true))
	require.ErrorContains(t, err, "citeck deps")
	assert.Equal(t, depsOutcomeUnknown, rep.Outcome)
}

// …and so is a follow that runs out of time.
func TestDepsUpgrade_ATimedOutFollowIsUnknown(t *testing.T) {
	depsTestSetup(t)
	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = make(chan api.EventDto)
	opts := testActionOpts(false, false, true)
	opts.follow = depsFollow{poll: time.Hour, timeout: 20 * time.Millisecond}

	rep, err := depsUpgradeSteps(f, "postgres", opts)
	require.Error(t, err)
	assert.Equal(t, depsOutcomeUnknown, rep.Outcome)
}

// `--format json` answers with ONE object and nothing else: no preflight
// prose, no accept message, no per-step progress. AGENTS.md's CLI conventions
// promise --format json on any command, and a verdict a script can parse is
// what that means for a command whose whole point is the verdict.
func TestDepsUpgrade_JSONPrintsOnlyTheVerdict(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationProgress, AppName: "postgres", Phase: "dump", Current: 4, Total: 10},
		api.EventDto{Type: api.EventDepsMigrationComplete, AppName: "postgres", After: "postgres migrated to postgres:18"},
	)

	var err error
	out := captureStdout(t, func() { err = depsUpgrade(f, "postgres", testActionOpts(false, false, true)) })
	require.NoError(t, err)

	var rep depsActionReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep), "the whole of stdout must be one JSON object:\n%s", out)
	assert.Equal(t, depsOutcomeMigrated, rep.Outcome)
	assert.Equal(t, "postgres", rep.ID)
	assert.Equal(t, "postgres:17.5", rep.From)
	assert.Equal(t, "postgres:18", rep.To)
	assert.NotContains(t, out, tHelper("deps.preflight.title", "from", "postgres:17.5", "to", "postgres:18"))
	assert.NotContains(t, out, tHelper("deps.step.dump"), "no per-step progress prose")
	// The daemon's accept message is a FIELD, never a line of its own — the
	// Unmarshal above is what proves nothing was printed beside the object.
	assert.Equal(t, "migration of postgres started", rep.Message)
}

// A failure has to be IN the object: Execute() suppresses the "Error:" line in
// JSON mode, so a report without the reason leaves a script with an exit code
// and nothing else.
func TestDepsUpgrade_JSONCarriesTheFailure(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationError, AppName: "postgres", After: "restore failed"},
	)

	var err error
	out := captureStdout(t, func() { err = depsUpgrade(f, "postgres", testActionOpts(false, false, true)) })
	require.Error(t, err, "a failed migration must still exit non-zero")

	var rep depsActionReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep), out)
	assert.Equal(t, depsOutcomeFailed, rep.Outcome)
	assert.Contains(t, rep.Error, "restore failed")
	assert.Equal(t, err.Error(), rep.Error, "the object and the exit code must tell the same story")
}

// Text mode is unchanged: the running commentary, and no JSON in it.
func TestDepsUpgrade_TextPrintsTheProgressAndNoJSON(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatText)
	t.Cleanup(func() { output.SetFormat(prev) })

	f := &fakeDepsDaemon{list: &api.DependenciesDto{}, pre: okPreflight()}
	f.events = bufferedEvents(
		api.EventDto{Type: api.EventDepsMigrationProgress, AppName: "postgres", Phase: "dump", Current: 4, Total: 10},
		api.EventDto{Type: api.EventDepsMigrationComplete, AppName: "postgres", After: "postgres migrated to postgres:18"},
	)

	var err error
	out := captureStdout(t, func() { err = depsUpgrade(f, "postgres", testActionOpts(false, false, true)) })
	require.NoError(t, err)
	assert.Contains(t, out, tHelper("deps.preflight.title", "from", "postgres:17.5", "to", "postgres:18"))
	assert.Contains(t, out, "migration of postgres started")
	assert.Contains(t, out, tHelper("deps.step.dump"))
	assert.Contains(t, out, "postgres migrated to postgres:18")
	assert.NotContains(t, out, `"outcome"`)
}

// The poll's own verdict lines are prose too, and in JSON mode they would sit
// in front of the object.
func TestDepsUpgrade_JSONSuppressesThePollVerdictLines(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	f := &fakeDepsDaemon{pre: okPreflight()}
	f.events = make(chan api.EventDto)
	f.onList = func(call int) *api.DependenciesDto {
		if call == 1 {
			return &api.DependenciesDto{}
		}
		return &api.DependenciesDto{LastResult: &api.DependencyMigrationResultDto{
			ID: "postgres", From: "postgres:17.5", To: "postgres:18", Success: true,
			OldVolume: "citeck_postgres_default", FinishedAt: time.Now().Add(time.Minute).UnixMilli(),
		}}
	}

	out := captureStdout(t, func() { _ = depsUpgrade(f, "postgres", testActionOpts(false, false, true)) })
	var rep depsActionReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep), "the whole of stdout must be one JSON object:\n%s", out)
	assert.Equal(t, depsOutcomeMigrated, rep.Outcome)
	assert.NotContains(t, out, "citeck_postgres_default")
}

// The existing-target-volume warning is a STRUCTURED field (PreflightResult.
// ExistingTargetVolume), and the CLI is the only thing that turns it into a
// sentence — through the locale key, once. It used to arrive as an English
// prose warning as well, which rendered the same fact twice, the second time
// untranslated. The producer side of that rule lives in internal/deps/migrate
// (checkTargetVolume sets the field and appends no warning); this pins the
// consumer: one line, from the key, with all three facts in it.
func TestPreflightLines_ExistingVolumeIsRenderedOnceThroughTheLocaleKey(t *testing.T) {
	depsTestSetup(t)
	pre := okPreflight()
	pre.ExistingTargetVolume = &migrate.ExistingVolume{Name: "citeck_pg18", SizeBytes: 1 << 30, Version: "18"}

	joined := strings.Join(preflightLines(pre), "\n")
	assert.Equal(t, 1, strings.Count(joined, "citeck_pg18"), "the volume is named once:\n%s", joined)
	assert.Contains(t, joined, tHelper("deps.preflight.existingVolume",
		"volume", "citeck_pg18", "size", "1.0 GiB", "version", "18"))
	// …and never as the bare lookup key, which is what a missing key renders as.
	assert.NotContains(t, joined, "deps.preflight.existingVolume")
}
