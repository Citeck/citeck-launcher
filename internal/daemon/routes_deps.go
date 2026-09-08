package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// depsMigrator is the seam between the routes and one dependency's migrator.
// Only PostgreSQL has a plan in v1; every other descriptor reports
// Migratable() == false and is refused before this is ever reached.
type depsMigrator interface {
	Preflight(ctx context.Context, env migrate.Env, from, to string) migrate.PreflightResult
	Plan(ctx context.Context, env migrate.Env, from, to string, opts migrate.PlanOptions) (*migrate.Plan, deps.MigrationJournal, error)
}

// migratorFor answers the migrator for a dependency. found=false means the
// descriptor claims Migratable() but this function has no plan wired for it —
// a wiring bug, not a user error, so the caller reports it as an internal
// failure rather than as "update the launcher".
func (d *Daemon) migratorFor(id deps.ID, env migrate.Env) (depsMigrator, bool) {
	if d.depsMigratorFn != nil {
		return d.depsMigratorFn(env), true
	}
	if id == deps.Postgres {
		return migrate.PostgresMigrator{}, true
	}
	return nil, false
}

// depsEnvFor builds the migration Env for a namespace snapshot, through the
// test seam when one is installed.
func (d *Daemon) depsEnvFor(act activeNamespace) migrate.Env {
	if d.depsEnvFn != nil {
		return d.depsEnvFn(act)
	}
	return d.newDepsEnv(act)
}

// depsMigrationState is the live progress of the running migration, pinned to
// its namespace the way updateInFlightNsID pins Updating: the field is
// daemon-global (one migration at a time) but the progress belongs to one
// namespace, and a user who switches namespaces mid-migration must not see it
// reported on the one they switched to.
type depsMigrationState struct {
	nsID string
	dto  api.DependencyMigrationDto
}

// setDepsMigration publishes (dto != nil) or clears the running migration.
func (d *Daemon) setDepsMigration(nsID string, dto *api.DependencyMigrationDto) {
	if dto == nil {
		d.depsMigration.Store(nil)
		return
	}
	d.depsMigration.Store(&depsMigrationState{nsID: nsID, dto: *dto})
}

// currentDepsMigration returns the running migration if it belongs to nsID.
// The DTO is copied out so a caller cannot mutate the published state.
func (d *Daemon) currentDepsMigration(nsID string) *api.DependencyMigrationDto {
	s := d.depsMigration.Load()
	if s == nil || s.nsID != nsID {
		return nil
	}
	dto := s.dto
	return &dto
}

// dependencyItems derives the list the UI and CLI show from three inputs: the
// runtime's PINS (what the data runs on), the last generation's
// effective-vs-candidate images, and the upgrades that generation held back.
//
// A dependency the LAST GENERATION did not emit (mongo on a v2 namespace,
// keycloak with authentication off) is left out entirely — including one that
// still carries a pin from when it did run, since the pin describes a volume
// nobody is mounting and the list would show a row with no target.
func dependencyItems(act activeNamespace) []api.DependencyDto {
	pins := map[deps.ID]string{}
	if act.runtime != nil {
		pins = act.runtime.DependencyPins()
	}
	held := map[deps.ID]namespace.DependencyUpgrade{}
	for _, u := range act.dependencyUpgrades {
		held[u.ID] = u
	}
	items := make([]api.DependencyDto, 0, len(deps.All()))
	for _, desc := range deps.All() {
		gen, generated := act.dependencies[desc.ID()]
		if !generated {
			// Not part of this namespace's LAST generation, so there is
			// nothing to offer: no candidate image, no container, no upgrade.
			// A leftover PIN is not enough — a namespace whose mongo was turned
			// off keeps the pin (the volume is deliberately left alone) and
			// would otherwise be listed "up-to-date" with an empty target,
			// i.e. a row about a container that no longer exists.
			continue
		}
		pin := pins[desc.ID()]
		current := pin
		if current == "" {
			current = gen.Effective
		}
		item := api.DependencyDto{
			ID: string(desc.ID()), App: desc.AppName(), CurrentImage: current,
			TargetImage: gen.Candidate, Migratable: desc.Migratable(), Status: api.DependencyUpToDate,
		}
		switch {
		case held[desc.ID()].To != "":
			// The offered target is the held-back one, and the VERSION must be
			// read off it rather than off the candidate: they agree today only
			// because both come from one generation.
			item.TargetImage = held[desc.ID()].To
			// Two questions, both of which must be yes: does the launcher ship
			// a migration for this DEPENDENCY, and does it know this PAIR?
			if desc.Migratable() && !unsupportedPair(desc.ID(), held[desc.ID()].From, item.TargetImage) {
				item.Status = api.DependencyUpgradeAvailable
			} else {
				item.Status = api.DependencyRequiresLauncherUpdate
			}
		case pin != "" && gen.Effective != "" && pin != gen.Effective:
			// Non-breaking: the generator already emits the new image and the
			// pin follows it once the container runs.
			item.Status = api.DependencyPendingMinor
		}
		if v, ok := desc.ParseVersion(current); ok {
			item.CurrentVersion = v.String()
		}
		if v, ok := desc.ParseVersion(item.TargetImage); ok {
			item.TargetVersion = v.String()
		}
		items = append(items, item)
	}
	return items
}

