package namespace

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

func testBundleDef(version string) *bundle.Def {
	return &bundle.Def{
		Key:          bundle.Key{Version: version},
		Applications: map[string]bundle.AppDef{"emodel": {Image: "citeck/emodel:1.0"}},
	}
}

func newCacheTestRuntime(t *testing.T) (*Runtime, *fakePersister) {
	t.Helper()
	cfg := DefaultNamespaceConfig()
	cfg.ID = "ns1"
	r := NewRuntime(&cfg, nil, t.TempDir())
	p := &fakePersister{}
	r.SetStatePersister(p)
	return r, p
}

// The cache only helps across a restart, so the reload path's write has to
// reach the store. Before this, the ONLY caller was the load path, which must
// not persist — so a bundle ref that arrived through an edit was cached in
// memory at best and the next daemon start resolved from scratch, with zero
// applications when the bundle had left the repo meanwhile.
func TestSetCachedBundlePersistsIt(t *testing.T) {
	r, p := newCacheTestRuntime(t)

	r.SetCachedBundle(testBundleDef("2026.2"))

	require.Positive(t, p.callCount(), "a cached bundle nobody wrote down is not a cache")
	var state NsPersistedState
	require.NoError(t, json.Unmarshal([]byte(p.lastJSON()), &state))
	require.NotNil(t, state.CachedBundle)
	assert.Equal(t, "2026.2", state.CachedBundle.Key.Version)
}

// The load path's variant must stay silent: it runs before the caller has
// acted on ShouldStart, and a write there records r.status while it is still
// the placeholder STOPPED — which is what decides whether the namespace
// auto-starts next time.
func TestRestoreCachedBundleDoesNotPersist(t *testing.T) {
	r, p := newCacheTestRuntime(t)

	r.RestoreCachedBundle(testBundleDef("2026.2"))

	assert.Zero(t, p.callCount(), "the load path must not write state")
	assert.Equal(t, "2026.2", r.cachedBundleForTest().Key.Version, "but it must still install the cache")
}

// An empty bundle is not a fallback worth having: resolving to it reports a
// namespace with no Citeck services as healthy, and the daemon's fallback
// refuses an empty cache for the same reason. Both entry points drop it.
func TestAnEmptyBundleIsNeverCached(t *testing.T) {
	r, p := newCacheTestRuntime(t)
	r.SetCachedBundle(testBundleDef("2026.2"))
	before := p.callCount()

	r.SetCachedBundle(&bundle.Def{Key: bundle.Key{Version: "empty"}})
	r.RestoreCachedBundle(&bundle.Def{Key: bundle.Key{Version: "empty"}})
	r.SetCachedBundle(nil)

	assert.Equal(t, before, p.callCount(), "an empty bundle must not even cost a write")
	assert.Equal(t, "2026.2", r.cachedBundleForTest().Key.Version, "the real cache must survive an empty one")
}

// cachedBundleForTest reads the field under the runtime's own lock — the
// production readers of r.cachedBundle are persistState and the daemon's
// fallback, neither of which is a getter.
func (r *Runtime) cachedBundleForTest() *bundle.Def {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cachedBundle
}
