package daemon

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
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
		id: d.ID(), app: name, pinned: pinned, wanted: newDef.Image,
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
	id             deps.ID
	app            string
	pinned, wanted string
	backwards      bool
}

// message is what the operator reads. It has two forms because
// `citeck deps upgrade` will never move data backwards: pointing a backwards
// edit at it would refuse the operator (DEPENDENCY_BACKWARDS) at the end of a
// trip this message sent them on. The one deliberate way back is the rollback
// onto the volume the migration retained — which may not exist, so the
// sentence says "if there is one" rather than promising it; the Dependencies
// dialog and `citeck deps` are where the offer itself is reported.
func (r dependencyEditRefusal) message() string {
	if r.backwards {
		return fmt.Sprintf("%s runs on %s; %s is older and cannot read that data — "+
			"`citeck deps rollback %s` puts the namespace back on the volume its migration retained, "+
			"if it made one, and editing the image moves no data at all",
			r.app, r.pinned, r.wanted, r.id)
	}
	return fmt.Sprintf("%s runs on %s; moving its data to %s is a version migration — "+
		"run `citeck deps upgrade %s` (or use the Dependencies dialog) instead of editing the image",
		r.app, r.pinned, r.wanted, r.id)
}
