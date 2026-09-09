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
	"github.com/citeck/citeck-launcher/internal/deps"
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
			"applied silently: it is reported here and migrated with `citeck deps upgrade <id>`.\n\n" +
			"`citeck deps rollback <id>` puts a dependency back on the version it ran on before its\n" +
			"last migration, on the volume that migration left.",
		RunE: func(_ *cobra.Command, _ []string) error { return runDepsList() },
	}
	cmd.AddCommand(newDepsListCmd(), newDepsUpgradeCmd(), newDepsRollbackCmd())
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
// multi-line entry), then the sentences a status could not fit, then what a
// migration left behind. Every one of those is a separate line rather than a
// table column, and for the same reason: a column is padded to its widest
// value, so one paragraph in a cell would push every other row off the screen.
func depsListLines(dto *api.DependenciesDto) []string {
	lines := make([]string, 0, 2*len(dto.Items)+5)
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

	lines = append(lines, statusDetailLines(dto)...)
	if offer := rollbackAvailableLine(dto); offer != "" {
		lines = append(lines, offer)
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

// statusDetailLines renders the sentence behind a status a fixed label cannot
// explain — a vendor-forbidden hop naming the intermediate version, a bundle
// offering something older than the data.
//
// It is VERBATIM: the daemon builds these in internal/deps/migrate, where
// every other migration sentence lives, and re-wording them here would be a
// second account of one refusal. It is below the TABLE and not in the status
// cell because a column is padded to its widest value, and these run to
// several hundred characters — one blocked row would push every other row's
// status past the edge of the terminal. Each line names its dependency, or two
// of them would be two anonymous paragraphs.
func statusDetailLines(dto *api.DependenciesDto) []string {
	lines := make([]string, 0, len(dto.Items))
	for _, it := range dto.Items {
		if it.StatusDetail == "" {
			continue
		}
		lines = append(lines, t("deps.statusDetail", "id", it.ID, "detail", it.StatusDetail))
	}
	return lines
}

// rollbackAvailableLine advertises the rollbacks this namespace could take,
// or "" when there are none. Without it the action is invisible: nothing else
// in the CLI mentions it, and a namespace that migrated last week has no other
// way to learn it can go back.
//
// An offer with Available=false is deliberately NOT reported. The one reason
// it happens in practice is a retained volume the operator deleted — which is
// what `deps.oldVolume` told them to do after the migration — so reporting it
// on every list would be the launcher arguing with its own advice. The reason
// is still there for anyone who runs `citeck deps rollback`, where the
// preflight names the volume.
func rollbackAvailableLine(dto *api.DependenciesDto) string {
	offers := make([]string, 0, len(dto.Items))
	for _, it := range dto.Items {
		if it.Rollback == nil || !it.Rollback.Available {
			continue
		}
		offers = append(offers, fmt.Sprintf("%s → %s", it.ID, versionOr(it.Rollback.ToVersion, it.Rollback.ToImage)))
	}
	if len(offers) == 0 {
		return ""
	}
	return t("deps.rollbackAvailable", "list", strings.Join(offers, ", "))
}

// lastResultLines reports the verdict of the last migration OR rollback — a
// namespace has one result slot and the two share it, which is why every line
// here is chosen by Kind. "postgres:18.6 → postgres:17.5 succeeded" reported as
// a migration would claim the launcher moved data backwards, which is the one
// thing it will never do.
//
// A success names the volume the data it is NOT using is in: the launcher
// never deletes either one. For a migration that is the previous data, and the
// advice is to reclaim it once the new version is trusted; for a rollback it is
// everything written since the migration, which the launcher keeps and never
// reads again — the same volume role, the opposite advice.
func lastResultLines(r *api.DependencyMigrationResultDto) []string {
	rollback := r.Kind == deps.ResultKindRollback
	if !r.Success {
		// A rollback failure is REPORTED and not recorded today (the result
		// slot still holds the migration being undone, and dating a live offer
		// by a failure that changed nothing would be worse than silence), so
		// this arm answers a newer daemon rather than anything shipping now.
		key := "deps.lastFailed"
		if rollback {
			key = "deps.rollback.failed"
		}
		return []string{output.Colorize(output.Red, t(key,
			"id", r.ID, "from", r.From, "to", r.To, "error", r.Error))}
	}
	key, volumeKey := "deps.lastSucceeded", "deps.oldVolume"
	if rollback {
		key, volumeKey = "deps.rollback.done", "deps.rollback.frozenVolume"
	}
	lines := []string{t(key,
		"id", r.ID, "from", r.From, "to", r.To, "time", formatEpochMillis(r.FinishedAt))}
	if r.OldVolume != "" {
		lines = append(lines, t(volumeKey, "volume", r.OldVolume))
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
//
// The two held-back statuses the daemon explains in prose (upgrade-blocked and
// bundle-older) render a SHORT label here and their sentence below the table
// (statusDetailLines). A table cell is padded to the width of the longest
// value in its column, so a 300-character vendor refusal in one row would push
// every other row's status off the screen — and StatusDetail is exactly that
// long. Neither label borrows an upgrade's words: "requires a newer launcher"
// is false for both, since no launcher lifts a vendor refusal and none moves
// data backwards.
func formatDependencyStatus(it api.DependencyDto) string {
	switch it.Status {
	case api.DependencyPendingMinor:
		return t("deps.status.pendingMinor", "version", versionOr(it.TargetVersion, it.TargetImage))
	case api.DependencyUpgradeAvailable:
		return t("deps.status.upgradeAvailable", "id", it.ID)
	case api.DependencyRequiresLauncherUpdate:
		return t("deps.status.requiresLauncherUpdate")
	case api.DependencyUpgradeBlocked:
		return t("deps.status.blocked")
	case api.DependencyBundleOlder:
		return t("deps.status.bundleOlder", "version", versionOr(it.CurrentVersion, it.CurrentImage))
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
	// A backwards hold is NOT an upgrade: there is nothing to migrate and
	// nothing to wait for, and folded into "Upgrades available" it would send
	// the operator to `citeck deps upgrade`, which refuses it (409
	// DEPENDENCY_BACKWARDS). It still gets a clause of its own rather than
	// being dropped, so an operator who never opens `citeck deps` still learns
	// the bundle is offering something older than their data.
	var upgrades, older []string
	for _, u := range ns.DependencyUpgrades {
		entry := fmt.Sprintf("%s %s → %s", u.ID, u.From, u.To)
		if u.BundleOlder {
			older = append(older, entry)
			continue
		}
		upgrades = append(upgrades, entry)
	}
	clauses := make([]string, 0, 2)
	if len(upgrades) > 0 {
		clauses = append(clauses, t("deps.statusHint", "list", strings.Join(upgrades, ", ")))
	}
	if len(older) > 0 {
		clauses = append(clauses, t("deps.bundleOlderHint", "list", strings.Join(older, ", ")))
	}
	if len(clauses) == 0 {
		return ""
	}
	// One line, because `citeck status` prints it after a "Deps:" label — and
	// the "see `citeck deps`" pointer belongs to the line, not to each clause,
	// or a namespace with both conditions would be told twice where to look.
	return t("deps.hintLine", "hints", strings.Join(clauses, "; "))
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

// migrationRunningLine reports the operation running on the shared progress
// channel. Kind is what tells the two apart: a rollback announced as a
// migration sends the operator looking for one they did not start.
func migrationRunningLine(m *api.DependencyMigrationDto) string {
	key := "deps.migrationRunning"
	if m.Kind == deps.ResultKindRollback {
		key = "deps.rollbackRunning"
	}
	return t(key, "id", m.ID, "step", stepTitle(m.Step),
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
		Long: "Stops the namespace and moves the data into a NEW volume, leaving the old one untouched;\n" +
			"any failure rolls back to it. PostgreSQL is dumped and restored into the new cluster;\n" +
			"RabbitMQ and ZooKeeper have their data volume copied and the COPY upgraded, so the original\n" +
			"is only ever read. Either way the result is verified before the namespace switches over.\n" +
			"Use --replace-existing to allow deleting a leftover target volume.\n\n" +
			"The migration runs in the daemon: interrupting this command stops the progress output, not\n" +
			"the migration. `citeck deps` reports what happened.\n\n" +
			"With --format json nothing is printed until it is over: the whole answer is one object with\n" +
			"the outcome (refused / canceled / started / migrated / failed / unknown) and the preflight.",
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

// depsAPI is the daemon surface `citeck deps upgrade` and `citeck deps
// rollback` drive. The commands are built from a package-global client, so
// without this seam their orchestration — which refusal comes before which
// request, what the follow loop makes of an event, what the verdict and the
// exit code are — could only be exercised against a live daemon, i.e. never.
type depsAPI interface {
	GetDependencies() (*api.DependenciesDto, error)
	DependencyPreflight(id string) (*migrate.PreflightResult, error)
	MigrateDependency(id string, replaceExisting bool) (*api.ActionResultDto, error)
	DependencyRollbackPreflight(id string) (*migrate.PreflightResult, error)
	RollbackDependency(id string) (*api.ActionResultDto, error)
	StreamEvents(ctx context.Context) (<-chan api.EventDto, error)
}

// depsAction is everything that differs between the two operations inside the
// shared confirm → start → follow orchestration: what to POST, which of the
// two verdicts in the namespace's ONE result slot belongs to it, and what a
// success is called. Everything else — the pending-rollback refusal, the
// preflight, the confirmation, the event stream, the poll, the JSON verdict —
// is the same code, which is what makes "the rollback needs no rendering path
// of its own" true rather than aspirational.
type depsAction struct {
	// kind is deps.ResultKindMigration or deps.ResultKindRollback: the
	// discriminator the poll uses, because a namespace has one result slot and
	// the other operation's verdict must not end this one's follow.
	kind string
	// success is the outcome reported in --format json. A rollback that
	// answered "migrated" would tell a script the launcher moved data
	// backwards, which is the one thing it will never do.
	success string
	start   func(c depsAPI, id string) (*api.ActionResultDto, error)
}

func migrateAction(replaceExisting bool) depsAction {
	return depsAction{
		kind: deps.ResultKindMigration, success: depsOutcomeMigrated,
		start: func(c depsAPI, id string) (*api.ActionResultDto, error) {
			return c.MigrateDependency(id, replaceExisting)
		},
	}
}

func rollbackAction() depsAction {
	return depsAction{
		kind: deps.ResultKindRollback, success: depsOutcomeRolledBack,
		start: func(c depsAPI, id string) (*api.ActionResultDto, error) {
			return c.RollbackDependency(id)
		},
	}
}

// depsFollow are the two durations the follow loop is built on, kept together
// so a test can drive it in milliseconds instead of 15s/6h.
type depsFollow struct {
	poll    time.Duration
	timeout time.Duration
}

// depsActionOpts is one invocation of either command. confirm is injected for
// the same reason the client is: promptConfirm needs a terminal, and "the user
// said no" is a branch that must not start anything. replaceExisting is unused
// by the rollback, which creates no volume.
type depsActionOpts struct {
	detach          bool
	replaceExisting bool
	confirm         func(id string, pre *migrate.PreflightResult) bool
	follow          depsFollow
}

func defaultDepsActionOpts(detach, replaceExisting bool, confirm func(string, *migrate.PreflightResult) bool) depsActionOpts {
	return depsActionOpts{
		detach:          detach,
		replaceExisting: replaceExisting,
		confirm:         confirm,
		follow:          depsFollow{poll: depsPollInterval, timeout: depsMigrationTimeout},
	}
}

// The outcomes `citeck deps upgrade --format json` reports. They answer one
// question — what happened to the DATA — and they are deliberately distinct
// where a single "ok/error" would mislead: `refused` means nothing was
// started, so nothing was touched; `failed` means it ran, failed and was
// rolled back; and `unknown` means this command lost sight of a migration that
// is very probably still running, since the daemon runs it on its own context
// and finishes it whether or not anyone is listening. Collapsing that last one
// into `failed` would tell a script the opposite of what happened.
const (
	depsOutcomeRefused  = "refused"
	depsOutcomeCanceled = "canceled"
	depsOutcomeStarted  = "started"
	depsOutcomeMigrated = "migrated"
	// depsOutcomeRolledBack is the rollback's success. It is NOT "migrated":
	// the two share every line of the orchestration, but a script told a
	// dependency was "migrated" when its data went back a generation has been
	// told the opposite of what happened.
	depsOutcomeRolledBack = "rolled-back"
	depsOutcomeFailed     = "failed"
	depsOutcomeUnknown    = "unknown"
)

// depsActionReport is the whole of stdout under `--format json`, for both the
// upgrade and the rollback — one shape, so a script needs one parser. Every field
// is filled from something every path has: the preflight (which is also what
// carries the reason for a refusal) and the outcome. Nothing that only ONE
// path could fill belongs here — a shape that changes with the route taken is
// not a machine contract.
type depsActionReport struct {
	ID        string                   `json:"id"`
	From      string                   `json:"from,omitempty"`
	To        string                   `json:"to,omitempty"`
	Outcome   string                   `json:"outcome"`
	Error     string                   `json:"error,omitempty"`
	Message   string                   `json:"message,omitempty"`
	Preflight *migrate.PreflightResult `json:"preflight,omitempty"`
}

func runDepsUpgrade(id string, detach, replaceExisting bool) error {
	ensureI18n()
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()
	return depsUpgrade(c, id, defaultDepsActionOpts(detach, replaceExisting, confirmMigration))
}

// depsUpgrade runs the command and prints its answer. The error is returned
// unchanged for the exit code AND copied into the report, because in JSON mode
// Execute() prints nothing to stderr: a report without the reason would leave
// a script with an exit code and no way to learn what happened.
func depsUpgrade(c depsAPI, id string, opts depsActionOpts) error {
	report, err := depsUpgradeSteps(c, id, opts)
	if err != nil {
		report.Error = err.Error()
	}
	if output.IsJSON() {
		output.PrintJSON(report)
	}
	return err
}

// depsUpgradeSteps is the orchestration itself: the two refusals that come
// before any work, the confirmation, and then either the detached start or the
// follow. It always returns a report — a refusal is an answer too.
func depsUpgradeSteps(c depsAPI, id string, opts depsActionOpts) (*depsActionReport, error) {
	report := &depsActionReport{ID: id, Outcome: depsOutcomeRefused}

	// A pending rollback is refused here rather than after the preflight: it
	// freezes the pin and the daemon will not accept a new migration either
	// way, and the preflight walks the whole data volume before it can say so.
	list, err := c.GetDependencies()
	if err != nil {
		return report, fmt.Errorf("list dependencies: %w", err)
	}
	if list.RollbackPending != "" {
		return report, errors.New(list.RollbackPending)
	}

	pre, err := c.DependencyPreflight(id)
	if err != nil {
		return report, fmt.Errorf("preflight %s: %w", id, err)
	}
	report.Preflight, report.From, report.To = pre, pre.From, pre.To
	for _, line := range preflightLines(pre) {
		sayProgress(line)
	}
	if !pre.OK {
		return report, errors.New(t("deps.preflightFailed"))
	}
	// An existing target volume is data this launcher did not create. Deleting
	// it is a separate decision from "migrate", so it is a refusal naming the
	// flag, not one more prompt inside the confirmation.
	if pre.ExistingTargetVolume != nil && !opts.replaceExisting {
		return report, errors.New(t("deps.existingVolumeRefused", "volume", pre.ExistingTargetVolume.Name))
	}
	if !opts.confirm(id, pre) {
		report.Outcome = depsOutcomeCanceled
		sayProgress(t("deps.cancelled"))
		return report, nil
	}

	return startAndFollow(c, id, report, opts, migrateAction(opts.replaceExisting))
}

// startAndFollow is the tail both commands share: either dispatch and return,
// or dispatch and watch. It is the only place either of them starts anything,
// so "the daemon answered, therefore the outcome is no longer `refused`" is one
// rule rather than two.
func startAndFollow(c depsAPI, id string, report *depsActionReport,
	opts depsActionOpts, act depsAction,
) (*depsActionReport, error) {
	if opts.detach {
		res, startErr := startAction(c, id, act)
		if startErr != nil {
			return report, startErr
		}
		report.Outcome, report.Message = depsOutcomeStarted, res.Message
		sayProgress(res.Message)
		return report, nil
	}
	return runAndFollow(c, id, report, opts, act)
}

func newDepsRollbackCmd() *cobra.Command {
	var detach bool
	cmd := &cobra.Command{
		Use:   "rollback <dependency>",
		Short: "Put a dependency back on the version it ran on before its last migration",
		Long: "Stops the namespace, points the dependency back at the image AND the data volume it ran\n" +
			"on before its last completed migration, and starts the namespace again. Nothing is created\n" +
			"and nothing is deleted: the volume the namespace is leaving is KEPT and never read again,\n" +
			"so everything written since that migration becomes unreachable. There is no roll-forward.\n\n" +
			"It is offered only where this launcher performed the migration and its volume is still on\n" +
			"the host — `citeck deps` says when there is one.\n\n" +
			"The rollback runs in the daemon: interrupting this command stops the progress output, not\n" +
			"the rollback. `citeck deps` reports what happened.\n\n" +
			"With --format json nothing is printed until it is over: the whole answer is one object with\n" +
			"the outcome (refused / canceled / started / rolled-back / failed / unknown) and the preflight.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runDepsRollback(args[0], detach)
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Start the rollback and return immediately")
	return cmd
}

func runDepsRollback(id string, detach bool) error {
	ensureI18n()
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()
	// replaceExisting is false and stays false: a rollback creates no volume,
	// so there is nothing it could be asked to replace.
	return depsRollback(c, id, defaultDepsActionOpts(detach, false, confirmRollback))
}

// depsRollback runs the command and prints its answer, on the same rules as
// depsUpgrade: the error is returned unchanged for the exit code AND copied
// into the report, because in JSON mode Execute() prints nothing to stderr.
func depsRollback(c depsAPI, id string, opts depsActionOpts) error {
	report, err := depsRollbackSteps(c, id, opts)
	if err != nil {
		report.Error = err.Error()
	}
	if output.IsJSON() {
		output.PrintJSON(report)
	}
	return err
}

// depsRollbackSteps is the rollback's orchestration. It is deliberately the
// same shape as depsUpgradeSteps — the pending-rollback refusal first, then the
// preflight, then the confirmation, then dispatch — and differs in exactly
// three places: the preflight it asks for, the screen it renders, and the fact
// that there is no existing-target-volume decision, because a rollback creates
// no volume.
//
// The dependency LIST is fetched for two reasons, not one: RollbackPending is
// the refusal that must come before any Docker work, and the row's offer
// carries the date of the migration being undone, which the preflight
// deliberately does not.
func depsRollbackSteps(c depsAPI, id string, opts depsActionOpts) (*depsActionReport, error) {
	report := &depsActionReport{ID: id, Outcome: depsOutcomeRefused}

	list, err := c.GetDependencies()
	if err != nil {
		return report, fmt.Errorf("list dependencies: %w", err)
	}
	// An interrupted migration whose journal is still open freezes the pin and
	// the daemon refuses everything — including this. Refusing here spares the
	// preflight's volume read (a utils container on a desktop).
	if list.RollbackPending != "" {
		return report, errors.New(list.RollbackPending)
	}

	pre, err := c.DependencyRollbackPreflight(id)
	if err != nil {
		return report, fmt.Errorf("rollback preflight %s: %w", id, err)
	}
	report.Preflight, report.From, report.To = pre, pre.From, pre.To
	for _, line := range rollbackPreflightLines(pre, rollbackOfferFor(list, id)) {
		sayProgress(line)
	}
	if !pre.OK {
		return report, errors.New(t("deps.preflightFailed"))
	}
	if !opts.confirm(id, pre) {
		report.Outcome = depsOutcomeCanceled
		sayProgress(t("deps.cancelled"))
		return report, nil
	}
	return startAndFollow(c, id, report, opts, rollbackAction())
}

// rollbackOfferFor is the offer on one dependency's row, or nil. A nil answer
// is not an error here: the preflight is the authority on whether a rollback
// may run, and it refuses a missing target in its own words.
func rollbackOfferFor(list *api.DependenciesDto, id string) *api.DependencyRollbackDto {
	if list == nil {
		return nil
	}
	for _, it := range list.Items {
		if it.ID == id {
			return it.Rollback
		}
	}
	return nil
}

// confirmRollback asks for the go-ahead. The default is YES for the same
// reason confirmMigration's is: under the global --yes promptConfirm returns
// its default without asking, and a "no" default would turn the scripted
// spelling of "do not ask me" into a silent cancellation.
func confirmRollback(id string, pre *migrate.PreflightResult) bool {
	return promptConfirm(t("deps.rollback.confirm", "id", id, "to", pre.To), true)
}

// sayProgress prints one line of the command's running commentary. JSON mode
// prints nothing until the end: the machine contract is the single verdict
// object, and prose interleaved with it is neither valid JSON nor readable
// prose.
func sayProgress(line string) {
	if line == "" || output.IsJSON() {
		return
	}
	output.PrintText("%s", line)
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
	return preflightLinesTitled(t("deps.preflight.title", "from", pre.From, "to", pre.To), pre)
}

// rollbackPreflightLines is the same block under the rollback's own title —
// "Migration 18.6 → 17.5" would describe the one thing this launcher never
// does to data — plus the date of the migration being undone.
//
// The three consequences the confirmation exists for (the data is as of the
// migration, everything since then is on a volume that is kept and never read
// again, and there is no roll-forward) are NOT built here: the daemon's
// preflight already carries them as warnings, in the same English every other
// migration sentence is written in, and the block above prints every warning.
// Rendering them a second time from locale keys would state each fact twice —
// the defect TestPreflightLines_ExistingVolumeIsRenderedOnceThroughTheLocaleKey
// pins from the other direction.
//
// The DATE is the exception, and deliberately so: migrate.warnRollbackConsequences
// has no result record to read it from and says as much, so it reaches the
// operator through the offer. offer may be nil (the preflight is about to
// refuse) and its MigratedAt may be 0 (the namespace's one result slot has
// moved on) — both render as nothing, never as 1970.
func rollbackPreflightLines(pre *migrate.PreflightResult, offer *api.DependencyRollbackDto) []string {
	lines := []string{t("deps.rollback.title", "from", pre.From, "to", pre.To)}
	if offer != nil {
		if at := formatEpochMillis(offer.MigratedAt); at != "" {
			lines = append(lines, "  "+t("deps.rollback.migratedAt", "time", at))
		}
	}
	return append(lines, preflightLinesTitled("", pre)...)
}

// preflightLinesTitled is the body both renderings share. An empty title emits
// no title line, so a caller that has already written its own header does not
// get a blank one.
func preflightLinesTitled(title string, pre *migrate.PreflightResult) []string {
	var lines []string
	if title != "" {
		lines = append(lines, title)
	}
	// A preflight the daemon refused before it touched Docker measured
	// nothing, and its zeros are not facts: printing them says the namespace
	// holds no data and the host has no free space, above the line that gives
	// the actual reason.
	if pre.Measured() {
		lines = append(lines, "  "+t("deps.preflight.data", "size", fsutil.FormatBytes(pre.DataSizeBytes)))
		lines = append(lines, spaceRequirementLines(pre)...)
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

// spaceRequirementLines renders the free-space requirement. On a SERVER — the
// ordinary case — the dump directory and the data volumes are ONE filesystem,
// and what has to fit on it is the two halves ADDED UP: the scratch dump is
// removed only after the commit, and the new cluster is built next to the old
// data. Printed as the two independent lines, each half looks satisfiable on
// its own while the daemon refuses the migration for wanting them together, so
// the confirm screen would quote two requirements and then be contradicted by
// a third number in the refusal. The shared case is therefore ONE line
// INSTEAD of the pair, not one more line beside it. Same rule as the web
// dialog (web/src/components/DependenciesDialog.tsx).
func spaceRequirementLines(pre *migrate.PreflightResult) []string {
	if pre.SharedFilesystem {
		return []string{"  " + t("deps.preflight.shared",
			"need", fsutil.FormatBytes(pre.RequiredTotalBytes),
			"free", fsutil.FormatBytes(smallerFreeBytes(pre)))}
	}
	lines := make([]string, 0, 2)
	// A COPY-upgrade plan writes no host file at all — it copies a volume — so
	// its host requirement is legitimately zero, and the line would read as
	// "this needs no space and the disk is empty" beside a volume line that
	// carries the whole requirement. The discriminator is the REQUIREMENT and
	// not the free space: a measured filesystem with genuinely nothing free is
	// a problem the preflight has already reported in its own words.
	if pre.RequiredHostBytes > 0 {
		lines = append(lines, "  "+t("deps.preflight.host",
			"need", fsutil.FormatBytes(pre.RequiredHostBytes), "free", fsutil.FormatBytes(pre.FreeHostBytes)))
	}
	return append(lines, "  "+t("deps.preflight.volume",
		"need", fsutil.FormatBytes(pre.RequiredVolumeBytes), "free", fsutil.FormatBytes(pre.FreeVolumeBytes)))
}

// smallerFreeBytes is the free number the shared line quotes: the smaller of
// the two measurements, SKIPPING a zero. A zero is not "no space left" — it is
// a measurement that FAILED, which has already produced its own problem line;
// taking it as the minimum would render a failed probe as a full disk.
func smallerFreeBytes(pre *migrate.PreflightResult) int64 {
	smallest := int64(0)
	for _, free := range []int64{pre.FreeHostBytes, pre.FreeVolumeBytes} {
		if free > 0 && (smallest == 0 || free < smallest) {
			smallest = free
		}
	}
	return smallest
}

// startAction posts the request and checks the daemon accepted it. A refusal
// (400/409 with a code) is already an error from the client; a 202 that somehow
// reports failure is one too, because nothing would follow it.
func startAction(c depsAPI, id string, act depsAction) (*api.ActionResultDto, error) {
	res, err := act.start(c, id)
	if err != nil {
		return nil, fmt.Errorf("start %s of %s: %w", actionNoun(act), id, err)
	}
	if !res.Success {
		return nil, errors.New(res.Message)
	}
	return res, nil
}

// actionNoun is what an error calls the operation. It is derived from the same
// Kind the poll discriminates on, so the two cannot disagree about which of the
// two this run is.
func actionNoun(act depsAction) string {
	if act.kind == deps.ResultKindRollback {
		return "rollback"
	}
	return "migration"
}

// runAndFollow subscribes to the event stream BEFORE asking for the work — the
// daemon broadcasts the start event as soon as it has answered 202, and a
// subscription opened afterwards would miss it (the same rule snapshotAndWait
// follows). The daemon's answer is checked before anything is awaited, so a
// refusal exits immediately instead of waiting for events that will never come.
//
// A rollback rides the MIGRATION's event channel with three steps of its own,
// which is why this loop takes it unchanged: the terminal events, the step
// counter and the per-step title are the same wire shape.
func runAndFollow(c depsAPI, id string, report *depsActionReport,
	opts depsActionOpts, act depsAction,
) (*depsActionReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.follow.timeout)
	defer cancel()

	events, err := c.StreamEvents(ctx)
	if err != nil {
		return report, fmt.Errorf("connect to event stream: %w", err)
	}

	clickedAt := time.Now()
	res, err := startAction(c, id, act)
	if err != nil {
		return report, err
	}
	report.Message = res.Message
	sayProgress(res.Message)
	// From here on the work is running in the daemon: it outlives this
	// command, so every exit below reports what became of it, never "refused".
	report.Outcome = depsOutcomeUnknown

	poll := time.NewTicker(opts.follow.poll)
	defer poll.Stop()
	for {
		select {
		case evt, open := <-events:
			if !open {
				return report, errors.New(t("deps.streamClosed"))
			}
			if !isMigrationEventFor(evt, id) {
				continue
			}
			line, terminal, failed := renderMigrationEvent(evt)
			sayProgress(line)
			if terminal {
				if failed {
					report.Outcome = depsOutcomeFailed
					return report, errors.New(t("deps.failed", "error", evt.After))
				}
				report.Outcome = act.success
				return report, nil
			}
		case <-poll.C:
			if done, verdict := pollActionVerdict(c.GetDependencies, id, clickedAt, act.kind); done {
				report.Outcome = act.success
				if verdict != nil {
					report.Outcome = depsOutcomeFailed
				}
				return report, verdict
			}
		case <-ctx.Done():
			return report, errors.New(t("deps.streamClosed"))
		}
	}
}

// pollActionVerdict is the safety net under the event stream. The daemon
// DROPS an event rather than block when a subscriber's channel is full
// (internal/daemon/sse.go), and a migration's own stop/start burst — every app
// of the namespace going down and coming back — is the most likely thing in
// the launcher to fill it. A dropped terminal event used to leave this command
// waiting up to depsMigrationTimeout (six hours) for a migration that had
// finished minutes earlier, with no output and no way to tell.
//
// done=true means stop following; verdict is the error to report, or nil for a
// success. THREE conditions are required: the daemon must say nothing is
// running any more, the recorded result must be NEWER than this click (a
// verdict left behind by a previous run against the same dependency would
// otherwise be reported as this one's), and its Kind must be the one this run
// is waiting for — a namespace has ONE result slot and a migration and a
// rollback share it, so a migration's verdict would otherwise end a rollback's
// follow and report the opposite direction as its answer. Anything the poll
// cannot answer (a failed request, a result that is not ours) simply keeps the
// loop going — the events are still the primary source.
func pollActionVerdict(fetch func() (*api.DependenciesDto, error), id string,
	clickedAt time.Time, kind string,
) (done bool, verdict error) {
	dto, err := fetch()
	if err != nil || dto == nil || dto.Migration != nil {
		return false, nil
	}
	r := dto.LastResult
	if r == nil || r.ID != id || r.Kind != kind || r.FinishedAt < clickedAt.UnixMilli() {
		return false, nil
	}
	for _, line := range lastResultLines(r) {
		sayProgress(line)
	}
	if !r.Success {
		return true, errors.New(t("deps.failed", "error", r.Error))
	}
	return true, nil
}
