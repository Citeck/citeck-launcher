package migrate

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// The rollback is the ONE action in this launcher that makes a retained volume
// live again, and it is deliberately NOT a journalled multi-step operation.
//
// The whole sequence is: refuse unless the preconditions hold (this file),
// stop the namespace, write the pin back — image and generation, one atomic
// write, with the rollback target cleared — and reload, starting again if it
// was running. Nothing is created and nothing is deleted: the retained volume
// is already there, and the volume the namespace is leaving is kept exactly as
// it is. So there is no partial state a journal could describe and no leftover
// a rollback-of-the-rollback could remove — and building one would be actively
// harmful, because a journal surviving a crash makes the daemon's boot recovery
// refuse to start the namespace until it is cleared, which for an operation
// with nothing to clean up would strand a namespace over a write that either
// landed or did not.
//
// The invariant it ends is worth stating plainly rather than letting it slip:
// "the original data volume is only ever READ, and rollback is
// delete-what-we-created" is a property OF A MIGRATION, for the duration of
// that migration. It is not a promise that the launcher will never write to a
// retained volume again — the whole point of retaining it is that the operator
// may decide to go back to it. This ends that guarantee deliberately, in the
// open, with a confirmation; it does not weaken it while a migration is
// running, because a rollback cannot even be started while a journal is open
// (that refusal is the daemon's, see RefusedPreflight).

// RollbackStepIDs are the ids of the rollback's progress steps, in order.
//
// The rollback reuses the MIGRATION's progress channel with a three-step list
// rather than growing a rendering path of its own, so the CLI's event renderer
// and the dialog's progress screen work unchanged and only the title differs.
// Every id here is a locale key in both assets.
//
// "stop-namespace" is deliberately the SAME id both migration plans already
// use: it is literally the same step, and a second spelling would be one
// sentence translated twice, in 16 files.
func RollbackStepIDs() []string {
	return []string{"stop-namespace", "switch-generation", "start-namespace"}
}

// BundleOlderNotice is what an operator is told when the bundle offers a
// version OLDER than the one this namespace's data runs on, ACROSS a data
// format, and there is no rollback target to offer.
//
// It is not an upgrade that is held back — there is nothing to migrate and
// nothing to wait for — so it must not borrow the words of one: no "update the
// launcher", and above all no `citeck deps upgrade`, which would refuse the
// operator at the end of a trip the message sent them on.
//
// Note what is NOT here: a same-format backwards move (a bundle reverting a
// patch bump) has no notice at all, because it is not held back. It applies on
// the next start, exactly as it always has (user ruling, 2026-09-09).
//
// It takes no dependency name, unlike its sibling and unlike the two vendor
// refusals: there is no command to interpolate one into, and the sentence is
// rendered on the dependency's own row.
func BundleOlderNotice(from, to string) string {
	return fmt.Sprintf(
		"the bundle offers %s, which is older than the %s this namespace's data runs on and cannot "+
			"read it; the data stays on %s. Going back would need the data as %s wrote it, and this "+
			"namespace has no volume recorded from that version.",
		to, from, from, to)
}

// BundleOlderRollbackNotice is the same state with one difference that changes
// the advice completely: the version the bundle is offering is the one this
// namespace MIGRATED FROM, so the volume that migration left is still on disk
// and going back is an action rather than a wish.
//
// dep is the dependency ID and not a display name, because it is interpolated
// into a command the operator will type — the same rule VendorPathProblem
// follows.
func BundleOlderRollbackNotice(dep, from, to string) string {
	return fmt.Sprintf(
		"the bundle offers %s, which is older than the %s this namespace's data runs on and cannot "+
			"read it; the data stays on %s. This is the version this namespace migrated from, so "+
			"`citeck deps rollback %s` puts it back on the volume that migration left — everything "+
			"written since then stays on the newer volume and is not read again.",
		to, from, from, dep)
}

