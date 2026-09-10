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

	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// migratorFor answers the migrator for a dependency. found=false means the
// descriptor claims Migratable() but nothing is wired for it — a wiring bug,
// not a user error, so the caller reports it as an internal failure rather
// than as "update the launcher".
//
// The lookup itself lives in internal/deps/migrate and is PURE: the dependency
// LIST asks about every dependency on every request and has no Env to give,
// and a second table here is exactly how "the descriptor claims Migratable()
// and nothing is wired" becomes reachable again. This keeps only the test
// seam.
func (d *Daemon) migratorFor(id deps.ID) (migrate.Migrator, bool) {
	if d.depsMigratorFn != nil {
		return d.depsMigratorFn(id), true
	}
	return migrate.MigratorFor(id)
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
func (d *Daemon) dependencyItems(ctx context.Context, t *i18n.Translator, act activeNamespace) []api.DependencyDto {
	states := map[deps.ID]deps.DependencyState{}
	if act.runtime != nil {
		states = act.runtime.DependencyStates()
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
		pin := states[desc.ID()].Image
		current := pin
		if current == "" {
			current = gen.Effective
		}
		item := api.DependencyDto{
			ID: string(desc.ID()), App: desc.AppName(), CurrentImage: current,
			TargetImage: gen.Candidate, Migratable: desc.Migratable(), Status: api.DependencyUpToDate,
		}
		item.Rollback = renderRollbackOffer(t, d.rollbackOffer(ctx, act, desc, states[desc.ID()]))
		switch {
		case held[desc.ID()].To != "":
			// The offered target is the held-back one, and the VERSION must be
			// read off it rather than off the candidate: they agree today only
			// because both come from one generation.
			item.TargetImage = held[desc.ID()].To
			item.Status, item.StatusDetail = d.heldUpgradeStatus(t, desc, held[desc.ID()], item.TargetImage, item.Rollback)
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

// heldUpgradeStatus is the ONE place a held-back upgrade is turned into the
// status and the sentence every surface shows. Three questions, in this order,
// because each later one would give the wrong advice if an earlier one applies:
//
//  0. is the candidate OLDER than what the data runs on? Then it is not an
//     upgrade at all, and none of the three questions below has a truthful
//     answer for it: no launcher moves data backwards (so "update the
//     launcher" is a lie), the vendor was never asked (a backwards move skips
//     vendorVerdict, because "there is no upgrade path from 4.2.9 to 4.1.8"
//     answers a question nobody asked), and the migrator's own refusal for a
//     downgrade is deliberately EMPTY — which, before this arm existed, made
//     the very first case below claim it as an ordinary "upgrade available".
//     It comes first for that reason: every later arm would answer it wrongly;
//  1. does this LAUNCHER ship a migration for the dependency at all? If not,
//     nothing about the pair matters — "update the launcher" is the whole
//     answer, and it is the truthful one;
//  2. does the DEPENDENCY'S OWN VENDOR forbid this hop? Then updating the
//     launcher would not help, so the status must not borrow the words that
//     say it would. The FACT comes from the generator (which asked the
//     registry) and the SENTENCE comes from the migrator, so the two cannot
//     drift into two different accounts of one refusal;
//  3. can THIS RELEASE carry the pair out? A refusal here really is a
//     launcher-age problem.
//
// A pair the migrator refuses with an EMPTY reason is deliberately reported as
// an ordinary upgrade: that is the migrator contract's "the preflight words
// this better" (a downgrade, an unparsable tag), and the refusal then happens
// where the accurate sentence lives instead of being overwritten here.
func (d *Daemon) heldUpgradeStatus(t *i18n.Translator, desc deps.Descriptor, held namespace.DependencyUpgrade,
	target string, rollback *api.DependencyRollbackDto,
) (status, detail string) {
	if held.BundleOlder {
		return api.DependencyBundleOlder, t.Render(bundleOlderDetail(desc, held.From, target, rollback))
	}
	if !desc.Migratable() {
		return api.DependencyRequiresLauncherUpdate, ""
	}
	problem := d.pairProblem(desc.ID(), held.From, target)
	switch {
	case problem.Empty():
		return api.DependencyUpgradeAvailable, ""
	case held.VendorBlocked:
		return api.DependencyUpgradeBlocked, t.Render(problem)
	default:
		// StatusDetail stays empty: "requires-launcher-update" is a complete
		// answer on its own, and the label is what the table renders.
		return api.DependencyRequiresLauncherUpdate, ""
	}
}

// bundleOlderDetail is the sentence behind the bundle-older status, and the
// choice between its two forms is the whole of R2.5's "routing, not merging":
// the launcher offers exactly one deliberate way back, and it exists only when
// this namespace is the one that migrated away from the version being offered.
//
// The test for "the same version" is the registry's own FORMAT rule rather
// than a hand-written major/series comparison: a retained volume the offered
// image can read is precisely a non-breaking pair, which is the same question
// deps.Breaking answers everywhere else. The offer must also be USABLE —
// pointing at `citeck deps rollback` when the retained volume is gone would
// send the operator after a volume the launcher itself told them they could
// delete.
func bundleOlderDetail(desc deps.Descriptor, from, target string, rollback *api.DependencyRollbackDto) msg.Message {
	if rollback != nil && rollback.Available && !deps.Breaking(desc, rollback.ToImage, target) {
		return migrate.BundleOlderRollbackNotice(string(desc.ID()), from, target)
	}
	return migrate.BundleOlderNotice(from, target)
}

// rollbackOffer is the per-dependency "go back to what this ran on before the
// last migration" offer, or nil when there is nothing to go back to.
//
// It asks exactly TWO of the six questions the rollback preflight asks: is
// there a recorded previous state, and is its volume still there. The other
// four — does the volume still hold what the pin claims, is a journal open, is
// another long operation running, is the namespace settled — are the
// preflight's, which the user reaches by clicking. That split is not tidiness:
// this runs for every dependency on every list request, and on a desktop each
// volume read is a utils container.
func (d *Daemon) rollbackOffer(ctx context.Context, act activeNamespace,
	desc deps.Descriptor, st deps.DependencyState,
) *api.DependencyRollbackDto {
	prev, has := st.Previous()
	if !has {
		return nil
	}
	retained := deps.VolumeName(desc, prev.Gen())
	offer := &api.DependencyRollbackDto{
		ToImage:      prev.Image,
		Volume:       retained,
		FrozenVolume: deps.VolumeName(desc, st.Gen()),
		MigratedAt:   migrationFinishedAt(act.runtime, desc.ID()),
	}
	if v, ok := desc.ParseVersion(prev.Image); ok {
		offer.ToVersion = v.String()
	}
	if retained == "" {
		// Unreachable for the dependencies that can migrate today (Keycloak is
		// the one with no volume of its own, and nothing migrates it), but a
		// silent Available=true here would offer a switch to a generation that
		// does not physically exist.
		offer.ProblemMsg = migrate.NoOwnVolumeProblem(desc.ID())
		return offer
	}
	exists, err := d.depsEnvFor(act).VolumeExists(ctx, retained)
	switch {
	case err != nil:
		offer.ProblemMsg = migrate.VolumeCheckProblem(retained, err)
	case !exists:
		// Same sentence migrate.RollbackPreflight gives once the user clicks
		// through — from the same function, so the two cannot drift.
		offer.ProblemMsg = migrate.RetainedVolumeGoneProblem(desc.ID(), retained, prev.Image)
	default:
		offer.Available = true
	}
	return offer
}

// migrationFinishedAt is when the MIGRATION this dependency would roll back
// finished, or 0 when the namespace's one result slot no longer holds it.
//
// Three conditions, and each excludes a result that would date the offer
// wrongly: another dependency's migration (the slot is per namespace), a
// FAILED migration (it moved no pin, so it is not the one that created this
// target), and a ROLLBACK verdict — which for a live offer can only be a
// rollback that FAILED, and dating "the data as it was when the migration
// finished" by that failure would be simply false.
func migrationFinishedAt(rt *namespace.Runtime, id deps.ID) int64 {
	if rt == nil {
		return 0
	}
	res := rt.LastDependencyMigration()
	if res == nil || res.ID != id || res.Kind != deps.ResultKindMigration || !res.OK() {
		return 0
	}
	return res.FinishedAt.UnixMilli()
}

// pairProblem asks the dependency's migrator about ONE version pair and
// answers the operator-facing refusal, "" when the pair is fine. It is the
// single place the list route, the preflight route and the migrate route agree
// on what a pair is worth, and it carries the REASON rather than a boolean so
// the three cannot word it differently.
//
// Two carve-outs, both of which answer "" and leave the refusal to the
// preflight, which has the accurate message for each:
//
//   - an UNPARSABLE tag on either side. There are no versions to ask a
//     migrator about, and the preflight's message names the tag;
//   - any refusal the migrator states with an EMPTY reason. That is its
//     contract (see migrate.Migrator): a downgrade is a POLICY, not a missing
//     feature, and routing it here would tell the operator to go and update a
//     launcher that will never grow the ability.
func (d *Daemon) pairProblem(id deps.ID, from, to string) msg.Message {
	desc, found := deps.Lookup(id)
	if !found {
		return msg.Message{}
	}
	fromV, okFrom := desc.ParseVersion(from)
	toV, okTo := desc.ParseVersion(to)
	if !okFrom || !okTo {
		return msg.Message{}
	}
	m, wired := d.migratorFor(id)
	if !wired {
		// The descriptor claims no migrator, or claims one that is not wired.
		// Both are answered by the caller's Migratable() arm; saying anything
		// about the pair here would add a second, weaker account of it.
		return msg.Message{}
	}
	if ok, problem := m.SupportsPair(fromV, toV); !ok {
		return problem
	}
	return msg.Message{}
}

// rollbackPendingMessage describes an OPEN journal that no running migration
// owns: an interrupted migration whose rollback has not succeeded. It is the
// user-facing half of the engine's "a failed rollback keeps the journal" rule
// — the leftovers it names are still on the host, the launcher retries the
// rollback at every start, and until then the dependency's pin cannot move
// (syncDependencyPinsUnderLock stands aside while a journal exists).
func rollbackPendingMessage(t *i18n.Translator, j *deps.MigrationJournal, last *deps.MigrationResult) string {
	if j == nil {
		return ""
	}
	// TWO keys rather than one sentence with the verdict glued on: the detail
	// is a clause of its own (often a whole English error), and a translator
	// handed "…the version is frozen" plus ": {detail}" cannot decide where
	// the colon belongs in their language. The one that carries it says so.
	if last != nil && last.ID == j.ID {
		if detail := lastFailureText(t, last); detail != "" {
			return t.T("deps.msg.journal.rollbackPendingDetail",
				"id", string(j.ID), "from", j.From, "to", j.To, "detail", detail)
		}
	}
	return t.T("deps.msg.journal.rollbackPending", "id", string(j.ID), "from", j.From, "to", j.To)
}

// lastFailureText is the verdict a pending rollback quotes: the structured
// sentence when the launcher worded it, the persisted English otherwise. Same
// preference, and same reason, as resultDto — a result written by an older
// launcher has only the string.
func lastFailureText(t *i18n.Translator, last *deps.MigrationResult) string {
	if !last.ErrorMsg.Empty() {
		return t.Render(last.ErrorMsg)
	}
	return last.Error
}

// journalBlocker is the ONE place that reads an open migration journal and
// says what it means for a NEW migration. The journal covers two states and
// they must never be described interchangeably: one running right now, and an
// interrupted one whose rollback is still pending. Both refuse a new
// migration; only the second is something the operator has to act on.
// "" means the journal is clear.
func (d *Daemon) journalBlocker(t *i18n.Translator, act activeNamespace) string {
	rt := act.runtime
	if rt == nil {
		return ""
	}
	j := rt.MigrationJournal()
	if j == nil {
		return ""
	}
	if d.currentDepsMigration(namespaceIDOf(act)) != nil {
		return t.T("deps.msg.journal.running", "id", string(j.ID))
	}
	return rollbackPendingMessage(t, j, rt.LastDependencyMigration())
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
func (d *Daemon) rollbackBlocker(t *i18n.Translator, act activeNamespace) string {
	if d.currentDepsMigration(namespaceIDOf(act)) != nil {
		return ""
	}
	return d.journalBlocker(t, act)
}

func (d *Daemon) handleListDependencies(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	if act.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	nsID := ""
	if act.nsConfig != nil {
		nsID = act.nsConfig.ID
	}
	t := d.translatorFor(r)
	dto := api.DependenciesDto{
		Items:      d.dependencyItems(r.Context(), t, act),
		Migration:  renderMigrationDto(t, d.currentDepsMigration(nsID)),
		LastResult: resultDto(t, act.runtime.LastDependencyMigration()),
	}
	// A journal that belongs to the migration reported above is not "pending";
	// it is simply the record of what is happening right now, and Migration
	// already says so — journalBlocker returns the rollback wording precisely
	// when there is no running migration to attribute the journal to.
	if dto.Migration == nil {
		dto.RollbackPending = d.journalBlocker(t, act)
	}
	writeJSON(w, dto)
}

// resolveMigration validates the {id} path segment and answers the pending
// (from → to) pair. Every refusal has already been written when ok is false.
func (d *Daemon) resolveMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, act activeNamespace, id string) (from, to string, ok bool) {
	t := d.translatorFor(r)
	desc, found := deps.Lookup(deps.ID(id))
	if !found {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeDependencyUnknown,
			t.T("deps.msg.route.unknownDependency", "id", id))
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
			t.T("deps.msg.route.noPendingUpgrade", "id", id))
		return "", "", false
	}
	// A BACKWARDS candidate is still a held-back "upgrade" and would otherwise
	// walk straight past the two arms below into the preflight, which refuses
	// it with the right sentence in the wrong place. Refused here, before
	// either of them: DEPENDENCY_NOT_MIGRATABLE would promise that a newer
	// launcher helps (none will ever move data backwards) and
	// DEPENDENCY_PAIR_UNSUPPORTED would report a vendor refusal to a question
	// nobody asked — a backwards move never reaches vendorVerdict at all.
	if upgrade.BundleOlder {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyBackwards,
			t.Render(bundleOlderDetail(desc, upgrade.From, upgrade.To,
				d.rollbackOffer(ctx, act, desc, act.runtime.DependencyStates()[desc.ID()]))))
		return "", "", false
	}
	if !desc.Migratable() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNotMigratable,
			t.T("deps.msg.route.notMigratable", "id", id))
		return "", "", false
	}
	// The dependency is migratable but this PAIR is refused. WHICH refusal it
	// is decides the code, and the two must never be collapsed: a hop the
	// DEPENDENCY'S vendor forbids is not fixed by a newer launcher, so it gets
	// its own code and the vendor's own sentence (which names the intermediate
	// version to take first, when there is one). A pair this RELEASE cannot
	// carry out keeps the old code and the old answer — update the launcher —
	// with a message that names the versions, because "postgres cannot be
	// migrated" would contradict the 17 → 18 the same launcher performs.
	if problem := d.pairProblem(desc.ID(), upgrade.From, upgrade.To); !problem.Empty() {
		code := api.ErrCodeDependencyNotMigratable
		if upgrade.VendorBlocked {
			code = api.ErrCodeDependencyPairUnsupported
		}
		writeErrorCode(w, http.StatusConflict, code, t.Render(problem))
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
// action is the gerund the namespace-status line ends with ("migrating",
// "rolling it back"). It is a parameter and not a constant because this list is
// what the confirm screen SHOWS: telling an operator who clicked Roll back to
// "start or stop it before migrating" sends them looking for a migration, the
// same defect the long-op holder's own busyMessage exists to prevent. The other
// two arms need no such split — the journal really is a migration's, and the
// holder names itself.
func (d *Daemon) preMigrationProblems(t *i18n.Translator, act activeNamespace, settleKey string) []msg.Message {
	var problems []msg.Message
	blocked := d.journalBlocker(t, act)
	if blocked != "" {
		// journalBlocker composes two keys of its own, so what comes back is
		// already a sentence. passthrough is how an already-final string
		// re-enters the message pipeline, and it says so at the call site.
		problems = append(problems, msg.New("deps.msg.passthrough", "text", blocked))
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
		problems = append(problems, holder.busyMessage())
	}
	if act.runtime != nil {
		if st := act.runtime.Status(); !migratableNsStatus(st) {
			problems = append(problems, msg.New(settleKey, "status", string(st)))
		}
	}
	return problems
}

// The two "settle the namespace first" sentences preMigrationProblems chooses
// between. They are two whole keys rather than one with a {action} slot: the
// gerund is a CLAUSE, and a clause dropped into another sentence in English
// word order is the shape no translator can rearrange — Russian wants a
// different case for it, German a different position.
const (
	settleBeforeMigrating   = "deps.msg.ns.settleBeforeMigrating"
	settleBeforeRollingBack = "deps.msg.ns.settleBeforeRollingBack"
)

func (d *Daemon) handleDependencyPreflight(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	t := d.translatorFor(r)
	id := r.PathValue("id")
	from, to, ok := d.resolveMigration(r.Context(), w, r, act, id)
	if !ok {
		return
	}
	if problems := d.preMigrationProblems(t, act, settleBeforeMigrating); len(problems) > 0 {
		writeJSON(w, renderPreflight(t, migrate.RefusedPreflight(from, to, problems...)))
		return
	}
	if act.dockerClient == nil {
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}
	env := d.depsEnvFor(act)
	m, found := d.migratorFor(deps.ID(id))
	if !found {
		writeInternalError(w, fmt.Errorf("no migrator wired for dependency %q", id))
		return
	}
	// Measuring a data volume means walking it (or running `du` inside the
	// Docker VM), which on a real cluster outlives the socket server's 120s
	// write deadline — and the result is the only thing the confirm dialog has.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	writeJSON(w, renderPreflight(t, m.Preflight(r.Context(), env, from, to)))
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
	t := d.translatorFor(r)
	id := deps.ID(r.PathValue("id"))
	from, to, ok := d.resolveMigration(r.Context(), w, r, act, string(id))
	if !ok {
		return
	}
	nsID := namespaceIDOf(act)
	if blocked := d.journalBlocker(t, act); blocked != "" {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyMigrationInProgress, blocked)
		return
	}
	if st := act.runtime.Status(); !migratableNsStatus(st) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyNamespaceBusy,
			t.T("deps.msg.ns.settleBeforeMigratingDep", "status", string(st), "id", string(id)))
		return
	}
	// Claimed as longOpMigration, NEVER through tryLongOp: that helper labels
	// its holder longOpRequest, which the five lifecycle routes TOLERATE — so a
	// mislabeled migration would let a namespace Start and the per-app toggles
	// run right beside the volume rewrite this lock exists to protect. See the
	// trap on longOpMigration. Ownership transfers into the goroutine below.
	if !d.longOp.TryLock(longOpMigration) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeLongOpInProgress,
			t.Render(d.longOp.Holder().busyMessage()))
		return
	}
	if act.dockerClient == nil {
		d.longOp.Unlock()
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}
	env := d.depsEnvFor(act)
	m, found := d.migratorFor(id)
	if !found {
		d.longOp.Unlock()
		writeInternalError(w, fmt.Errorf("no migrator wired for dependency %q", id))
		return
	}
	evt := func(typ, phase string, cur, total int, pct float64, m msg.Message) api.EventDto {
		return depsEvent(typ, nsID, id, phase, cur, total, pct, m)
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
		0, 0, 0, versionPairMessage(from, to)))
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
		writeErrorCode(w, http.StatusConflict, api.ErrCodeDependencyPreflightFailed, renderPlanError(t, err))
		return
	}
	steps := len(plan.Steps)
	d.setDepsMigration(nsID, &api.DependencyMigrationDto{ID: string(id), StepCount: steps})
	rt := act.runtime
	// No recover() in here, and that is a decision rather than an oversight: it
	// is parity with every other bgWg goroutine (both snapshot paths, the
	// workspace-snapshot download), and for THIS one a crash is the RECOVERABLE
	// outcome. The write-ahead journal is already on disk, the process is
	// restarted (systemd in server mode, the wrapper on the desktop), and boot
	// recovery rolls the interrupted migration back before the runtime touches
	// the data. Swallowing the panic instead would leave a live daemon holding
	// an open journal that nothing retries until the next launcher start — a
	// namespace that refuses every start with no way forward from inside the
	// app. The deferred unlock and the cleared progress below still run, since
	// deferred calls run while the panic unwinds.
	d.bgWg.Go(func() {
		defer d.longOp.Unlock()
		defer d.setDepsMigration(nsID, nil)
		d.broadcastEvent(evt(api.EventDepsMigrationStart, "", 0, steps, 0, versionPairMessage(from, to)))
		progress := func(step string, i, n int, pct float64, m msg.Message) {
			d.setDepsMigration(nsID, &api.DependencyMigrationDto{
				ID: string(id), Step: step, StepIndex: i, StepCount: n, Percent: pct, MessageMsg: m,
			})
			d.broadcastEvent(evt(api.EventDepsMigrationProgress, step, i, n, pct, m))
		}
		runErr := migrate.Run(d.bgCtx, rt, journal, plan, progress)
		var fe *migrate.FinalizeError
		switch {
		case runErr == nil:
			//nolint:gosec // G706: id passed deps.Lookup (a fixed registry) and the images come from the resolved bundle/pins
			slog.Info("Dependency migration finished", "dependency", id, "from", from, "to", to)
			d.broadcastEvent(evt(api.EventDepsMigrationComplete, "", steps, steps, 100,
				msg.New("deps.msg.event.migrated", "id", string(id), "image", to)))
		case errors.As(runErr, &fe):
			// The data has moved and the pin says so; only the tidy-up or the
			// restart failed, so this is a completion with a warning.
			//nolint:gosec // G706: id passed deps.Lookup (a fixed registry)
			slog.Warn("Dependency migration committed with a finalize failure", "dependency", id, "err", fe.Err)
			d.broadcastEvent(evt(api.EventDepsMigrationComplete, "", steps, steps, 100,
				msg.New("deps.msg.event.migratedWithWarning",
					"id", string(id), "image", to, "error", fe.Err.Error())))
		default:
			//nolint:gosec // G706: id passed deps.Lookup (a fixed registry)
			slog.Error("Dependency migration failed", "dependency", id, "err", runErr)
			// The failure is a step's own error — a docker refusal, a psql
			// stderr, a wrapped cancellation — and it is already final text.
			d.broadcastEvent(evt(api.EventDepsMigrationError, "", 0, steps, 0,
				msg.New("deps.msg.passthrough", "text", runErr.Error())))
		}
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.ActionResultDto{
		Success: true,
		Message: t.T("deps.msg.action.migrationStarted", "id", string(id), "image", to),
	})
}

// versionPairMessage is the "17.5 → 18.6" line the start and preparing events
// carry. It has no words in it — two image references and an arrow — so it
// gets one key with both as parameters rather than being assembled from
// pieces: the arrow is punctuation a locale may legitimately want to change
// (and a right-to-left one certainly does).
func versionPairMessage(from, to string) msg.Message {
	return msg.New("deps.msg.event.versionPair", "from", from, "to", to)
}
