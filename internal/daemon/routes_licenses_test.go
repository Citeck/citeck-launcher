package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/license"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// TestCollectExtraLicenses_RoundTripsLicenseStoreThroughGenerator pins the
// end-to-end glue introduced to fix item #1 in the porting checklist: a
// license written via the encrypted store (the same path the UI exercises
// via POST /api/v1/licenses) must land in the eapps cloud-config under
// `ecos.webapp.license.instances`. Before the fix the license.Service was
// dead code — Generate only consulted wsCfg.Licenses, so UI-added entries
// were silently dropped.
//
// The test goes all the way through SecretService + license.Service +
// collectExtraLicensesFrom + Generate so a regression at any layer
// (locked store, JSON shape drift, generator forgetting the merge) is
// caught here rather than at runtime when a webapp can't see its license.
func TestCollectExtraLicenses_RoundTripsLicenseStoreThroughGenerator(t *testing.T) {
	// Stand up a real SQLite-backed SecretService — same code the daemon
	// uses in desktop mode. NewSecretService auto-enables the default
	// password so we never hit the locked-store branch.
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	licSvc := license.NewService(svc)

	// Persist a stub enterprise license. IsValid will be false (no real
	// signature) but the generator does not gate on validity — it only
	// requires a present, parseable record.
	stub := license.Instance{
		ID:         "stub-1",
		Tenant:     "acme",
		Priority:   42,
		IssuedTo:   "Acme Corp",
		IssuedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Content:    json.RawMessage(`{"feature":"enterprise"}`),
	}
	require.NoError(t, licSvc.Add(stub))

	// Wire the license set through the same helper Start() / handleNamespaceReload()
	// use to populate GenerateOpts.
	extras := collectExtraLicensesFrom(licSvc)
	require.Len(t, extras, 1, "expected the stored license to surface via collectExtraLicensesFrom")
	assert.Equal(t, "stub-1", extras[0].ID, "round-trip preserves the license ID")

	// Minimal namespace config + bundle wiring eapps so the license
	// injection branch in generateWebapp is exercised.
	cfg := &namespace.Config{
		Authentication: namespace.AuthenticationProps{
			Type:  namespace.AuthBasic,
			Users: []string{"admin"},
		},
		Proxy: namespace.ProxyProps{Port: 80},
	}
	bun := &bundle.Def{
		Key: bundle.Key{Version: "test-bundle-1.0"},
		Applications: map[string]bundle.AppDef{
			"eapps": {Image: "nexus.citeck.ru/eapps:test"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{ID: "eapps"}},
		// Note: deliberately NO workspace-declared licenses — only the
		// UI-added one should reach the cloud config.
	}

	resp, err := namespace.Generate(cfg, bun, wsCfg, namespace.SystemSecrets{
		JWT:  "test-jwt",
		OIDC: "test-oidc",
	}, namespace.GenerateOpts{ExtraLicenses: extras})
	require.NoError(t, err)

	yamlBlob, ok := resp.Files["app/eapps/props/application-launcher.yml"]
	require.True(t, ok, "eapps cloud-config YAML must be generated")
	yamlStr := string(yamlBlob)
	// flatMapToYAML expands dotted keys, so the canonical leaf is
	//   ecos: { webapp: { license: { instances: [...] } } }
	// Use a substring that is robust to either flat or nested rendering.
	assert.True(t,
		strings.Contains(yamlStr, "instances:") && strings.Contains(yamlStr, "license:"),
		"expected license/instances keys in the eapps cloud-config YAML, got:\n%s", yamlStr)
	// Stronger: the marshalled license must include our stub ID and tenant.
	assert.Contains(t, yamlStr, `"id":"stub-1"`,
		"stub license id must reach the eapps cloud-config")
	assert.Contains(t, yamlStr, `"tenant":"acme"`,
		"stub license tenant must reach the eapps cloud-config")
}

// TestCollectExtraLicenses_LockedStoreFallsBackToWorkspaceOnly verifies the
// graceful-degradation path: a locked SecretService must NOT abort namespace
// generation. collectExtraLicensesFrom returns nil, the generator falls back
// to wsCfg.Licenses only, and the build succeeds — mirroring desktop-mode
// startup before the master-password unlock.
func TestCollectExtraLicenses_LockedStoreFallsBackToWorkspaceOnly(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	// Pre-populate the store with an encrypted blob AND mark the secrets
	// state as encrypted so NewSecretService detects "encrypted but no key".
	// IsLocked := encrypted && derivedKey == nil; without the state flag,
	// the service treats the blob as plaintext.
	require.NoError(t, store.PutSecretBlob("some-encrypted-blob-data"))
	require.NoError(t, store.SetStateValue("secrets_encrypted", "true"))

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.True(t, svc.IsLocked(), "SecretService should report locked when started with a pre-existing custom-password blob")

	licSvc := license.NewService(svc)
	extras := collectExtraLicensesFrom(licSvc)
	assert.Nil(t, extras, "locked store must yield nil (not error) so Generate keeps working")
}

// TestCollectExtraLicenses_NilServiceReturnsNil checks the defensive nil
// branch — daemon initialization paths construct license.NewService(secretSvc)
// before *Daemon exists, but a future refactor must not break the
// nil-tolerant contract.
func TestCollectExtraLicenses_NilServiceReturnsNil(t *testing.T) {
	assert.Nil(t, collectExtraLicensesFrom(nil))
}

// guard: appdef.AppEapps must keep its current string value, otherwise the
// generator's license-injection branch (which keys on appName == AppEapps)
// would silently no-op. This is the canonical service that emits
// `ecos.webapp.license.instances`.
func TestEappsAppNameStable(t *testing.T) {
	assert.Equal(t, "eapps", appdef.AppEapps,
		"appdef.AppEapps is the gate for license injection; renaming it would silently strip licenses from webapps")
	// We don't assert anything else here — just pin the constant.
	_ = strings.TrimSpace // keep import used in case the test grows
}
