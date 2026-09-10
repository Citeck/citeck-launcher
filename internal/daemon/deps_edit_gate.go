package daemon

import (
	"context"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// dependencyEditLocked reports whether an app-config edit would move a
// registered infra dependency to a breaking image version, returning the
// dependency id and the image its data is pinned to.
//
// The generator's pin gate (resolveDependencyImage) runs BEFORE
// EditedAppPatches are applied — they land at the tail of Generate — so a
// patch is the one door a breaking image could still walk through: a
// `citeck edit postgres` (or the gear editor) setting `image: postgres:18`
// would put PostgreSQL 18 onto a 17 data directory with no migration.
//
// Two shapes pass. An edit with no image names no VERSION, which is the only
// question this gate asks. (It is NOT the same as "the image is unchanged":
// handlePutAppConfig unmarshals the whole submitted YAML into a def, and
// ApplicationDef.Image has no `omitempty`, so DiffAppDef records `image: ""`
// and the merged def ends up with a BLANK image that fails at pull time. That
// is a malformed def, not a version move onto data it does not fit, and
// refusing it here would answer the wrong question with the wrong message.)
// And with no pin there is no recorded version to refuse against.
func dependencyEditLocked(rt *namespace.Runtime, name string, newDef appdef.ApplicationDef) (dependencyEditRefusal, bool) {
	d, ok := deps.ByApp(name)
	if !ok || newDef.Image == "" {
		return dependencyEditRefusal{}, false
	}
	pinned, has := rt.DependencyPins()[d.ID()]
	if !has || pinned == "" || !deps.Breaking(d, pinned, newDef.Image) {
		return dependencyEditRefusal{}, false
	}
	return dependencyEditRefusal{
		desc: d, app: name, pinned: pinned, wanted: newDef.Image,
		// The rule is unchanged — the FORMAT question, deps.Breaking, exactly
		// as before (a same-format backwards edit, i.e. a reverted patch bump,
		// still passes, because the generator applies such a bundle silently
		// and a gate stricter than the generator forbids by hand what the
		// bundle does on its own). Direction changes nothing about WHETHER the
		// edit is refused; it changes what the operator is told to do instead.
		backwards: deps.BundleOlder(d, pinned, newDef.Image),
	}, true
}

// dependencyEditRefusal is a refused image edit and the two facts the message
// needs: which dependency, and which way the move goes.
type dependencyEditRefusal struct {
	desc           deps.Descriptor
	app            string
	pinned, wanted string
	backwards      bool
}

// message is what the operator reads. It has two forms because
// `citeck deps upgrade` will never move data backwards: pointing a backwards
// edit at it would refuse the operator (DEPENDENCY_BACKWARDS) at the end of a
// trip this message sent them on. The one deliberate way back is the rollback
// onto the volume the migration retained, and rollback names a volume that may
// or may not still be there — so that half of the sentence is a fact the
// CALLER establishes (dependencyEditWayBack) and this one only words.
func (r dependencyEditRefusal) message(rollback *api.DependencyRollbackDto) string {
	if r.backwards {
		return fmt.Sprintf("%s runs on %s; %s is older and cannot read that data, and editing the "+
			"image moves no data at all — %s", r.app, r.pinned, r.wanted, r.wayBack(rollback))
	}
	return fmt.Sprintf("%s runs on %s; moving its data to %s is a version migration — "+
		"run `citeck deps upgrade %s` (or use the Dependencies dialog) instead of editing the image",
		r.app, r.pinned, r.wanted, r.desc.ID())
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
func (r dependencyEditRefusal) wayBack(offer *api.DependencyRollbackDto) string {
	id := r.desc.ID()
	switch {
	case offer == nil:
		return "this namespace has no retained volume from an older version, so there is nothing to go back to"
	case offer.Available:
		return fmt.Sprintf("the way back is `citeck deps rollback %s`, which puts the namespace on "+
			"volume %s, the data as it was when the migration finished", id, offer.Volume)
	case offer.Problem == migrate.RetainedVolumeGoneProblem(id, offer.Volume, offer.ToImage):
		return offer.Problem
	default:
		return fmt.Sprintf("`citeck deps rollback %s` puts the namespace back on the volume its "+
			"migration retained, if it made one", id)
	}
}

// dependencyEditWayBack is the caller's half of the message above: the rollback
// offer for a refused BACKWARDS edit, or nil when there is nothing to go back
// to.
//
// It is separate from dependencyEditLocked, and lives on the Daemon, because
// rollbackOffer reaches Docker (VolumeExists) while the gate that decides
// WHETHER to refuse must stay a pure question a test can drive with nothing but
// a Runtime. A forward refusal never asks: it names the migration and nothing
// else, so a plain image edit on a stopped namespace still costs no container.
func (d *Daemon) dependencyEditWayBack(ctx context.Context, r dependencyEditRefusal) *api.DependencyRollbackDto {
	if !r.backwards {
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
