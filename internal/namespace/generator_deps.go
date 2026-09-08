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
	if ctx.DependencyImages == nil {
		ctx.DependencyImages = make(map[deps.ID]DependencyGen)
	}
	// deps.Breaking already answers false for pinned == candidate and true for
	// an unparsable tag on either side, so only the "no pin at all" case needs
	// its own arm here.
	pinned, hasPin := ctx.DependencyPins[id]
	if !hasPin || pinned == "" || !deps.Breaking(d, pinned, candidate) {
		ctx.DependencyImages[id] = DependencyGen{Effective: candidate, Candidate: candidate}
		return candidate
	}
	effective := rehomePin(d, pinned, candidate)
	slog.Info("Dependency image held back by pin",
		"dependency", id, "pinned", pinned, "candidate", candidate, "effective", effective)
	ctx.DependencyImages[id] = DependencyGen{Effective: effective, Candidate: candidate}
	ctx.DependencyUpgrades = append(ctx.DependencyUpgrades, DependencyUpgrade{
		ID: id, App: d.AppName(), From: effective, To: candidate, Migratable: d.Migratable(),
	})
	return effective
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
