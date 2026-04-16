// Behavioral test for refreshSnapshotDigests: dev-cycle contract that
// citeck start / citeck reload re-pull :snapshot images before the hash diff
// so a developer push under the same tag is actually applied (not masked
// by stale local digest).
package namespace

import (
	"context"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
)

// TestRefreshSnapshotDigestsPullsOnlySnapshots asserts the contract:
//   - Images whose tag contains "snapshot" AND Kind != ThirdParty → pulled.
//   - All other images (pinned release tags, ThirdParty infra) → NOT pulled.
//
// Without this pre-pull, a developer push to :dev-snapshot with the same tag
// would be silently missed by reload (hash computed from stale local digest
// still matches the running container's stored hash → adoption path).
func TestRefreshSnapshotDigestsPullsOnlySnapshots(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		// Should pull: webapp with -snapshot tag.
		{Name: "emodel", Image: "nexus.example.com/ecos-model:2.38.0-snapshot", Kind: appdef.KindCiteckCore},
		// Should pull: webapp with SNAPSHOT (case-insensitive).
		{Name: "eapps", Image: "nexus.example.com/ecos-apps:2.26-SNAPSHOT", Kind: appdef.KindCiteckCore},
		// Should NOT pull: pinned release tag, no "snapshot" substring.
		{Name: "uiserv", Image: "nexus.example.com/ecos-uiserv:2.34.3", Kind: appdef.KindCiteckCore},
		// Should NOT pull: ThirdParty, regardless of tag.
		{Name: "postgres", Image: "postgres:17", Kind: appdef.KindThirdParty},
		// Should NOT pull: ThirdParty with "snapshot" in tag — ThirdParty
		// is explicitly excluded from shouldPullImage.
		{Name: "custom-third-party", Image: "example/foo:snapshot-1", Kind: appdef.KindThirdParty},
		// Should NOT pull: empty image (edge).
		{Name: "virtual", Image: "", Kind: appdef.KindCiteckCore},
	}

	pullsBefore := md.pullCalls
	r.refreshSnapshotDigests(context.Background(), apps)
	pullsAfter := md.pullCalls

	// Exactly 2 snapshot webapps should have been pulled.
	assert.Equal(t, pullsBefore+2, pullsAfter,
		"expected 2 snapshot pulls (emodel + eapps); got %d", pullsAfter-pullsBefore)
}

// TestRefreshSnapshotDigestsHandlesPullFailureGracefully confirms the
// best-effort policy: a pull failure does NOT panic or block; the helper
// returns after logging the WARN. The downstream flow then computes hash
// from whatever local digest is available (stale or missing).
func TestRefreshSnapshotDigestsHandlesPullFailureGracefully(t *testing.T) {
	md := newMockDocker()
	// Wedge the pull so the worker blocks briefly, then releases with ctx
	// timeout. We want to verify refreshSnapshotDigests still returns
	// promptly instead of hanging if PullImageWithProgress errors out.
	// Use pullBlock to force ctx-cancel path.
	md.pullBlock = make(chan struct{})
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()
	defer close(md.pullBlock)

	apps := []appdef.ApplicationDef{
		{Name: "emodel", Image: "nexus.example.com/ecos-model:2.38.0-snapshot", Kind: appdef.KindCiteckCore},
	}

	// Use a short ctx so the pull aborts quickly (2s instead of 2-min
	// internal timeout). This simulates a registry hang.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.refreshSnapshotDigests(ctx, apps)
	}()

	select {
	case <-done:
		// Good: returned cleanly under ctx timeout.
	case <-time.After(3 * time.Second):
		t.Fatal("refreshSnapshotDigests did not return within 3s after ctx timeout; pre-pull should be best-effort")
	}
}