// unsupportedPair reports a version pair the launcher UNDERSTANDS, could
// plausibly migrate one day, and cannot migrate TODAY: the two majors would
// share a data volume, so this plan — which builds the new cluster next to the
// old data, and whose rollback is therefore a deletion — has nowhere to put it.
// 18 → 19 is the case that exists (PostgresLayoutFor maps every major from 18
// up to postgres3), and without this the row said "upgrade available: citeck
// deps upgrade postgres" and the preflight then refused it with a message about
// volumes, sending the operator after a disk problem they do not have.
//
// The answer this drives is "update the launcher", so everything a NEWER
// LAUNCHER WOULD NOT FIX has to be carved out and left to the preflight, which
// has the accurate message for each:
//
//   - an UNPARSABLE tag — refused with a message about the tag;
//   - a DOWNGRADE — refused with "the launcher does not migrate data
//     backwards", which is a policy, not a missing feature. Supports() answers
//     false for it (it demands a forward move), so without this carve-out both
//     routes told the operator to go and update a launcher that will never
//     grow the ability, and the real message became unreachable.
func unsupportedPair(id deps.ID, from, to string) bool {
	if id != deps.Postgres {
		return false
	}
	fromV, okFrom := deps.ParseImageVersion(from)
	toV, okTo := deps.ParseImageVersion(to)
	if !okFrom || !okTo {
		return false
	}
	if toV.Major < fromV.Major {
		return false
	}
	return !migrate.PostgresMigrator{}.Supports(fromV, toV)
}

// resultDto renders the last migration verdict for the wire.
func resultDto(r *deps.MigrationResult) *api.DependencyMigrationResultDto {
	if r == nil {
		return nil
	}
	return &api.DependencyMigrationResultDto{
		ID: string(r.ID), From: r.From, To: r.To, FinishedAt: r.FinishedAt.UnixMilli(),
		Success: r.OK(), Error: r.Error, OldVolume: r.OldVolume,
	}
}

// rollbackPendingMessage describes an OPEN journal that no running migration
// owns: an interrupted migration whose rollback has not succeeded. It is the
// user-facing half of the engine's "a failed rollback keeps the journal" rule
// — the leftovers it names are still on the host, the launcher retries the
// rollback at every start, and until then the dependency's pin cannot move
// (syncDependencyPinsUnderLock stands aside while a journal exists).
func rollbackPendingMessage(j *deps.MigrationJournal, last *deps.MigrationResult) string {
	if j == nil {
		return ""
	}
	msg := fmt.Sprintf("a previous migration of %s (%s → %s) left a rollback pending "+
		"(retried at every launcher start); until it succeeds the version is frozen", j.ID, j.From, j.To)
	if last != nil && last.Error != "" && last.ID == j.ID {
		msg += ": " + last.Error
	}
	return msg
}

