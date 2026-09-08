package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/output"
)

// The deps_migration_* SSE events (see api.EventDto): AppName is the dependency
// id, Phase the step id, Current/Total the step index/count, Percent the step's
// own sub-progress and After a human message. The names are api's, not this
// package's: the daemon broadcasts the same constants, and a private copy here
// is a typo away from a command that waits for a terminal event that never
// matches.
const (
	depsEventPrefix   = api.EventDepsMigrationPrefix
	depsEventStart    = api.EventDepsMigrationStart
	depsEventProgress = api.EventDepsMigrationProgress
	depsEventComplete = api.EventDepsMigrationComplete
	depsEventError    = api.EventDepsMigrationError
)

// depsMigrationTimeout bounds the event stream, not the migration: the daemon
// runs the migration on its own background context and finishes it whether or
// not anyone is listening. It is generous because the dump/restore of a large
// cluster is measured in hours, and giving up on the stream early would report
// a failure the daemon never had.
const depsMigrationTimeout = 6 * time.Hour

// depsPollInterval is how often the follow loop asks the daemon what it thinks,
// instead of trusting that every event reached it. See pollMigrationVerdict.
const depsPollInterval = 15 * time.Second

func newDepsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Show infra dependency versions and run version migrations",
		Long: "Lists PostgreSQL, RabbitMQ, ZooKeeper, Keycloak and MongoDB with the version the\n" +
			"namespace's data runs on and the version the bundle offers. A breaking change is never\n" +
			"applied silently: it is reported here and migrated with `citeck deps upgrade <id>`.",
		RunE: func(_ *cobra.Command, _ []string) error { return runDepsList() },
	}
	cmd.AddCommand(newDepsListCmd(), newDepsUpgradeCmd())
	return cmd
}

func newDepsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List dependencies and available upgrades",
		RunE:  func(_ *cobra.Command, _ []string) error { return runDepsList() },
	}
}

func runDepsList() error {
	ensureI18n()
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()

	dto, err := c.GetDependencies()
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}
	printDependencies(dto)
	return nil
}

// printDependencies renders the dependency list: the DTO verbatim in JSON mode
// (it is the machine contract — the table is a view of it), the table plus its
// notice lines in text mode.
func printDependencies(dto *api.DependenciesDto) {
	output.PrintResult(dto, func() {
		for _, line := range depsListLines(dto) {
			output.PrintText("%s", line)
		}
	})
}

// depsListLines is the text rendering of the dependency list: the table (one
// multi-line entry) followed by everything a migration left behind. The three
// notices are deliberately separate lines rather than table columns — each one
// is a sentence about the namespace, not a fact about a single dependency.
func depsListLines(dto *api.DependenciesDto) []string {
	lines := make([]string, 0, len(dto.Items)+4)
	if len(dto.Items) == 0 {
		lines = append(lines, t("deps.empty"))
	} else {
		rows := make([][]string, 0, len(dto.Items))
		for _, it := range dto.Items {
			rows = append(rows, []string{
				it.ID,
				versionOr(it.CurrentVersion, it.CurrentImage),
				versionOr(it.TargetVersion, it.TargetImage),
				formatDependencyStatus(it),
			})
		}
		lines = append(lines, output.FormatTable([]string{
			t("deps.col.dependency"), t("deps.col.current"), t("deps.col.available"), t("deps.col.status"),
		}, rows))
	}

	// A pending rollback outranks everything else on this screen: the version
	// is frozen until it succeeds and no new migration is accepted.
	if dto.RollbackPending != "" {
		lines = append(lines, output.Colorize(output.Yellow, "! "+dto.RollbackPending))
	}
	if dto.Migration != nil {
		lines = append(lines, migrationRunningLine(dto.Migration))
	}
	if r := dto.LastResult; r != nil {
		lines = append(lines, lastResultLines(r)...)
	}
	return lines
}

// lastResultLines reports the verdict of the last migration. A success names
// the volume the previous data is still in: the launcher never deletes it, so
// that name is the only way for the user to reclaim the space once they trust
// the new version.
func lastResultLines(r *api.DependencyMigrationResultDto) []string {
	if !r.Success {
		return []string{output.Colorize(output.Red, t("deps.lastFailed",
			"id", r.ID, "from", r.From, "to", r.To, "error", r.Error))}
	}
	lines := []string{t("deps.lastSucceeded",
		"id", r.ID, "from", r.From, "to", r.To, "time", formatEpochMillis(r.FinishedAt))}
	if r.OldVolume != "" {
		lines = append(lines, t("deps.oldVolume", "volume", r.OldVolume))
	}
	return lines
}

