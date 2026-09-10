package daemon

import (
	"context"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// dependencyEditLocked reports whether an app-config edit would move a
// registered infra dependency to a version the launcher refuses to put on it,
// returning the dependency, the image its data is pinned to, and WHICH rule
// refused.
//
// The generator's pin gate (resolveDependencyImage) runs BEFORE
// EditedAppPatches are applied — they land at the tail of Generate — so a
// patch is the one door such an image could still walk through: a
// `citeck edit postgres` (or the gear editor) setting `image: postgres:18`
// would put PostgreSQL 18 onto a 17 data directory with no migration.
//
// It decides on VERSION FACTS ALONE — a pin, an image, and the registry — so
// it is a pure question a test can drive with nothing but a Runtime. Two of
// the three rules it can fire are about DATA that may or may not be there, and
// resolving that half needs Docker; the reason says which, and the caller
// (dependencyEditPinFollows) answers it. The floor deliberately does not need
// the caller at all, which is what makes a floor refusal work with Docker
// down.
//
// Three shapes pass before any rule is asked. An edit with no image names no
// VERSION, which is the only question this gate asks. (It is NOT the same as
// "the image is unchanged": handlePutAppConfig unmarshals the whole submitted
// YAML into a def, and ApplicationDef.Image has no `omitempty`, so DiffAppDef
// records `image: ""` and the merged def ends up with a BLANK image that fails
// at pull time. That is a malformed def, not a version move onto data it does
// not fit, and refusing it here would answer the wrong question with the wrong
// message.) With no pin there is no recorded version to refuse against. And an
// edit that names the SAME VERSION as the pin chooses no version at all — see
// editKeepsVersion — which matters for the floor alone, the one rule that does
// not otherwise compare the two sides: the editor round-trips the whole def,
// so every save of a memory limit on a stand seeded below the floor carries
// that image with it, and refusing those would leave the operator unable to
// touch the namespace at all.
func dependencyEditLocked(rt *namespace.Runtime, name string, newDef appdef.ApplicationDef) (dependencyEditRefusal, bool) {
	d, ok := deps.ByApp(name)
	if !ok || newDef.Image == "" {
		return dependencyEditRefusal{}, false
	}
	pinned, has := rt.DependencyPins()[d.ID()]
	if !has || pinned == "" || editKeepsVersion(d, pinned, newDef.Image) {
		return dependencyEditRefusal{}, false
	}
	r := dependencyEditRefusal{desc: d, app: name, pinned: pinned, wanted: newDef.Image}
	switch {
	case deps.BelowSupportFloor(d, newDef.Image):
		// FIRST, and answered without the caller: a version below the floor is
		// one the platform has not been tested on, which is a fact about the
		// version and not about this namespace's disk.
		r.reason = editReasonBelowFloor
	case editMovesBackwards(d, pinned, newDef.Image):
		// A backwards move on data that exists is refused whatever the
		// components say, patch reverts included (user ruling, 2026-09-10). A
		// patch downgrade is usually harmless and nowhere guaranteed:
		// PostgreSQL documents only the forward direction and some minors need
		// a REINDEX, and a RabbitMQ patch that enabled a feature flag the older
		// release does not know refuses to start on that data. Whether it is
		// ALSO breaking changes nothing about the refusal and everything about
		// what the operator is told.
		//
		// This is the EDIT gate only. The generator's rule is unchanged: a
		// bundle offering an older patch still applies silently, because a gate
		// stricter than the generator forbids by hand what the bundle does on
		// its own.
		r.reason = editReasonBackwards
		r.breaking = deps.Breaking(d, pinned, newDef.Image)
	case deps.Breaking(d, pinned, newDef.Image):
		r.reason = editReasonBreakingForward
	default:
		return dependencyEditRefusal{}, false
	}
	return r, true
}

// editKeepsVersion reports that an edit chooses no version: the pinned and the
// edited reference name the SAME version, so nothing this gate rules on has
// moved and no rule of it may fire (user ruling, 2026-09-10: "do not compare
// images byte for byte, only the version in the tags").
//
// Byte identity is kept as one arm of it, and it is the only thing that can be
// said when neither tag parses — but on its own it is too narrow for the job.
// It would refuse `postgres:15.6` → `myreg/postgres:15.6` on a stand seeded
// below the support floor, i.e. refuse an operator on a private-registry or
// air-gapped stand the one edit that keeps their dependency running exactly
// the version it already runs.
//
// deps.Version is deliberately NOT compared with ==: it carries Raw, the whole
// tag, so "4.2.9-management" and "4.2.9" would compare unequal on a difference
// of spelling rather than of version. Neither-older-than-the-other reuses the
// ordering deps itself defines, so this cannot drift from the direction rule
// below. The consequence is real and accepted: dropping RabbitMQ's
// `-management` suffix changes which image runs and still passes, because it
// moves no version — the thing this gate exists to hold.
//
// An unparsable tag on either side is NOT the same version as anything,
// including another unparsable tag: "latest" names no version, so claiming two
// of them agree would be an invention. Such a pair falls through to
// deps.Breaking, which holds it and says so in a message about the tag.
func editKeepsVersion(d deps.Descriptor, pinned, wanted string) bool {
	if pinned == wanted {
		return true
	}
	from, okFrom := d.ParseVersion(pinned)
	to, okTo := d.ParseVersion(wanted)
	return okFrom && okTo && !deps.MovesBackwards(from, to) && !deps.MovesBackwards(to, from)
}

// editMovesBackwards asks the DIRECTION question of two image references,
// which deps.MovesBackwards asks of two parsed versions.
//
// An unparsable tag on either side has no direction — nothing orders "latest"
// — so it is not backwards. Such a pair is held by deps.Breaking instead, and
// its message is about the tag, which is the one the operator can act on.
func editMovesBackwards(d deps.Descriptor, pinned, wanted string) bool {
	from, okFrom := d.ParseVersion(pinned)
	to, okTo := d.ParseVersion(wanted)
	return okFrom && okTo && deps.MovesBackwards(from, to)
}

// dependencyEditReason names WHICH rule refused an image edit.
//
// It exists because the rules do not all ask the same kind of question: the
// support floor is decided by versions alone, while "backwards" and "breaking"
// only matter while there is DATA for the version to land on. Reporting the
// reason is what lets dependencyEditLocked stay Docker-free and the caller
// resolve the data-dependent half.
type dependencyEditReason int

const (
	// editReasonBelowFloor: the edit names a version older than the oldest one
	// the platform is tested on (deps.Descriptor.SupportFloor).
	editReasonBelowFloor dependencyEditReason = iota + 1
	// editReasonBackwards: the edit names an OLDER version than the pin.
	editReasonBackwards
	// editReasonBreakingForward: the edit names a newer version whose data
	// format differs (deps.Breaking).
	editReasonBreakingForward
)

// dataDependent reports whether the refusal stands only while the dependency's
// data volume is there. The floor is the one rule that does not: an untested
// version is untested whether or not this namespace has anything on disk.
func (r dependencyEditReason) dataDependent() bool { return r != editReasonBelowFloor }

// dependencyEditRefusal is a refused image edit and the facts its message
// needs: which dependency, which way the move goes, and which rule fired.
type dependencyEditRefusal struct {
	desc           deps.Descriptor
	app            string
	pinned, wanted string
	reason         dependencyEditReason
	// breaking is meaningful for editReasonBackwards only: whether the older
	// version ALSO cannot read the data. It decides between two true sentences,
	// never whether the edit is refused.
	breaking bool
}

// message is what the operator reads. It has four forms, and each is true of
// exactly one of them.
//
// It answers a LIST of sentences rather than one string, and that is the whole
// difference from the version before it: the two BACKWARDS forms end in a way
// back, which is a second, independent statement — whether there is a retained
// volume, and which — established by the CALLER (dependencyEditWayBack). Glued
// on with an em dash it was one English sentence with a clause of unknown
// content in the middle, which is the one shape a translator cannot rearrange.
// Two whole sentences, joined by the renderer, each translatable on its own.
//
// The two BACKWARDS forms do not name `citeck deps upgrade`: that command
// refuses a backwards pair (DEPENDENCY_BACKWARDS), so the message would end a
// dead end of exactly the class this codebase keeps closing.
//
// The FLOOR form names no way back at all, deliberately: it is not a direction
// problem. An unsupported version is unsupported from either side, so offering
// a rollback onto it — or a migration to it — would be offering the thing that
// was just refused.
func (r dependencyEditRefusal) message(rollback *api.DependencyRollbackDto) []msg.Message {
	switch {
	case r.reason == editReasonBelowFloor:
		offered, _ := r.desc.ParseVersion(r.wanted)
		return []msg.Message{msg.New("deps.msg.edit.belowFloor",
			"app", r.app, "image", r.wanted, "version", offered.String(),
			"floor", r.desc.SupportFloor().String(), "id", string(r.desc.ID()))}
	case r.reason == editReasonBackwards && r.breaking:
		return []msg.Message{
			msg.New("deps.msg.edit.backwardsBreaking",
				"app", r.app, "pinned", r.pinned, "wanted", r.wanted),
			r.wayBack(rollback),
		}
	case r.reason == editReasonBackwards:
		// NOT "cannot read that data": an older patch of the same series reads
		// it perfectly well, and a message that says otherwise teaches the
		// operator something false about their own stand. What is true is the
		// rule itself.
		return []msg.Message{
			msg.New("deps.msg.edit.backwards",
				"app", r.app, "pinned", r.pinned, "wanted", r.wanted),
			r.wayBack(rollback),
		}
	}
	return []msg.Message{msg.New("deps.msg.edit.breakingForward",
		"app", r.app, "pinned", r.pinned, "wanted", r.wanted, "id", string(r.desc.ID()))}
}

// wayBack turns the rollback offer into the second half of the backwards
// refusal: what this namespace can do instead of the edit it was refused.
//
// Three of the four arms state a fact and the fourth deliberately states none.
// Whether there is a previous state at all, and which volume it names, are
// answered from memory and are therefore always known; whether that volume is
// still on disk needs a VolumeExists, and that call can fail. "I could not
// ask" is not "it is gone" — printing the second on the strength of a socket
// error tells the operator their data is destroyed — so ONLY the exact
// sentence migrate.RetainedVolumeGoneProblem produces is read as the volume
// being gone, and every other unavailable offer falls through to the hedged
// wording this message carried before it could ask at all, which is wrong
// about nothing. Comparing against that constructor rather than re-spelling
// the sentence is also what keeps the three surfaces that print it — this one,
// the dependency list and the rollback preflight — from drifting apart.
func (r dependencyEditRefusal) wayBack(offer *api.DependencyRollbackDto) msg.Message {
	id := r.desc.ID()
	switch {
	case offer == nil:
		return msg.New("deps.msg.edit.wayBackNone")
	case offer.Available:
		return msg.New("deps.msg.edit.wayBackRollback", "id", string(id), "volume", offer.Volume)
	case offer.ProblemMsg.Key == migrate.RetainedVolumeGoneProblem(id, offer.Volume, offer.ToImage).Key:
		// The KEY, not the rendered prose: by the time this runs the offer may
		// already have been rendered in any of eight languages, and comparing
		// sentences would silently start failing in seven of them. Comparing
		// against the constructor rather than re-spelling the key is what keeps
		// the three surfaces that print it from drifting apart.
		return offer.ProblemMsg
	default:
		return msg.New("deps.msg.edit.wayBackHedged", "id", string(id))
	}
}

// dependencyEditWayBack is the caller's half of the message above: the rollback
// offer for a refused BACKWARDS edit, or nil when there is nothing to go back
// to.
//
// It is separate from dependencyEditLocked, and lives on the Daemon, because
// rollbackOffer reaches Docker (VolumeExists) while the gate that decides
// WHETHER an edit is refused must stay a pure question a test can drive with
// nothing but a Runtime. The other two refusals never ask this one: a FORWARD
// refusal names the migration and nothing else, and a FLOOR refusal must not
// touch Docker at all — it is the refusal that has to work with Docker down.
func (d *Daemon) dependencyEditWayBack(ctx context.Context, r dependencyEditRefusal) *api.DependencyRollbackDto {
	if r.reason != editReasonBackwards {
		return nil
	}
	act := d.active()
	if act.runtime == nil {
		// Unreachable — handlePutAppConfig has already required a runtime —
		// but nil here MEANS "there is nothing to go back to", and this
		// function may only claim what it established. An offer with no
		// verdict degrades to the hedged wording instead.
		return &api.DependencyRollbackDto{}
	}
	return d.rollbackOffer(ctx, act, r.desc, act.runtime.DependencyStates()[r.desc.ID()])
}

// dependencyPinMove is the pin write a no-longer-refused edit owes once it is
// applied: the dependency, the state to store, and what it moved away from.
//
// It is a VALUE the caller runs at a chosen moment rather than a write the
// decision makes for itself, because the two must not happen at the same
// point: the decision belongs before anything is persisted (an edit that is
// refused must leave no trace), and the write belongs after the edit itself
// landed and before the reload that reads it.
type dependencyPinMove struct {
	id     deps.ID
	state  deps.DependencyState
	from   string
	volume string
}

// apply records the pin and says so.
//
// SetDependencyState persists inline and reports nothing back: a refused write
// stays OWED (persistUnderLock keeps r.dirty set, the loop retries it, and
// Runtime.StateWriteError tells the operator the change was applied but not
// saved). That is what every other durable edit on this path does —
// UpdateAppDef, two lines earlier in the same handler, is one — and turning
// this one into a request failure would be a third behavior: the edit really
// was applied, and the store is not something the caller can do anything
// about.
func (m dependencyPinMove) apply(rt *namespace.Runtime) {
	rt.SetDependencyState(m.id, m.state)
	slog.Info("Dependency image edited by hand with no data to protect; the pin follows it",
		"dependency", m.id, "volume", m.volume, "from", m.from, "to", m.state.Image)
}

// dependencyEditPinFollows resolves the DATA half of a refusal whose reason is
// data-dependent (backwards, or breaking forward): both exist to stop a version
// landing on a data directory it must not land on, so when the launcher can
// PROVE there is no such data directory, there is nothing left to refuse. It
// answers the pin write that edit owes, or nil to leave the refusal standing.
//
// It is never asked about a floor refusal — an untested version is untested
// whether or not this namespace has anything on disk, and asking would make
// that refusal depend on a reachable Docker.
//
// This is the state the user described: a volume deleted from the Volumes page
// because it held nothing worth keeping (with RabbitMQ that is routine), a pin
// still naming the version it was seeded from, and an operator with no way to
// say which version the next start should bring up — `citeck deps upgrade`
// would migrate data that is not there, and the edit was refused in its
// favor. (A namespace that has never run has no pin at all and never reaches
// the gate; that path is untouched.)
//
// Three rules, each of which is the difference between this and a hole in the
// gate:
//
//   - PROOF means a definite answer. Any error fails CLOSED — an unreachable
//     Docker is "I could not ask", never "there is nothing there", the same
//     asymmetry seeding draws between seedUnknown and seedNoData and the
//     rollback offer between VolumeCheckFailedProblem and
//     RetainedVolumeGoneProblem;
//   - a volume that EXISTS is data, even if it is empty. The Env seam has no
//     directory listing (which is why the migration preflight's "empty source
//     volume" warning was left unimplemented), so "it is probably empty" would
//     be a guess whose cost is a version started on a cluster it cannot read.
//     Deleting the volume is the operator's unambiguous way to say there is
//     nothing in it;
//   - a dependency with NO volume of its own keeps the refusal. Keycloak's
//     VolumeBase is "" because its state lives in the namespace's PostgreSQL
//     database, so "there is no volume" there means the data is somewhere
//     else, not that there is none.
func (d *Daemon) dependencyEditPinFollows(ctx context.Context, rt *namespace.Runtime, r dependencyEditRefusal) *dependencyPinMove {
	st := rt.DependencyStates()[r.desc.ID()]
	volume := deps.VolumeName(r.desc, st.Gen())
	if volume == "" {
		return nil
	}
	exists, err := d.depsEnvFor(d.active()).VolumeExists(ctx, volume)
	if err != nil {
		slog.Warn("Dependency image edit: could not check whether the data volume is there; keeping the version locked",
			"dependency", r.desc.ID(), "volume", volume, "err", err)
		return nil
	}
	if exists {
		return nil
	}
	// Only the IMAGE follows the edit. The generation names the volume the
	// next start mounts and PrevImage/PrevVolumeGen name a retained volume
	// that may still be on disk; an image edit is evidence about neither. Same
	// rule syncDependencyPinsUnderLock follows when a running container
	// settles a non-breaking bump.
	moved := st
	moved.Image = r.wanted
	return &dependencyPinMove{id: r.desc.ID(), state: moved, from: r.pinned, volume: volume}
}