// journalBlocker is the ONE place that reads an open migration journal and
// says what it means for a NEW migration. The journal covers two states and
// they must never be described interchangeably: one running right now, and an
// interrupted one whose rollback is still pending. Both refuse a new
// migration; only the second is something the operator has to act on.
// "" means the journal is clear.
func (d *Daemon) journalBlocker(act activeNamespace) string {
	rt := act.runtime
	if rt == nil {
		return ""
	}
	j := rt.MigrationJournal()
	if j == nil {
		return ""
	}
	if d.currentDepsMigration(namespaceIDOf(act)) != nil {
		return fmt.Sprintf("a migration of %s is already running", j.ID)
	}
	return rollbackPendingMessage(j, rt.LastDependencyMigration())
}

// rollbackBlocker is journalBlocker's second arm on its own: an open journal
// that NO running migration owns, i.e. an interrupted migration whose rollback
// has not succeeded. "" means there is nothing pending.
//
// It exists because a pending rollback refuses more than a new migration. What
// the journal describes is still on the host, and depsmig-src mounts the
// namespace's OWN data volume read-write: starting the namespace over it puts
// a second postmaster on one PGDATA (the postmaster.pid interlock does not
// hold across PID/IPC namespaces). The long-op lock does not cover this —
// nobody holds it once the failed migration's goroutine has returned — so
// every path that STARTS the namespace on the user's behalf asks here.
//
// A migration running right now is deliberately NOT reported: it holds the
// long-op lock, which refuses those paths already and with a better message.
func (d *Daemon) rollbackBlocker(act activeNamespace) string {
	if d.currentDepsMigration(namespaceIDOf(act)) != nil {
		return ""
	}
	return d.journalBlocker(act)
}

func (d *Daemon) handleListDependencies(w http.ResponseWriter, _ *http.Request) {
	act := d.active()
	if act.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	nsID := ""
	if act.nsConfig != nil {
		nsID = act.nsConfig.ID
	}
	dto := api.DependenciesDto{
		Items:      dependencyItems(act),
		Migration:  d.currentDepsMigration(nsID),
		LastResult: resultDto(act.runtime.LastDependencyMigration()),
	}
	// A journal that belongs to the migration reported above is not "pending";
	// it is simply the record of what is happening right now, and Migration
	// already says so — journalBlocker returns the rollback wording precisely
	// when there is no running migration to attribute the journal to.
	if dto.Migration == nil {
		dto.RollbackPending = d.journalBlocker(act)
	}
	writeJSON(w, dto)
}

// resolveMigration validates the {id} path segment and answers the pending
// (from → to) pair. Every refusal has already been written when ok is false.
func (d *Daemon) resolveMigration(w http.ResponseWriter, act activeNamespace, id string) (from, to string, ok bool) {
	desc, found := deps.Lookup(deps.ID(id))
	if !found {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeDependencyUnknown, fmt.Sprintf("unknown dependency %q", id))
		return "", "", false
	}
	if act.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return "", "", false
	}
	// "Nothing to migrate" is checked BEFORE "this launcher cannot migrate it":
	// for a dependency with no pending upgrade the second answer would send the
	// operator off to update a launcher that has nothing to do either way.
	var upgrade *namespace.DependencyUpgrade
	for i, u := range act.dependencyUpgrades {
		if u.ID == desc.ID() {
			upgrade = &act.dependencyUpgrades[i]
			break
		}
	}
	if upgrade == nil {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyUpToDate,
			fmt.Sprintf("%s has no pending upgrade", id))
		return "", "", false
	}
	if !desc.Migratable() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNotMigratable,
			fmt.Sprintf("this launcher cannot migrate %s — update the launcher", id))
		return "", "", false
	}
	// The dependency is migratable but this PAIR is not one this release has a
	// plan for (see unsupportedPair). Same code, same answer — update the
	// launcher — but the message names the versions, because "postgres cannot
	// be migrated" would contradict the 17 → 18 the same launcher performs.
	if unsupportedPair(desc.ID(), upgrade.From, upgrade.To) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNotMigratable,
			migrate.UnsupportedPairProblem(upgrade.From, upgrade.To))
		return "", "", false
	}
	return upgrade.From, upgrade.To, true
}