// versionOr prefers the parsed version and falls back to the image reference,
// so a dependency whose image the launcher cannot parse still shows something
// the user can compare instead of an empty cell.
func versionOr(version, image string) string {
	if version != "" {
		return version
	}
	return image
}

// formatDependencyStatus renders DependencyDto.Status for the table. An
// unknown status reads as "up to date": a newer daemon's extra state is not
// something this launcher can act on, and inventing an upgrade prompt for it
// would send the user after a command that does nothing.
func formatDependencyStatus(it api.DependencyDto) string {
	switch it.Status {
	case api.DependencyPendingMinor:
		return t("deps.status.pendingMinor", "version", versionOr(it.TargetVersion, it.TargetImage))
	case api.DependencyUpgradeAvailable:
		return t("deps.status.upgradeAvailable", "id", it.ID)
	case api.DependencyRequiresLauncherUpdate:
		return t("deps.status.requiresLauncherUpdate")
	default:
		return t("deps.status.upToDate")
	}
}

// dependencyHintLine is the one line `citeck status` prints about dependency
// versions. Priority is urgency, not recency: a pending rollback is a stand
// whose version is frozen and whose leftovers are still on the host, a running
// migration explains why the namespace is down, and only then does an
// available upgrade — which waits patiently — get a mention. Returns "" when
// there is nothing to say. Both arguments are optional: `citeck status` fetches
// the dependency list best-effort, exactly as it does the license status.
func dependencyHintLine(ns *api.NamespaceDto, dto *api.DependenciesDto) string {
	if dto != nil && dto.RollbackPending != "" {
		return t("deps.rollbackHint", "message", dto.RollbackPending)
	}
	if m := runningMigration(ns, dto); m != nil {
		return migrationRunningLine(m)
	}
	if ns == nil || len(ns.DependencyUpgrades) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ns.DependencyUpgrades))
	for _, u := range ns.DependencyUpgrades {
		parts = append(parts, fmt.Sprintf("%s %s → %s", u.ID, u.From, u.To))
	}
	return t("deps.statusHint", "list", strings.Join(parts, ", "))
}

// runningMigration answers the migration in progress from whichever DTO the
// caller has. Both carry it, and both are namespace-scoped by the daemon.
func runningMigration(ns *api.NamespaceDto, dto *api.DependenciesDto) *api.DependencyMigrationDto {
	if ns != nil && ns.DependencyMigration != nil {
		return ns.DependencyMigration
	}
	if dto != nil {
		return dto.Migration
	}
	return nil
}

func migrationRunningLine(m *api.DependencyMigrationDto) string {
	return t("deps.migrationRunning", "id", m.ID, "step", stepTitle(m.Step),
		"index", strconv.Itoa(m.StepIndex), "total", strconv.Itoa(m.StepCount))
}

// stepTitle translates a migration step id. A step this launcher has no key
// for (a newer daemon's plan) renders as the raw id — never as the bare
// "deps.step.<id>" lookup key, which is what i18n.T would return.
func stepTitle(id string) string {
	if id == "" {
		return ""
	}
	if key := "deps.step." + id; i18n.HasKey(key) {
		return t(key)
	}
	return id
}

// renderMigrationEvent turns one deps_migration_* event into a CLI line and
// says whether it ends the migration. Terminal+failed is the ONLY signal the
// command has that the migration failed: by the time the error event is sent
// the rollback has already run, so After is the reason, not a warning.
func renderMigrationEvent(evt api.EventDto) (line string, terminal, failed bool) {
	switch evt.Type {
	case depsEventStart:
		return evt.After, false, false
	case depsEventProgress:
		// Total 0 means there is no plan yet — the "preparing" state the daemon
		// publishes while it builds one. "[0/0]" would read as a counter that
		// has gone wrong; the step's own title is the whole message.
		if evt.Total == 0 {
			line = stepTitle(evt.Phase)
		} else {
			line = fmt.Sprintf("[%d/%d] %s", evt.Current, evt.Total, stepTitle(evt.Phase))
		}
		// Percent 0 means "indeterminate" (the step has no measurable
		// sub-progress), which is not the same as "0% done".
		if evt.Percent > 0 {
			line += fmt.Sprintf(" %.0f%%", evt.Percent)
		}
		if evt.After != "" {
			line += " — " + evt.After
		}
		return line, false, false
	case depsEventComplete:
		return evt.After, true, false
	case depsEventError:
		return evt.After, true, true
	default:
		return "", false, false
	}
}

