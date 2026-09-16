package deps

// This file is the ONE place the support floors live: the oldest version of
// each dependency the platform has been tested on, and therefore the oldest
// the launcher will let an operator CHOOSE. The methods are gathered here
// rather than spread across the five descriptor files precisely because the
// numbers are a set — they are read, argued about and raised together.
//
// A future platform release may RAISE a floor; none of them ever moves down.
// Raising one is a deliberate act: it tells every operator still on that
// version that the launcher will no longer let them go back to it.
//
// WHERE THE FLOOR APPLIES: the app-config edit gate
// (daemon.dependencyEditLocked) and nowhere else. It must NOT be consulted by
// pin seeding, by the generator's resolveDependencyImage, or by anything else
// on the bundle path, because it limits what an operator may CHOOSE, not what
// may EXIST. A pin seeded from a PG_VERSION of 15 is a FACT about a stand that
// is already running; refusing it would leave the operator unable to touch
// that namespace at all — and a bundle that offers an older patch than the pin
// keeps applying silently, exactly as it does today (user ruling, 2026-09-10).

// SupportFloor is the oldest version of the dependency this launcher supports.
// See the file comment for what that means and where it is asked.
func (postgresDescriptor) SupportFloor() Version { return Version{Major: 17} }

// SupportFloor — see postgresDescriptor.SupportFloor.
func (rabbitDescriptor) SupportFloor() Version { return Version{Major: 4, Minor: 1} }

// SupportFloor — see postgresDescriptor.SupportFloor.
func (zookeeperDescriptor) SupportFloor() Version { return Version{Major: 3, Minor: 9} }

// SupportFloor — see postgresDescriptor.SupportFloor.
func (keycloakDescriptor) SupportFloor() Version { return Version{Major: 26, Minor: 4} }

// SupportFloor — see postgresDescriptor.SupportFloor.
func (mongoDescriptor) SupportFloor() Version { return Version{Major: 4} }

// BelowSupportFloor reports whether an image names a version older than the
// dependency's support floor.
//
// The floor is STRICT: the floor version itself is supported.
//
// An image whose tag names no version — "latest", a digest reference, an empty
// string — answers FALSE, because there is nothing to compare. That is not a
// hole: such a reference is already held back by Breaking, which says so in a
// message about the tag, and inventing a floor verdict for a version we could
// not read would answer a question nobody could act on.
func BelowSupportFloor(d Descriptor, image string) bool {
	v, ok := d.ParseVersion(image)
	if !ok {
		return false
	}
	return compareVersions(v, d.SupportFloor()) < 0
}

// SupportFloor — see postgresDescriptor.SupportFloor.
//
// 1.14 is the version RAG shipped with and the oldest that has ever been on a
// stand: nothing older was released, so an operator choosing one would be
// choosing a version the platform has never run.
func (qdrantDescriptor) SupportFloor() Version { return Version{Major: 1, Minor: 14} }