// preMigrationProblems collects everything that would refuse a migration of
// this namespace WITHOUT touching Docker. It is what the confirm screen shows
// as its own input: a condition that will 409 the click belongs in the dialog
// the user is reading, not in an error modal after they pressed the button.
//
// Not touching Docker matters for the running-migration arm too — probing
// volumes and containers in the middle of a migration measures a world that is
// being rewritten.
func (d *Daemon) preMigrationProblems(act activeNamespace) []string {
	var problems []string
	blocked := d.journalBlocker(act)
	if blocked != "" {
		problems = append(problems, blocked)
	}
	// A read, not a claim: this route mutates nothing, and the migrate route
	// does its own TryLock. The window between them is the same check-then-act
	// tryLongOp documents — worst case the confirm screen looks clear and the
	// click is refused with the same message.
	//
	// Skipped when the journal already spoke, because during a migration the
	// two describe ONE condition from two angles ("a migration of postgres is
	// already running" and "a dependency migration is in progress") and a
	// confirm screen that lists the same fact twice reads as two problems.
	// The journal's wording wins: it names the dependency.
	if holder := d.longOp.Holder(); holder != longOpNone && blocked == "" {
		problems = append(problems, holder.busyMessage()+" — wait for it to finish")
	}
	if act.runtime != nil {
		if st := act.runtime.Status(); !migratableNsStatus(st) {
			problems = append(problems, fmt.Sprintf(
				"the namespace is %s — start or stop it before migrating", st))
		}
	}
	return problems
}

func (d *Daemon) handleDependencyPreflight(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	id := r.PathValue("id")
	from, to, ok := d.resolveMigration(w, act, id)
	if !ok {
		return
	}
	if problems := d.preMigrationProblems(act); len(problems) > 0 {
		writeJSON(w, migrate.RefusedPreflight(from, to, problems...))
		return
	}
	if act.dockerClient == nil {
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}
	env := d.depsEnvFor(act)
	m, found := d.migratorFor(deps.ID(id), env)
	if !found {
		writeInternalError(w, fmt.Errorf("no migrator wired for dependency %q", id))
		return
	}
	// Measuring a data volume means walking it (or running `du` inside the
	// Docker VM), which on a real cluster outlives the socket server's 120s
	// write deadline — and the result is the only thing the confirm dialog has.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	writeJSON(w, m.Preflight(r.Context(), env, from, to))
}

// namespaceIDOf is the id a namespace-scoped daemon field is pinned to.
func namespaceIDOf(act activeNamespace) string {
	if act.nsConfig == nil {
		return ""
	}
	return act.nsConfig.ID
}

// migratableNsStatus reports whether a migration may be STARTED against this
// namespace status. Only the two settled states qualify: the plan's first step
// stops the namespace, Runtime.Stop merely enqueues that command, and a
// runtime mid-transition may never read it — see ErrCodeDependencyNamespaceBusy.
func migratableNsStatus(st namespace.NsRuntimeStatus) bool {
	return st == namespace.NsStatusRunning || st == namespace.NsStatusStopped
}