// isMigrationEventFor selects the events of ONE dependency's migration off the
// shared stream. Both halves matter: the stream carries every app-status and
// pull event of the namespace being stopped and started, and AppName is the
// dependency id, so a second migration's events would otherwise be read as
// this one's — including its terminal event, which ends the wait.
func isMigrationEventFor(evt api.EventDto, id string) bool {
	return evt.AppName == id && strings.HasPrefix(evt.Type, depsEventPrefix)
}

// formatEpochMillis renders an epoch-millisecond timestamp in local time. A
// zero (or negative) timestamp renders as nothing rather than as 1970 — the
// field is optional on the wire and a 1970 date reads as corruption.
func formatEpochMillis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func newDepsUpgradeCmd() *cobra.Command {
	var detach, replaceExisting bool
	cmd := &cobra.Command{
		Use:   "upgrade <dependency>",
		Short: "Migrate a dependency's data to the version the bundle offers (e.g. PostgreSQL 17 → 18)",
		Long: "Stops the namespace, dumps the data, builds a new cluster in a NEW volume, restores and\n" +
			"verifies it, then switches the namespace over. The old volume is left untouched; any failure\n" +
			"rolls back to it. Use --replace-existing to allow deleting a leftover target volume.\n\n" +
			"The migration runs in the daemon: interrupting this command stops the progress output, not\n" +
			"the migration. `citeck deps` reports what happened.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runDepsUpgrade(args[0], detach, replaceExisting)
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Start the migration and return immediately")
	cmd.Flags().BoolVar(&replaceExisting, "replace-existing", false,
		"Allow deleting an existing target volume (see the preflight warning)")
	return cmd
}

func runDepsUpgrade(id string, detach, replaceExisting bool) error {
	ensureI18n()
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()

	// A pending rollback is refused here rather than after the preflight: it
	// freezes the pin and the daemon will not accept a new migration either
	// way, and the preflight walks the whole data volume before it can say so.
	list, err := c.GetDependencies()
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}
	if list.RollbackPending != "" {
		return errors.New(list.RollbackPending)
	}

	pre, err := c.DependencyPreflight(id)
	if err != nil {
		return fmt.Errorf("preflight %s: %w", id, err)
	}
	for _, line := range preflightLines(pre) {
		output.PrintText("%s", line)
	}
	if !pre.OK {
		return errors.New(t("deps.preflightFailed"))
	}
	// An existing target volume is data this launcher did not create. Deleting
	// it is a separate decision from "migrate", so it is a refusal naming the
	// flag, not one more prompt inside the confirmation.
	if pre.ExistingTargetVolume != nil && !replaceExisting {
		return errors.New(t("deps.existingVolumeRefused", "volume", pre.ExistingTargetVolume.Name))
	}
	if !confirmMigration(id, pre) {
		output.PrintText("%s", t("deps.cancelled"))
		return nil
	}

	if detach {
		res, startErr := startMigration(c, id, replaceExisting)
		if startErr != nil {
			return startErr
		}
		output.PrintText("%s", res.Message)
		return nil
	}
	return migrateAndFollow(c, id, replaceExisting)
}

// confirmMigration asks for the go-ahead. The default is YES because under the
// global --yes promptConfirm returns its default without asking: a "no" default
// would turn `citeck deps upgrade postgres --yes` — the scripted spelling of
// "do not ask me" — into a silent cancellation.
func confirmMigration(id string, pre *migrate.PreflightResult) bool {
	return promptConfirm(t("deps.confirm", "id", id, "from", pre.From, "to", pre.To), true)
}

