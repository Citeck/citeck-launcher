package daemon

import (
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Which path caches the bundle, and which one persists, is a two-sided rule
// and both sides are load-bearing:
//
//   - doReloadEx must cache what it resolved (SetCachedBundle, which PERSISTS).
//     A bundle ref that arrives through an edit is written by the edit and then
//     reloaded, so before this the ref was never cached until the next load —
//     remove the bundle from the repo in between and the namespace came back
//     with zero applications, which is the state the cache exists to prevent.
//   - loadNamespace must NOT persist (RestoreCachedBundle). It runs before the
//     caller has acted on ShouldStart, so a write there records r.status while
//     it is still the placeholder STOPPED — and that recorded status is what
//     decides whether the namespace auto-starts next time.
//
// Neither function is unit-drivable (git, Docker, a real store), so this
// asserts on the production source — the same reason
// TestNewerBundleFillNeverReacquiresConfigMu does.
func TestTheReloadPathCachesTheBundleAndTheLoadPathDoesNotPersist(t *testing.T) {
	reload := parseFuncDecl(t, "server.go", "doReloadEx")
	require.NotEqual(t, token.NoPos, callPos(reload, "SetCachedBundle"),
		"doReloadEx no longer caches the bundle it resolved — then a bundle ref that arrived "+
			"through an edit has no cache until the next daemon start, and a bundle removed from "+
			"the repo in the meantime leaves the namespace with zero applications")

	load := parseFuncDecl(t, "namespace_loader.go", "loadNamespace")
	assert.NotEqual(t, token.NoPos, callPos(load, "RestoreCachedBundle"),
		"loadNamespace must still install the cache it resolved")
	assert.Equal(t, token.NoPos, callPos(load, "SetCachedBundle"),
		"loadNamespace must use the NON-persisting variant: persisting there writes the "+
			"placeholder STOPPED status that ShouldStart is derived from")
}
