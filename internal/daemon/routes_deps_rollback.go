package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The dependency ROLLBACK: put one dependency back on the image AND the volume
// generation it ran on before its last completed migration, and start the
// namespace on it again.
//
// It is deliberately NOT a journalled multi-step operation, and that is a
// decision rather than a shortcut. Nothing is created and nothing is deleted:
// the retained volume is already there — that is the premise of the feature —
// and the volume the namespace is leaving is kept exactly as it is. So there
// is no partial state a journal could describe, and writing one would be
// actively harmful: a journal surviving a crash makes the boot recovery
// (deps_recovery.go) refuse to start the namespace until it is cleared, which
// for an operation with nothing to clean up would strand a namespace over a
// write that either landed or did not.
//
// Two things it DOES share with a migration and must not drift from:
//
//   - the namespace is STOPPED before the pin moves. Not for the data's sake,
//     for syncDependencyPinsUnderLock's: that hook re-pins any dependency whose
//     app is RUNNING on an image other than its pin, so a rollback written
//     while the 18 container is up would be re-pinned forward on the next loop
//     tail WHILE THE GENERATION STAYED BACK — postgres:18 mounted on the
//     generation that holds 17 data;
//   - the long-operation lock, claimed explicitly as longOpDepsRollback (never
//     through tryLongOp, which labels its holder longOpRequest — a member of
//     tolerateLifecycleWork), so nothing starts the namespace in the window
//     between the stop and the write.

// rollbackTarget resolves the {id} path segment and answers BOTH halves of the
// move: the state the namespace runs on now and the one a rollback would
// restore. Every refusal has already been written when ok is false.
//
// The two come out of ONE read of the pin map, not two: syncDependencyPinsUnderLock
// moves a pin's image from the runtime loop whenever a container is RUNNING on
// something else, so two reads could describe a from and a to that never
// coexisted — and "from" is what the verdict records.
//
// It reads the RUNTIME rather than the Env: the runtime is the authority on
// what this namespace runs, and the Env's copy is a view of it.
func (d *Daemon) rollbackTarget(w http.ResponseWriter, act activeNamespace, id string) (cur, prev deps.DependencyState, ok bool) {
	desc, found := deps.Lookup(deps.ID(id))
	if !found {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeDependencyUnknown, fmt.Sprintf("unknown dependency %q", id))
		return cur, prev, false
	}
	if act.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return cur, prev, false
	}
	cur = act.runtime.DependencyStates()[desc.ID()]
	prev, has := cur.Previous()
	if !has {
		// The ordinary state of a namespace that has never migrated, and of
		// every namespace that has already rolled back — a rollback withdraws
		// its own offer, because there is no roll-forward.
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNoRollbackTarget,
			fmt.Sprintf("%s has no recorded previous version to roll back to", id))
		return cur, prev, false
	}
	return cur, prev, true
}

func (d *Daemon) handleDependencyRollbackPreflight(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	id := r.PathValue("id")
	cur, prev, ok := d.rollbackTarget(w, act, id)
	if !ok {
		return
	}
	// The same three conditions a migration's confirm screen shows as its own
	// input: a condition that will 409 the click belongs in the dialog the user
	// is reading, not in an error modal after they pressed the button. They are
	// also the three the preflight itself cannot answer — an open journal,
	// another long operation and a namespace mid-transition are daemon facts,
	// not Docker ones.
	if problems := d.preMigrationProblems(act, "rolling it back"); len(problems) > 0 {
		writeJSON(w, migrate.RefusedPreflight(cur.Image, prev.Image, problems...))
		return
	}
	if act.dockerClient == nil {
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}
	// It measures nothing, but it does read a file out of a data volume, which
	// on a desktop is a utils container — the same reason the migration
	// preflight lifts the deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	writeJSON(w, migrate.RollbackPreflight(r.Context(), d.depsEnvFor(act), deps.ID(id), prev))
}

