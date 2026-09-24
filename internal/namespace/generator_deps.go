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
	// Path is the ROUTE this upgrade takes: the pinned image at index 0, then
	// every rung of the bundle's ladder strictly newer than it, ending at To.
	// A bundle naming one image gives the ordinary two-element pair, so no
	// consumer needs a second shape for the single-hop case.
	//
	// EMPTY means there is no route at all — a rung the version parser cannot
	// read — and it is deliberately not the same as a one-element path: the
	// dependency is held back and the preflight's message about the tag is
	// what the operator needs, so nothing downstream may invent a pair here.
	Path []string
}

// DependencyGen records, per registered dependency, the image the generator
// emitted and the image it was asked for — equal unless a pin held it back.
type DependencyGen struct {
	Effective string
	Candidate string
}

// resolveDependencyImage is the gate every infra generator passes its image
// through. It takes the bundle's whole LADDER rather than one candidate: the
// candidate is its last rung, and the rungs in between are what make a hop the
// vendor forbids in one step reachable in several.
//
// With no pin (no data yet) the candidate applies. With a pin, a non-breaking
// candidate applies (and the Runtime re-pins it once RUNNING); a breaking one
// is held and reported. Upgrades are appended in generator order;
// sortedUpgrades re-orders them to registry order.
func resolveDependencyImage(ctx *NsGenContext, id deps.ID, chain []string) string {
	candidate := ""
	if len(chain) > 0 {
		candidate = chain[len(chain)-1]
	}
	d, ok := deps.Lookup(id)
	if !ok {
		return candidate
	}
	// A malformed ladder (rungs out of order, a duplicate, one nobody can
	// parse) is refused by deps.UpgradeRoute wherever a pin exists to route
	// from — but on a FRESH stand nothing calls UpgradeRoute at all, so
	// nothing would otherwise say anything: the candidate above is already
	// just chain's last element, taken as written, regardless of whether the
	// rest of the chain makes sense. Checking here, before the two paths
	// diverge, covers both: a fresh stand still gets the candidate it always
	// got (this is a diagnostic, not a behavior change), and a pinned stand
	// whose route later comes back empty now has a reason on record instead
	// of a bare "held back".
	if deps.MalformedLadder(d, chain) {
		slog.Warn("Bundle ladder is malformed (a rung is unreadable, or the "+
			"rungs do not strictly ascend, as written); using its last rung "+
			"as written, and no multi-step route can be computed through it "+
			"until the ladder is fixed",
			"dependency", id, "ladder", chain)
	}
	// deps.Breaking already answers false for pinned == candidate and true for
	// an unparsable tag on either side, so only the "no pin at all" case needs
	// its own arm here — and a dependency absent from the map reads as the zero
	// DependencyState, whose image is exactly that empty string.
	pinned := ctx.DependencyStates[id].Image
	if pinned != "" && ctx.DatalessDependencies[id] && deps.Breaking(d, pinned, candidate) {
		// The pin names a version its data ran on, and that data is gone:
		// nothing is left for the hold to protect. The generation is kept by
		// resolveDependencyVolume, which reads the pin, not this image.
		slog.Info("Dependency pin has no data left; the bundle image applies",
			"dependency", id, "pinned", pinned, "candidate", candidate)
		pinned = ""
	}
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
	var path []string
	if !older {
		// The route is computed from the EFFECTIVE pin, which is what the
		// migration's first container is built from — a rehomed pin names the
		// registry the stand can actually pull from.
		path, blocked, via = routeVerdict(d, effective, rehomeChain(d, effective, chain))
	}
	slog.Info("Dependency image held back by pin",
		"dependency", id, "pinned", pinned, "candidate", candidate, "effective", effective,
		"bundleOlder", older, "rungs", len(path))
	ctx.DependencyImages[id] = DependencyGen{Effective: effective, Candidate: candidate}
	ctx.DependencyUpgrades = append(ctx.DependencyUpgrades, DependencyUpgrade{
		ID: id, App: d.AppName(), From: effective, To: candidate, Migratable: d.Migratable(),
		VendorBlocked: blocked, VendorVia: via, BundleOlder: older, Path: path,
	})
	return effective
}

// rehomeChain applies the pin's registry to every rung, the same way
// rehomePin applies it to the candidate: a ladder is only usable if every rung
// on it can be pulled from the registry this stand actually has.
//
// It is deliberately NOT a per-rung call into rehomePin: that function always
// answers with the PIN's own tag (it is choosing the one running image), while
// here every rung must keep its OWN tag and only the repository is up for
// adoption. The decision is the same one rehomePin makes for the candidate —
// a pin already known to live on a real (non-default) registry is the only
// evidence this stand has about where its pulls can succeed, and that
// registry is what every rung is rewritten onto. A pin still carrying the
// launcher's own guessed default teaches nothing about the real registry, so
// the ladder is left exactly as the bundle wrote it.
func rehomeChain(d deps.Descriptor, effectivePin string, chain []string) []string {
	if len(chain) == 0 {
		return nil
	}
	pinRepo, _, ok := deps.SplitImageRef(effectivePin)
	if !ok {
		return chain
	}
	defaultRepo, _, okDefault := deps.SplitImageRef(d.LegacyImage())
	if !okDefault || pinRepo == defaultRepo {
		return chain
	}
	out := make([]string, 0, len(chain))
	for _, rung := range chain {
		_, rungTag, okRung := deps.SplitImageRef(rung)
		if !okRung {
			out = append(out, rung)
			continue
		}
		out = append(out, pinRepo+":"+rungTag)
	}
	return out
}

// routeVerdict computes the route and asks the vendor about EVERY adjacent
// pair on it, rather than about (pin, target).
//
// That is the whole of the ladder feature on the gate's side. Asking about the
// pair alone is what made a reachable multi-step upgrade report as blocked,
// and the intermediate it then named was one the edit gate refuses to apply.
//
// With no route — an unreadable rung — the verdict is EMPTY rather than
// guessed: the pin is held back anyway (deps.Breaking answers true for an
// unparsable tag) and the preflight's message about the tag is the accurate
// one, the same carve-out the downgrade case gets.
//
// `via` names the GAP: the version the ladder would have needed at the first
// pair the vendor refuses. It is not the ladder's own next rung — that is
// precisely the rung that does not help.
func routeVerdict(d deps.Descriptor, pinned string, chain []string) (path []string, blocked bool, via string) {
	route, ok := deps.UpgradeRoute(d, pinned, chain)
	if !ok {
		return nil, false, ""
	}
	for i := 0; i+1 < len(route); i++ {
		from, okFrom := d.ParseVersion(route[i])
		to, okTo := d.ParseVersion(route[i+1])
		if !okFrom || !okTo { // unreachable: UpgradeRoute parsed them all
			return route, false, ""
		}
		if sup := d.UpgradeSupport(from, to); !sup.Allowed {
			return route, true, sup.Via
		}
	}
	return route, false, ""
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
