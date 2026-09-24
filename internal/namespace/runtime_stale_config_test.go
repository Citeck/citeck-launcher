package namespace

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestSetConfigRefreshesTheLinksWithoutTheRuntimeLoop pins the root of the
// stale-config class.
//
// Everything the namespace DTO derives from the config — the header name and
// bundle, the sidebar links' proxy host/scheme, the Keycloak link's auth type,
// AppliedConfig — reads r.config, which cmdRegenerate refreshes only on the
// runtime LOOP. On a stopped namespace that loop is not running, so an edit to
// the bundle or the proxy host kept showing the pre-edit value until the
// namespace was re-activated. doReloadEx now publishes it synchronously.
func TestSetConfigRefreshesTheLinksWithoutTheRuntimeLoop(t *testing.T) {
	r := &Runtime{
		apps:             map[string]*AppRuntime{},
		editedAppPatches: map[string]json.RawMessage{},
		config: &Config{
			ID:    "ns",
			Proxy: ProxyProps{Host: "old.example.com", Port: 80},
		},
	}

	r.mu.RLock()
	before := r.proxyBaseURL()
	r.mu.RUnlock()
	require.Contains(t, before, "old.example.com")

	r.SetConfig(&Config{ID: "ns", Proxy: ProxyProps{Host: "new.example.com", Port: 80}})

	r.mu.RLock()
	after := r.proxyBaseURL()
	r.mu.RUnlock()
	assert.Contains(t, after, "new.example.com",
		"an edited proxy host must reach the links without waiting for the runtime loop")

	// A nil config is ignored rather than blanking a working one — Regenerate
	// is allowed to carry nil, and generateLinks dereferences r.config.
	r.SetConfig(nil)
	r.mu.RLock()
	still := r.proxyBaseURL()
	r.mu.RUnlock()
	assert.Equal(t, after, still)
}

// TestPgAdminLinkAppearsBeforeTheFirstStart: the built-in PgAdmin link checked
// r.apps only, which is empty until the namespace has been started once — so
// enabling PgAdmin on a stopped namespace produced no link until it ran. The
// custom links right below it already use the live-OR-configured rule.
func TestPgAdminLinkAppearsBeforeTheFirstStart(t *testing.T) {
	r := &Runtime{
		apps:             map[string]*AppRuntime{},
		editedAppPatches: map[string]json.RawMessage{},
		config:           &Config{ID: "ns", Proxy: ProxyProps{Host: "localhost", Port: 80}},
	}
	r.SetGeneratedDefs([]appdef.ApplicationDef{{Name: "pgadmin", Image: "dpage/pgadmin4:9.17"}})

	r.mu.RLock()
	links := r.generateLinks()
	r.mu.RUnlock()

	found := false
	for _, l := range links {
		if l.Name == "PG Admin" {
			found = true
		}
	}
	assert.True(t, found, "a configured pgadmin must be linked before the namespace ever starts")
}

// TestStartableAppImagesFallsBackToGeneratedDefs: this feeds the pre-start
// registry credential check, and r.apps is empty until the first start — see
// the daemon-side regression test for what that cost.
func TestStartableAppImagesFallsBackToGeneratedDefs(t *testing.T) {
	r := &Runtime{
		apps:              map[string]*AppRuntime{},
		manualStoppedApps: map[string]bool{"detached": true},
	}
	r.SetGeneratedDefs([]appdef.ApplicationDef{
		{Name: "emodel", Image: "enterprise-registry.citeck.ru/ecos/emodel:2.41.0"},
		{Name: "detached", Image: "enterprise-registry.citeck.ru/ecos/ai:1.12.0"},
		{Name: "postgres", Image: "postgres:17.5"},
	})

	assert.Equal(t,
		[]string{"enterprise-registry.citeck.ru/ecos/emodel:2.41.0", "postgres:17.5"},
		r.StartableAppImages(),
		"detached apps stay excluded when falling back to the generated defs")
}

// The infrastructure web UIs are published on loopback only, so their links
// name loopback — localhost, and 127.0.0.1 for RabbitMQ so its cookies are
// kept apart from the other UIs' (1.x parity) — even when the namespace is addressed by a LAN host — a
// connection to this machine's LAN address does not arrive via loopback. The
// proxy link keeps the namespace host: it is the one app reached from outside.
func TestInfrastructureLinksNameLocalhost(t *testing.T) {
	r := &Runtime{
		apps:             map[string]*AppRuntime{},
		editedAppPatches: map[string]json.RawMessage{},
		config:           &Config{ID: "ns", Proxy: ProxyProps{Host: "192.168.1.20", Port: 80}},
	}
	r.SetGeneratedDefs([]appdef.ApplicationDef{{Name: "pgadmin", Image: "dpage/pgadmin4:9.17"}})

	r.mu.RLock()
	links := r.generateLinks()
	r.mu.RUnlock()

	urls := map[string]string{}
	for _, l := range links {
		urls[l.Name] = l.URL
	}
	assert.Equal(t, "http://127.0.0.1:15672", urls["RabbitMQ"], "a host name of its own: a separate cookie jar (1.x parity)")
	assert.Equal(t, "http://localhost:8025", urls["Mailpit"])
	assert.Equal(t, "http://localhost:5050", urls["PG Admin"])
	assert.Contains(t, urls["Citeck UI"], "192.168.1.20", "the proxy link keeps the namespace host")
}
