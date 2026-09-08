package daemon

import (
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
// An edit with no image says nothing about the version — DiffAppDef records no
// image at all when the edit did not change it, so the generated def's own
// (already pin-gated) image stands — and with no pin there is no recorded
// version to refuse against; both pass.
func dependencyEditLocked(rt *namespace.Runtime, name string, newDef appdef.ApplicationDef) (deps.ID, string, bool) {
	d, ok := deps.ByApp(name)
	if !ok || newDef.Image == "" {
		return "", "", false
	}
	pinned, has := rt.DependencyPins()[d.ID()]
	if !has || pinned == "" || !deps.Breaking(d, pinned, newDef.Image) {
		return "", "", false
	}
	return d.ID(), pinned, true
}
