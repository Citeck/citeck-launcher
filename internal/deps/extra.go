package deps

import "sync"

// This file is what lets a WORKSPACE declare a dependency the launcher has no
// Go code for — today a second PostgreSQL cluster beside the stand's own.
//
// The registry above is a fixed list because the RULES are a fixed list: what a
// breaking move is, which layout a major uses, what a vendor permits. A
// workspace-declared cluster brings none of that — it reuses the PostgreSQL
// descriptor entirely and differs only in id, container name and volume stem —
// so what is added here is an INSTANCE, never a new kind of thing.
//
// It is package-level mutable state, which is a deliberate trade and worth
// stating plainly: `Lookup` and `All` are called from the generator, the daemon
// routes, the seeding and the migration engine — some twenty places — and
// threading a per-workspace registry through all of them would touch far more
// code than it would protect. The state has ONE writer (the daemon, when the
// active workspace's config is resolved; see setWorkspaceDependencies) and is
// read under a lock. A workspace switch REPLACES the set, which is why the
// setter takes the whole list rather than adding to it: a cluster the previous
// workspace declared must not survive into the next one.
var (
	extraMu sync.RWMutex
	extras  []Descriptor
)

// NewPostgresDescriptor builds a descriptor for a PostgreSQL cluster declared
// outside this package. It is the ONLY way to make one: the type stays
// unexported so nothing can invent a descriptor that answers the PostgreSQL
// rules differently from the built-in clusters.
func NewPostgresDescriptor(id ID, appName, volumeBase string) Descriptor {
	return postgresDescriptor{id: id, appName: appName, volumeBase: volumeBase}
}

// SetExtraDependencies installs the dependencies the active workspace declares,
// replacing whatever the previous workspace declared.
//
// An entry whose id collides with a BUILT-IN is dropped: the built-ins carry
// rules (and, for the stand's own postgres, a volume every launcher ever
// shipped has agreed on), and letting a workspace file redefine `postgres`
// would point the pin, the probe and the migration at somebody else's data. A
// workspace tunes a built-in through its own config blocks, never by
// re-declaring it here.
func SetExtraDependencies(ds []Descriptor) {
	next := make([]Descriptor, 0, len(ds))
	seen := make(map[ID]bool, len(ds))
	for _, d := range ds {
		if d == nil || d.ID() == "" || seen[d.ID()] {
			continue
		}
		if isBuiltIn(d.ID()) {
			continue
		}
		seen[d.ID()] = true
		next = append(next, d)
	}
	extraMu.Lock()
	extras = next
	extraMu.Unlock()
}

// ResetExtraDependencies drops every workspace-declared dependency. Called when
// no workspace is active, and by tests that installed some.
func ResetExtraDependencies() { SetExtraDependencies(nil) }

func isBuiltIn(id ID) bool {
	for _, d := range registry {
		if d.ID() == id {
			return true
		}
	}
	return false
}

// extraDescriptors returns a snapshot of the workspace-declared set.
func extraDescriptors() []Descriptor {
	extraMu.RLock()
	defer extraMu.RUnlock()
	if len(extras) == 0 {
		return nil
	}
	out := make([]Descriptor, len(extras))
	copy(out, extras)
	return out
}
