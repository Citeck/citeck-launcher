package namespace

// Dependency-pin gate for the infrastructure generators. Every infra image
// (postgres, rabbitmq, zookeeper, keycloak, mongo) is resolved through
// resolveDependencyImage so a bundle bump can never move an existing
// namespace's DATA onto an incompatible version behind the user's back. The
// rules themselves live in internal/deps; this file only applies them.

import (
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// DependencyUpgrade is a bundle/workspace image the generator held back
// because applying it to the namespace's existing data would be a breaking
// change. Surfaced to the UI/CLI as "upgrade available".
type DependencyUpgrade struct {
	ID         deps.ID
	App        string
	From       string // pinned image (what runs)
	To         string // candidate image (what the bundle wants)
	Migratable bool   // this launcher can perform the migration
	// VendorBlocked reports that the DEPENDENCY'S OWN VENDOR does not support
	// this hop at all (RabbitMQ 4.1 -> 4.3, ZooKeeper data older than 3.5).
	// It is a different refusal from !Migratable: updating the launcher would
	// not help, so the two must never be worded the same way, and a client
	// that splits on Migratable alone would advertise a hop nobody can take.
	VendorBlocked bool
	// VendorVia names the intermediate version a two-hop path goes through
	// ("4.2" for RabbitMQ 4.1 -> 4.3), spelled major.minor. Empty when the
	// vendor documents no path at all — the difference between "do this first"
	// and "this is not possible". Meaningless unless VendorBlocked.
	VendorVia string
	// BundleOlder reports that the held-back candidate is an OLDER version
	// than the pin. It is not an upgrade that is held back — there is nothing
	// to migrate and nothing to wait for — so a client that renders every
	// entry here as "upgrade available" is simply wrong about this one: the
	// launcher keeps the data where it is, and the only way to the older
	// version is a rollback onto the volume a migration retained.
	//
	// It is a FACT and not a sentence: internal/namespace may not import
	// internal/deps/migrate, where every operator-facing migration sentence is
	// built. The daemon renders it (api.DependencyBundleOlder).
	//
	// A same-format backwards move — a bundle reverting a PATCH bump — is not
	// held at all and therefore never appears here (user ruling, 2026-09-09:
	// only format breaks are held back). See deps.BundleOlder.
	BundleOlder bool
}

// DependencyGen records, per registered dependency, the image the generator
// emitted and the image it was asked for — equal unless a pin held it back.
type DependencyGen struct {
	Effective string
	Candidate string
}

// resolveDependencyImage is the gate every infra generator passes its image
// through. With no pin (no data yet) the candidate applies. With a pin, a
// non-breaking candidate applies (and the Runtime re-pins it once RUNNING);
// a breaking one is held and reported. Upgrades are appended in generator
// order; sortedUpgrades re-orders them to registry order.
func resolveDependencyImage(ctx *NsGenContext, id deps.ID, candidate string) string {
	d, ok := deps.Lookup(id)
	if !ok {
		return candidate
	}
	// deps.Breaking already answers false for pinned == candidate and true for
	// an unparsable tag on either side, so only the "no pin at all" case needs
	// its own arm here — and a dependency absent from the map reads as the zero
	// DependencyState, whose image is exactly that empty string.
	pinned := ctx.DependencyStates[id].Image
	if pinned == "" || !deps.Breaking(d, pinned, candidate) {
		ctx.DependencyImages[id] = DependencyGen{Effective: candidate, Candidate: candidate}
		return candidate
	}
	effective := rehomePin(d, pinned, candidate)
	// A backwards hold is not an upgrade, so it is not asked the vendor's
	// upgrade question: no vendor here permits a downgrade, and answering
	// "there is no upgrade path from 4.2.9 to 4.1.8" would put a refusal about
	// upgrading in front of an operator who is not upgrading.
	older := deps.BundleOlder(d, effective, candidate)
	var blocked bool
	var via string
	if !older {
		blocked, via = vendorVerdict(d, effective, candidate)
	}
	slog.Info("Dependency image held back by pin",
		"dependency", id, "pinned", pinned, "candidate", candidate, "effective", effective,
		"bundleOlder", older)
	ctx.DependencyImages[id] = DependencyGen{Effective: effective, Candidate: candidate}
	ctx.DependencyUpgrades = append(ctx.DependencyUpgrades, DependencyUpgrade{
		ID: id, App: d.AppName(), From: effective, To: candidate, Migratable: d.Migratable(),
		VendorBlocked: blocked, VendorVia: via, BundleOlder: older,
	})
	return effective
}

// vendorVerdict asks the dependency's own vendor whether the held-back hop is
// supported at all, and — when it is not — which intermediate version the
// operator has to go through first. It is a question about the DEPENDENCY, so
// the table lives in internal/deps; the generator only records the answer,
// because the sentence an operator reads is built where every other migration
// refusal is worded.
//
// With an unreadable tag on either side there are no versions to ask about, so
// the verdict is empty rather than guessed. The pin is held back anyway
// (deps.Breaking answers true for an unparsable tag) and the preflight's
// message about the tag is the one the operator needs — the same carve-out the
// downgrade case gets, and for the same reason: a wrong intermediate sends the
// operator after a version that would not help.
func vendorVerdict(d deps.Descriptor, pinned, candidate string) (blocked bool, via string) {
	from, okFrom := d.ParseVersion(pinned)
	to, okTo := d.ParseVersion(candidate)
	if !okFrom || !okTo {
		return false, ""
	}
	sup := d.UpgradeSupport(from, to)
	if sup.Allowed {
		return false, ""
	}
	return true, sup.Via
}

// resolveDependencyVolume is the ONE place a generator learns which volume a
// dependency's data lives in. It is the pin's generation, or generation 1 for
// a namespace that has never migrated — which is every namespace that exists
// today, so every emitted volume name is unchanged.
//
// The volume is deliberately NOT derived from the version: a name that moved
// with the image would make every version bump a silent data move, and the
// counter advances only when a migration has actually copied the data across.
//
// "" for a dependency with no volume of its own (Keycloak) and for an id that
// is not registered — neither has a caller today, and a caller that appeared
// would be mounting a volume named after nothing.
func resolveDependencyVolume(ctx *NsGenContext, id deps.ID) string {
	d, ok := deps.Lookup(id)
	if !ok {
		return ""
	}
	// A missing entry reads as the zero DependencyState, whose Gen() is 1 —
	// which is exactly the answer for a namespace that has never migrated.
	return deps.VolumeName(d, ctx.DependencyStates[id].Gen())
}

// rehomePin keeps a HELD-BACK dependency on the registry the bundle is pulling
// from, without moving its version.
//
// A pin is not always evidence of where the image came from. The two data-
// derived seeds — the descriptor's LegacyImage and the "postgres:<major>" read
// out of PG_VERSION — are Docker Hub references, invented because there was no
// container to inspect. On a private-registry or air-gapped stand that is a
// reference the host may not be able to pull at all: the gate would emit
// "postgres:17" for a namespace whose every other image comes from
// mirror.example.com/, and the held-back dependency — the one case where the
// launcher deliberately does NOT follow the bundle — would be the one that
// cannot start.
//
// So when the pin still carries the DEFAULT repository (i.e. nobody has ever
// seen the real one) and the candidate carries a different one, the candidate's
// repository is adopted and the pin's TAG is kept: same version, reachable
// registry. A pin derived from an actual container already names the real
// repository and is left exactly as it is — and so is everything else, which
// is what keeps a same-repository namespace byte-identical to what the
// previous launcher emitted (the hash-stability golden).
func rehomePin(d deps.Descriptor, pinned, candidate string) string {
	pinRepo, pinTag, ok := deps.SplitImageRef(pinned)
	if !ok {
		return pinned
	}
	defaultRepo, _, okDefault := deps.SplitImageRef(d.LegacyImage())
	if !okDefault || pinRepo != defaultRepo {
		return pinned
	}
	candidateRepo, _, okCandidate := deps.SplitImageRef(candidate)
	if !okCandidate || candidateRepo == pinRepo {
		return pinned
	}
	return candidateRepo + ":" + pinTag
}

// sortedUpgrades returns ctx.DependencyUpgrades in registry order regardless
// of generator order (mongo runs first and keycloak last today, while the
// registry lists postgres first and mongo last).
func sortedUpgrades(ctx *NsGenContext) []DependencyUpgrade {
	if len(ctx.DependencyUpgrades) == 0 {
		return nil
	}
	out := make([]DependencyUpgrade, 0, len(ctx.DependencyUpgrades))
	for _, d := range deps.All() {
		for _, u := range ctx.DependencyUpgrades {
			if u.ID == d.ID() {
				out = append(out, u)
			}
		}
	}
	return out
}