func (d *Daemon) handleDependencyRollback(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	id := deps.ID(r.PathValue("id"))
	cur, prev, ok := d.rollbackTarget(w, act, string(id))
	if !ok {
		return
	}
	nsID := namespaceIDOf(act)
	if blocked := d.journalBlocker(act); blocked != "" {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyMigrationInProgress, blocked)
		return
	}
	// Same rule as a migration: Runtime.Stop only ENQUEUES, and a runtime
	// mid-transition may not be read — so a STALLED namespace has to be settled
	// first rather than have the rollback burn its stop timeout.
	if st := act.runtime.Status(); !migratableNsStatus(st) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNamespaceBusy,
			fmt.Sprintf("the namespace is %s — start or stop it before rolling %s back", st, id))
		return
	}
	if !d.longOp.TryLock(longOpDepsRollback) {
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

	// Published before the preflight for the same reason the migration
	// publishes it before its plan is built: the click has been accepted and
	// the daemon is deciding, and StepCount 0 is what tells a client to render
	// a spinner rather than an empty step list. Cleared with the refusal it
	// belongs to.
	d.setDepsRollback(nsID, &api.DependencyMigrationDto{
		ID: string(id), Step: api.DependencyMigrationStepPreparing,
	})
	d.broadcastEvent(depsEvent(api.EventDepsMigrationProgress, nsID, id,
		api.DependencyMigrationStepPreparing, 0, 0, 0, fmt.Sprintf("%s → %s", cur.Image, prev.Image)))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	// The preflight runs on the REQUEST's context: it only probes, and a client
	// that gave up should not leave it running. Everything after the 202 runs
	// on the daemon's background context — the rollback must survive the
	// request.
	pre := migrate.RollbackPreflight(r.Context(), env, id, prev)
	if !pre.OK {
		d.setDepsRollback(nsID, nil)
		d.longOp.Unlock()
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyPreflightFailed,
			strings.Join(pre.Problems, "; "))
		return
	}
	wasRunning := pre.WasRunning
	rt := act.runtime
	d.bgWg.Go(func() {
		defer d.longOp.Unlock()
		defer d.setDepsRollback(nsID, nil)
		d.runRollback(rollbackRun{
			nsID: nsID, id: id, env: env, rt: rt,
			cur: cur, prev: prev, wasRunning: wasRunning,
		})
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Rollback of %s to %s started", id, prev.Image),
	})
}

// rollbackRun is everything the background pass needs, captured from the
// request goroutine so nothing re-reads an active namespace that may have been
// switched underneath it.
type rollbackRun struct {
	nsID       string
	id         deps.ID
	env        migrate.Env
	rt         *namespace.Runtime
	cur, prev  deps.DependencyState
	wasRunning bool
}