// RetainedVolumeGoneProblem is why a rollback cannot be taken: the volume the
// migration retained is no longer there.
//
// The launcher itself tells the operator they may reclaim that volume once they
// trust the new version, so this is a state it actively creates. Naming the
// volume is what makes the answer actionable instead of mysterious.
//
// It is exported because two surfaces answer this same question and must not
// word it differently: the preflight below, and the dependency LIST, which
// judges every rollback offer on every request without building a preflight at
// all. One sentence, one place to reword it.
func RetainedVolumeGoneProblem(id deps.ID, volume, image string) string {
	return fmt.Sprintf("volume %s is gone, so there is no %s data from %s to go back to", volume, id, image)
}

// VolumeCheckFailedProblem is why a rollback could not be judged at all: the
// launcher could not ask whether the retained volume is still there. Exported
// for the same reason as RetainedVolumeGoneProblem, and deliberately distinct
// from it — "I could not ask" is not "it is gone".
func VolumeCheckFailedProblem(volume string, err error) string {
	return fmt.Sprintf("cannot check volume %s: %v", volume, err)
}

// RollbackPreflight reports everything that would refuse or endanger putting a
// dependency back on the state its last completed migration recorded as
// previous. It never mutates.
//
// prev is the target — DependencyState.Previous() of the namespace's current
// pin — and the current pin is read from the env, so the two halves of the move
// cannot be passed in disagreeing with each other.
//
// SpaceChecked stays FALSE and every size stays zero: nothing is created, so
// there is nothing to measure. Both renderers skip an unmeasured result, which
// is exactly what that field was introduced for.
//
// Four of the refusals in the design are NOT here and cannot be: an open
// journal, another long operation, and a namespace that is neither RUNNING nor
// STOPPED are conditions the DAEMON knows before it touches Docker, and they
// reach the confirm screen through RefusedPreflight as its own input rather
// than as a 409 after the click.
func RollbackPreflight(ctx context.Context, env Env, id deps.ID, prev deps.DependencyState) PreflightResult {
	cur := env.DependencyState(id)
	res := NewPreflightResult(cur.Image, prev.Image)
	res.WasRunning = env.IsRunning()

	d, registered := deps.Lookup(id)
	if !registered {
		res.Problems = append(res.Problems, fmt.Sprintf("%s is not a registered dependency", id))
		return res
	}
	if prev.Image == "" {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"%s has no recorded previous version to roll back to", id))
		return res
	}
	retained := deps.VolumeName(d, prev.Gen())
	if retained == "" {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"%s has no data volume of its own, so there is no generation to switch back to", id))
		return res
	}
	// A target that does not go back a generation is not a rollback at all: the
	// volume it would "return to" is the one the namespace is already on, and
	// the frozen-volume warning below would then name the live data. It can
	// only come from a corrupted state file or a commit that recorded the wrong
	// side of the move, and both are better refused than described confidently
	// and wrongly.
	if prev.Gen() >= cur.Gen() {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"the recorded rollback target of %s is volume generation %d, which is not older than the "+
				"generation %d the namespace runs on; there is nothing to switch back to",
			id, prev.Gen(), cur.Gen()))
		return res
	}

	exists, err := env.VolumeExists(ctx, retained)
	switch {
	case err != nil:
		res.Problems = append(res.Problems, VolumeCheckFailedProblem(retained, err))
		return res
	case !exists:
		res.Problems = append(res.Problems, RetainedVolumeGoneProblem(id, retained, prev.Image))
		return res
	}

	res.checkRetainedDataVersion(ctx, env, d, retained, prev)
	if len(res.Problems) > 0 {
		// The consequence warnings below describe a rollback that is going to
		// happen. Printing them beside a refusal would describe one that is not.
		return res
	}

	res.warnRollbackConsequences(ctx, env, deps.VolumeName(d, cur.Gen()), retained, cur.Image, prev.Image)
	res.OK = true
	return res
}