func (d *Daemon) handleDependencyMigrate(w http.ResponseWriter, r *http.Request) {
	var req api.DependencyMigrateRequestDto
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, "invalid request body")
			return
		}
	}
	act := d.active()
	id := deps.ID(r.PathValue("id"))
	from, to, ok := d.resolveMigration(w, act, string(id))
	if !ok {
		return
	}
	nsID := namespaceIDOf(act)
	if blocked := d.journalBlocker(act); blocked != "" {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyMigrationInProgress, blocked)
		return
	}
	if st := act.runtime.Status(); !migratableNsStatus(st) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNamespaceBusy,
			fmt.Sprintf("the namespace is %s — start or stop it before migrating %s", st, id))
		return
	}
	// Claimed as longOpMigration, NEVER through tryLongOp: that helper labels
	// its holder longOpRequest, which the five lifecycle routes TOLERATE — so a
	// mislabeled migration would let a namespace Start and the per-app toggles
	// run right beside the volume rewrite this lock exists to protect. See the
	// trap on longOpMigration. Ownership transfers into the goroutine below.
	if !d.longOp.TryLock(longOpMigration) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeLongOpInProgress,
			d.longOp.Holder().busyMessage()+" — wait for it to finish")
		return
	}
	if act.dockerClient == nil {
		d.longOp.Unlock()
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}
	env := d.depsEnvFor(act)
	m, found := d.migratorFor(id, env)
	if !found {
		d.longOp.Unlock()
		writeInternalError(w, fmt.Errorf("no migrator wired for dependency %q", id))
		return
	}
	evt := func(typ, phase string, cur, total int, pct float64, msg string) api.EventDto {
		return api.EventDto{
			Type: typ, Timestamp: time.Now().UnixMilli(), NamespaceID: nsID,
			AppName: string(id), Phase: phase, Current: cur, Total: total, Percent: pct, After: msg,
		}
	}
	// Building the plan runs the whole preflight — including a `du` of the data
	// volume, which on a real cluster is minutes — and it happens AFTER the
	// click and BEFORE the 202. Until this, nothing existed to show for that
	// stretch: the button did nothing, visibly, for as long as the measurement
	// took. Publish the state (and broadcast it, for the CLI, which subscribes
	// before it posts) with StepCount 0, which is how a client tells "no plan
	// yet" from a plan of zero steps: render a spinner, not an empty list.
	d.setDepsMigration(nsID, &api.DependencyMigrationDto{
		ID: string(id), Step: api.DependencyMigrationStepPreparing,
	})
	d.broadcastEvent(evt(api.EventDepsMigrationProgress, api.DependencyMigrationStepPreparing,
		0, 0, 0, fmt.Sprintf("%s → %s", from, to)))
	// The plan is built on the REQUEST's context on purpose: it only probes
	// (preflight), it changes nothing, and a client that gave up should not
	// leave it running. Everything after the 202 runs on the daemon's
	// background context instead — the migration must survive the request.
	// Same measurement as the preflight route, and the same deadline problem:
	// the 202 is written only after the plan is built.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	plan, journal, err := m.Plan(r.Context(), env, from, to, migrate.PlanOptions{
		ReplaceExistingVolume: req.ReplaceExistingVolume,
	})
	if err != nil {
		// The refusal is the answer to THIS request, so the preparing state
		// goes with it — leaving it published would show a migration that is
		// not happening to every client until the next one starts.
		d.setDepsMigration(nsID, nil)
		d.longOp.Unlock()
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyPreflightFailed, err.Error())
		return
	}
	steps := len(plan.Steps)
	d.setDepsMigration(nsID, &api.DependencyMigrationDto{ID: string(id), StepCount: steps})
	rt := act.runtime
	d.bgWg.Go(func() {
		defer d.longOp.Unlock()
		defer d.setDepsMigration(nsID, nil)
		d.broadcastEvent(evt(api.EventDepsMigrationStart, "", 0, steps, 0, fmt.Sprintf("%s → %s", from, to)))
		progress := func(step string, i, n int, pct float64, msg string) {
			d.setDepsMigration(nsID, &api.DependencyMigrationDto{
				ID: string(id), Step: step, StepIndex: i, StepCount: n, Percent: pct, Message: msg,
			})
			d.broadcastEvent(evt(api.EventDepsMigrationProgress, step, i, n, pct, msg))
		}
		runErr := migrate.Run(d.bgCtx, rt, journal, plan, progress)
		var fe *migrate.FinalizeError
		switch {
		case runErr == nil:
			//nolint:gosec // G706: id passed deps.Lookup (a fixed registry) and the images come from the resolved bundle/pins
			slog.Info("Dependency migration finished", "dependency", id, "from", from, "to", to)
			d.broadcastEvent(evt(api.EventDepsMigrationComplete, "", steps, steps, 100,
				fmt.Sprintf("%s migrated to %s", id, to)))
		case errors.As(runErr, &fe):
			// The data has moved and the pin says so; only the tidy-up or the
			// restart failed, so this is a completion with a warning.
			//nolint:gosec // G706: id passed deps.Lookup (a fixed registry)
			slog.Warn("Dependency migration committed with a finalize failure", "dependency", id, "err", fe.Err)
			d.broadcastEvent(evt(api.EventDepsMigrationComplete, "", steps, steps, 100,
				fmt.Sprintf("%s migrated to %s; %v", id, to, fe.Err)))
		default:
			//nolint:gosec // G706: id passed deps.Lookup (a fixed registry)
			slog.Error("Dependency migration failed", "dependency", id, "err", runErr)
			d.broadcastEvent(evt(api.EventDepsMigrationError, "", 0, steps, 0, runErr.Error()))
		}
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Migration of %s to %s started", id, to),
	})
}