// runRollback is the three steps, in the one order that is safe.
//
// Failures are REPORTED, not recorded as a verdict. A namespace has exactly one
// result slot and it currently holds the migration this rollback is undoing —
// the record the offer reads its date from. Overwriting it with "the rollback
// failed" would date a still-live offer by the failure, for an operation that
// changed nothing; the error reaches the operator as an event (and, for a
// synchronous caller, as the HTTP refusal that precedes the 202).
func (d *Daemon) runRollback(run rollbackRun) {
	ctx := d.bgCtx
	steps := migrate.RollbackStepIDs()
	total := len(steps)
	progress := func(i int, msg string) {
		dto := &api.DependencyMigrationDto{
			ID: string(run.id), Step: steps[i-1], StepIndex: i, StepCount: total, Message: msg,
		}
		d.setDepsRollback(run.nsID, dto)
		d.broadcastEvent(depsEvent(api.EventDepsMigrationProgress, run.nsID, run.id,
			steps[i-1], i, total, 0, msg))
	}
	fail := func(err error) {
		//nolint:gosec // G706: id came from deps.Lookup (a fixed registry)
		slog.Error("Dependency rollback failed", "dependency", run.id, "err", err)
		d.broadcastEvent(depsEvent(api.EventDepsMigrationError, run.nsID, run.id, "", 0, total, 0, err.Error()))
	}
	d.broadcastEvent(depsEvent(api.EventDepsMigrationStart, run.nsID, run.id, "", 0, total, 0,
		fmt.Sprintf("%s → %s", run.cur.Image, run.prev.Image)))

	progress(1, "")
	if err := run.env.StopNamespace(ctx); err != nil {
		// Nothing has moved, and the namespace is wherever the failed stop left
		// it — starting it again here would fight whatever refused to stop.
		fail(fmt.Errorf("stop namespace: %w", err))
		return
	}

	progress(2, "")
	res := deps.MigrationResult{
		ID: run.id, From: run.cur.Image, To: run.prev.Image,
		FinishedAt: time.Now(), Kind: deps.ResultKindRollback,
		// The volume the namespace is LEAVING: kept, never read again, and
		// therefore the one the operator may reclaim — the same meaning
		// OldVolume carries for a migration.
		OldVolume: frozenVolumeName(run.id, run.cur),
	}
	if err := run.rt.RollbackDependencyState(run.id, run.prev, res); err != nil {
		// A refused write moved nothing (the Runtime is atomic in memory too),
		// so the namespace goes back the way it was found — on the OLD pin.
		// Leaving it stopped would make an action that did nothing cost the
		// operator a manual Start.
		if relErr := run.env.ReloadAndStart(ctx, run.wasRunning); relErr != nil {
			fail(fmt.Errorf("%w (and the namespace could not be started again: %w)", err, relErr))
			return
		}
		fail(err)
		return
	}

	progress(3, "")
	if err := run.env.ReloadAndStart(ctx, run.wasRunning); err != nil {
		// The pin HAS moved and the record says so, so this is a completion
		// with a warning, not a failure to undo: pressing Start is the whole
		// recovery, exactly as after any failed start.
		//nolint:gosec // G706: id came from deps.Lookup (a fixed registry)
		slog.Warn("Dependency rollback committed but the namespace did not come back up",
			"dependency", run.id, "err", err)
		d.broadcastEvent(depsEvent(api.EventDepsMigrationComplete, run.nsID, run.id, "", total, total, 100,
			fmt.Sprintf("%s rolled back to %s; %v", run.id, run.prev.Image, err)))
		return
	}
	//nolint:gosec // G706: id came from deps.Lookup (a fixed registry) and the images come from the pin
	slog.Info("Dependency rollback finished", "dependency", run.id,
		"from", run.cur.Image, "to", run.prev.Image)
	d.broadcastEvent(depsEvent(api.EventDepsMigrationComplete, run.nsID, run.id, "", total, total, 100,
		fmt.Sprintf("%s rolled back to %s", run.id, run.prev.Image)))
}

// frozenVolumeName is the volume the namespace runs on today, "" for a
// dependency with no data volume of its own.
func frozenVolumeName(id deps.ID, cur deps.DependencyState) string {
	d, ok := deps.Lookup(id)
	if !ok {
		return ""
	}
	return deps.VolumeName(d, cur.Gen())
}

// setDepsRollback publishes progress on the MIGRATION channel with the
// rollback discriminator, so the CLI's event renderer and the dialog's
// progress screen need no path of their own — only the title differs.
func (d *Daemon) setDepsRollback(nsID string, dto *api.DependencyMigrationDto) {
	if dto != nil {
		dto.Kind = deps.ResultKindRollback
	}
	d.setDepsMigration(nsID, dto)
}

// depsEvent builds one deps_migration_* event. It is shared by the migration
// and the rollback so the two cannot describe one channel differently.
func depsEvent(typ, nsID string, id deps.ID, phase string, cur, total int, pct float64, msg string) api.EventDto {
	return api.EventDto{
		Type: typ, Timestamp: time.Now().UnixMilli(), NamespaceID: nsID,
		AppName: string(id), Phase: phase, Current: cur, Total: total, Percent: pct, After: msg,
	}
}