// preflightLines renders the preflight for a human: what moves where, the two
// filesystems it needs room on, and every warning and problem it found. It is
// printed whether or not the checks passed — a refusal the user cannot see the
// reason for is worse than no check at all.
func preflightLines(pre *migrate.PreflightResult) []string {
	lines := []string{t("deps.preflight.title", "from", pre.From, "to", pre.To)}
	// A preflight the daemon refused before it touched Docker measured
	// nothing, and its zeros are not facts: printing them says the namespace
	// holds no data and the host has no free space, above the line that gives
	// the actual reason.
	if pre.Measured() {
		lines = append(lines,
			"  "+t("deps.preflight.data", "size", fsutil.FormatBytes(pre.DataSizeBytes)),
			"  "+t("deps.preflight.host",
				"need", fsutil.FormatBytes(pre.RequiredHostBytes), "free", fsutil.FormatBytes(pre.FreeHostBytes)),
			"  "+t("deps.preflight.volume",
				"need", fsutil.FormatBytes(pre.RequiredVolumeBytes), "free", fsutil.FormatBytes(pre.FreeVolumeBytes)),
		)
	}
	if pre.WasRunning {
		lines = append(lines, "  "+t("deps.preflight.willStop"))
	}
	if v := pre.ExistingTargetVolume; v != nil {
		lines = append(lines, "  "+output.Colorize(output.Yellow, "!")+" "+t("deps.preflight.existingVolume",
			"volume", v.Name, "size", fsutil.FormatBytes(v.SizeBytes), "version", v.Version))
	}
	for _, w := range pre.Warnings {
		lines = append(lines, "  "+output.Colorize(output.Yellow, "!")+" "+w)
	}
	for _, p := range pre.Problems {
		lines = append(lines, "  "+output.Colorize(output.Red, "x")+" "+p)
	}
	return lines
}

// startMigration posts the request and checks the daemon accepted it. A
// refusal (400/409 with a code) is already an error from the client; a 202
// that somehow reports failure is one too, because nothing would follow it.
func startMigration(c *client.DaemonClient, id string, replaceExisting bool) (*api.ActionResultDto, error) {
	res, err := c.MigrateDependency(id, replaceExisting)
	if err != nil {
		return nil, fmt.Errorf("start migration of %s: %w", id, err)
	}
	if !res.Success {
		return nil, errors.New(res.Message)
	}
	return res, nil
}

// migrateAndFollow subscribes to the event stream BEFORE asking for the
// migration — the daemon broadcasts the start event as soon as it has answered
// 202, and a subscription opened afterwards would miss it (the same rule
// snapshotAndWait follows). The daemon's answer is checked before anything is
// awaited, so a refusal exits immediately instead of waiting for events that
// will never come.
func migrateAndFollow(c *client.DaemonClient, id string, replaceExisting bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), depsMigrationTimeout)
	defer cancel()

	events, err := c.StreamEvents(ctx)
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}

	clickedAt := time.Now()
	res, err := startMigration(c, id, replaceExisting)
	if err != nil {
		return err
	}
	output.PrintText("%s", res.Message)

	poll := time.NewTicker(depsPollInterval)
	defer poll.Stop()
	for {
		select {
		case evt, open := <-events:
			if !open {
				return errors.New(t("deps.streamClosed"))
			}
			if !isMigrationEventFor(evt, id) {
				continue
			}
			line, terminal, failed := renderMigrationEvent(evt)
			if line != "" {
				output.PrintText("%s", line)
			}
			if terminal {
				if failed {
					return errors.New(t("deps.failed", "error", evt.After))
				}
				return nil
			}
		case <-poll.C:
			if done, verdict := pollMigrationVerdict(c.GetDependencies, id, clickedAt); done {
				return verdict
			}
		case <-ctx.Done():
			return errors.New(t("deps.streamClosed"))
		}
	}
}

// pollMigrationVerdict is the safety net under the event stream. The daemon
// DROPS an event rather than block when a subscriber's channel is full
// (internal/daemon/sse.go), and a migration's own stop/start burst — every app
// of the namespace going down and coming back — is the most likely thing in
// the launcher to fill it. A dropped terminal event used to leave this command
// waiting up to depsMigrationTimeout (six hours) for a migration that had
// finished minutes earlier, with no output and no way to tell.
//
// done=true means stop following; verdict is the error to report, or nil for a
// success. The two conditions are both required: the daemon must say nothing
// is running any more AND the recorded result must be NEWER than this click,
// because a verdict left behind by a previous migration of the same dependency
// would otherwise be reported as this one's. Anything the poll cannot answer
// (a failed request, a result that is not ours) simply keeps the loop going —
// the events are still the primary source.
func pollMigrationVerdict(fetch func() (*api.DependenciesDto, error), id string, clickedAt time.Time) (done bool, verdict error) {
	dto, err := fetch()
	if err != nil || dto == nil || dto.Migration != nil {
		return false, nil
	}
	r := dto.LastResult
	if r == nil || r.ID != id || r.FinishedAt < clickedAt.UnixMilli() {
		return false, nil
	}
	for _, line := range lastResultLines(r) {
		output.PrintText("%s", line)
	}
	if !r.Success {
		return true, errors.New(t("deps.failed", "error", r.Error))
	}
	return true, nil
}
