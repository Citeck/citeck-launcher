package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ladderCtx builds a generation context whose bundle offers `ladder` for
// `qdrant` and whose namespace is pinned at `pinned`. Every case in this file
// happens to pin the same qdrant version, but the parameter is real: a caller
// pinning elsewhere is a one-line change, not a signature change, and dropping
// it would make each test build its own NsGenContext by hand.
//
//nolint:unparam // see above
func ladderCtx(pinned string, ladder []string) *NsGenContext {
	cfg := DefaultNamespaceConfig()
	bun := &bundle.Def{
		Dependencies: map[string]bundle.AppDef{
			"qdrant": {Image: ladder[len(ladder)-1], Images: ladder},
		},
	}
	ctx := NewNsGenContext(&cfg, bun)
	ctx.DependencyStates = map[deps.ID]deps.DependencyState{
		deps.Qdrant: {Image: pinned, VolumeGen: 1},
	}
	return ctx
}

func heldQdrant(t *testing.T, ctx *NsGenContext) DependencyUpgrade {
	t.Helper()
	for _, u := range ctx.DependencyUpgrades {
		if u.ID == deps.Qdrant {
			return u
		}
	}
	require.Fail(t, "qdrant was not reported as held back")
	return DependencyUpgrade{}
}

// The whole point: a hop the vendor forbids in ONE step is allowed when the
// ladder names the rungs, because every adjacent pair is a hop it permits.
func TestALadderMakesAVendorBlockedJumpReachable(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1",
		"qdrant/qdrant:v1.17.1", "qdrant/qdrant:v1.18.3", "qdrant/qdrant:v1.19.1",
	})
	effective := resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	assert.Equal(t, "qdrant/qdrant:v1.14.1", effective, "the container keeps running the pin")
	held := heldQdrant(t, ctx)
	assert.False(t, held.VendorBlocked, "every adjacent pair is one minor")
	assert.Empty(t, held.VendorVia)
	assert.Equal(t, []string{
		"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1",
		"qdrant/qdrant:v1.17.1", "qdrant/qdrant:v1.18.3", "qdrant/qdrant:v1.19.1",
	}, held.Path)
	assert.Equal(t, "qdrant/qdrant:v1.19.1", held.To)
}

// Without the ladder the same pair is what it always was: blocked, with the
// vendor naming the next hop.
func TestNoLadderLeavesTheVendorRefusalExactlyAsItWas(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{"qdrant/qdrant:v1.19.1"})
	resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	held := heldQdrant(t, ctx)
	assert.True(t, held.VendorBlocked)
	assert.Equal(t, "1.15", held.VendorVia)
	assert.Equal(t, []string{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.19.1"}, held.Path)
}

// A ladder with a rung missing is refused at the gap, and the vendor names the
// version the ladder should have had.
func TestALadderWithAGapIsBlockedAtTheGap(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1",
	})
	resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	held := heldQdrant(t, ctx)
	assert.True(t, held.VendorBlocked)
	assert.Equal(t, "1.16", held.VendorVia, "the gap is named, not the ladder's first rung")
}

// A rung nobody can read leaves the dependency on its pin with no route: the
// preflight's message about the tag is the one the operator needs.
func TestAnUnreadableRungLeavesNoRoute(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:latest", "qdrant/qdrant:v1.19.1",
	})
	effective := resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)
	assert.Equal(t, "qdrant/qdrant:v1.14.1", effective)

	held := heldQdrant(t, ctx)
	assert.Empty(t, held.Path, "no route at all, rather than one with a hole in it")
	assert.False(t, held.VendorBlocked, "the vendor was never asked; the tag is the problem")
}

// A single image behaves as it always did, and the path is the ordinary pair.
func TestASingleImageStillReportsAPlainPair(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{"qdrant/qdrant:v1.15.5"})
	resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	held := heldQdrant(t, ctx)
	assert.False(t, held.VendorBlocked)
	assert.Equal(t, []string{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"}, held.Path)
}

// The gate's registry-adoption rule (rehomePin) has to reach every rung of the
// ladder, not just the final candidate: a migration that walks the ladder
// pulls every intermediate image too, and a pin already known to live on a
// private registry is the one piece of evidence this stand has about where
// those pulls can actually succeed.
func TestRehomeChainCarriesThePinsPrivateRegistryToEveryRung(t *testing.T) {
	d, ok := deps.Lookup(deps.Qdrant)
	require.True(t, ok)
	out := rehomeChain(d, "registry.example.com/qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1",
	})
	assert.Equal(t, []string{
		"registry.example.com/qdrant/qdrant:v1.15.5",
		"registry.example.com/qdrant/qdrant:v1.16.1",
	}, out)
}

// Mongo goes through the identical chain-aware gate every other registered
// dependency does: a bundle `dependencies:` ladder for it must produce the
// FULL route, not the single pin/target pair a one-element chain collapses it
// to. Mongo's own UpgradeSupport never refuses a forward hop
// (forwardOnlySupport), so VendorBlocked stays false either way — a
// route-collapsing regression here is invisible to every other assertion and
// shows up only in Path, which is exactly why this needs its own test rather
// than riding along on TestMongoMajorBumpIsHeldByThePin.
func TestAMongoLadderIsNotCollapsedToJustThePinAndTarget(t *testing.T) {
	bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
		appdef.AppMongodb: {
			Image:  "mongo:7.0.0",
			Images: []string{"mongo:5.0.0", "mongo:6.0.0", "mongo:7.0.0"},
		},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.MongoDB: "mongo:4.0.2"})
	up := upgradeFor(t, resp, deps.MongoDB)
	require.NotNil(t, up)
	assert.Equal(t, []string{"mongo:4.0.2", "mongo:5.0.0", "mongo:6.0.0", "mongo:7.0.0"}, up.Path,
		"every rung the bundle wrote must survive the gate, not just the pin and the target")
}