// checkRetainedDataVersion asks the retained volume what it actually holds.
//
// The pin says what the launcher THINKS it left there; the data's own marker is
// what is on disk. A disagreement means somebody deleted and recreated that
// volume, and starting the older server on it is not a rollback — it is a fresh
// cluster wearing the old version's name.
func (res *PreflightResult) checkRetainedDataVersion(
	ctx context.Context, env Env, d deps.Descriptor, volume string, prev deps.DependencyState,
) {
	rel, major, ok := retainedVersionMarker(d, prev)
	if !ok {
		return
	}
	raw, err := env.ReadVolumeFile(ctx, volume, rel)
	if err != nil {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"cannot read %s/%s, so the retained volume does not hold the cluster %s names: %v",
			volume, rel, prev.Image, err))
		return
	}
	onDisk := strings.TrimSpace(raw)
	if n, convErr := strconv.Atoi(onDisk); convErr != nil || n != major {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"volume %s holds PostgreSQL %q, not the cluster %s names: it was deleted and recreated, "+
				"and starting the older server on it would not be a rollback",
			volume, onDisk, prev.Image))
	}
}

// retainedVersionMarker answers where inside a dependency's data volume the
// data announces its OWN version, for the version a rollback would restore.
//
// ok=false is the honest answer for a dependency whose data carries no such
// marker — neither RabbitMQ nor ZooKeeper writes one — and the check is then
// skipped entirely rather than inventing a path, which would refuse every
// rollback of those two. It is also false for a previous image whose tag cannot
// be read: the pin says the namespace really ran it, so it is still a valid
// target, but there is nothing to compare the data against.
//
// PostgreSQL is named explicitly rather than through a Descriptor method for
// the same reason CopyPreflight passes "" to checkExistingTarget: one
// dependency out of five has a marker, and a seam on the registry for it would
// be an interface every descriptor has to answer "no" to.
func retainedVersionMarker(d deps.Descriptor, prev deps.DependencyState) (rel string, major int, ok bool) {
	if d.ID() != deps.Postgres {
		return "", 0, false
	}
	v, parsed := d.ParseVersion(prev.Image)
	if !parsed {
		return "", 0, false
	}
	return deps.PostgresLayoutFor(v.Major).PGVersionRel, v.Major, true
}

// warnRollbackConsequences is the confirmation's whole reason to exist. Three
// facts, none of which the operator can derive from the button they clicked:
// the data is as of the migration, everything written since then lives on a
// volume that is KEPT and never read again, and there is no roll-forward.
//
// They are WARNINGS and not prose in the dialog body because warnings are where
// the confirm screens already put "you must read this before pressing the
// button".
//
// The date the migration finished is deliberately not here: this function has
// no result record to read it from, and the timestamp reaches the operator
// through the rollback offer's own MigratedAt field. Saying "when the migration
// finished" is true without one.
func (res *PreflightResult) warnRollbackConsequences(
	ctx context.Context, env Env, frozen, retained, from, to string,
) {
	// Checked, never enforced (ruling on OPEN QUESTION 6): the pin names an
	// image that really ran, so it names a real registry — but the host may
	// have pruned it and the registry may be unreachable, and without this the
	// failure lands after the namespace is already stopped. A heads-up that
	// costs nothing to ignore.
	if !env.ImageExists(ctx, to) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"image %s is not present locally; the rollback will pull it, and a registry it cannot "+
				"reach would leave the namespace stopped", to))
	}
	res.Warnings = append(res.Warnings,
		// The data is as of the migration.
		fmt.Sprintf("the namespace will run %s again, on the data in volume %s as it was when the "+
			"migration to %s finished", to, retained, from),
		// Everything since then lives on a volume that is KEPT and never read.
		fmt.Sprintf("everything written since then is in volume %s: the launcher keeps it and will "+
			"not read it again, so that data becomes unreachable", frozen),
		// And it is one-way.
		fmt.Sprintf("there is no roll-forward: to go back to %s you would migrate again, from the "+
			"%s data, into a new volume", from, to))
}
