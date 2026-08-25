package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
)

func TestNamespaceNeedsUserSecrets(t *testing.T) {
	ws := &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
		{ID: "ent", URL: "enterprise-registry.citeck.ru", AuthType: "BASIC"},
		{ID: "pub", URL: "registry.citeck.ru", AuthType: ""},
	}}

	t.Run("auth-required host → true", func(t *testing.T) {
		assert.True(t, namespaceNeedsUserSecrets(
			[]string{"enterprise-registry.citeck.ru/ecos/emodel:2.40.0"}, ws))
	})
	t.Run("public configured host → false", func(t *testing.T) {
		assert.False(t, namespaceNeedsUserSecrets(
			[]string{"registry.citeck.ru/ecos/proxy:3.7.4"}, ws))
	})
	t.Run("docker hub library image (no host) → false", func(t *testing.T) {
		assert.False(t, namespaceNeedsUserSecrets(
			[]string{"postgres:17.5", "rabbitmq:4.1.2-management"}, ws))
	})
	t.Run("mixed set with one auth host → true", func(t *testing.T) {
		assert.True(t, namespaceNeedsUserSecrets(
			[]string{"postgres:17.5", "enterprise-registry.citeck.ru/ecos/ai:1.12.0"}, ws))
	})
	t.Run("nil wsCfg → false", func(t *testing.T) {
		assert.False(t, namespaceNeedsUserSecrets(
			[]string{"enterprise-registry.citeck.ru/ecos/emodel:2.40.0"}, nil))
	})
	t.Run("empty images → false", func(t *testing.T) {
		assert.False(t, namespaceNeedsUserSecrets(nil, ws))
	})
	t.Run("localhost auth-required host → true", func(t *testing.T) {
		localWs := &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
			{ID: "local", URL: "localhost", AuthType: "BASIC"},
		}}
		assert.True(t, namespaceNeedsUserSecrets(
			[]string{"localhost/team/img:tag"}, localWs))
	})
}

type fakeVault struct{ encrypted, locked bool }

func (f fakeVault) IsEncrypted() bool { return f.encrypted }
func (f fakeVault) IsLocked() bool    { return f.locked }

func TestShouldDeferStartForSecrets(t *testing.T) {
	ws := &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
		{ID: "ent", URL: "enterprise-registry.citeck.ru", AuthType: "BASIC"},
	}}
	entImg := []string{"enterprise-registry.citeck.ru/ecos/emodel:2.40.0"}
	pubImg := []string{"postgres:17.5"}

	assert.True(t, shouldDeferStartForSecrets(true, fakeVault{true, true}, false, entImg, ws),
		"desktop + encrypted+locked + needs-secrets ns → defer")
	assert.False(t, shouldDeferStartForSecrets(false, fakeVault{true, true}, false, entImg, ws),
		"server mode → never defer")
	assert.False(t, shouldDeferStartForSecrets(true, fakeVault{true, false}, false, entImg, ws),
		"unlocked vault → no defer")
	assert.False(t, shouldDeferStartForSecrets(true, fakeVault{false, false}, false, entImg, ws),
		"plain (unencrypted) vault → no defer")
	assert.False(t, shouldDeferStartForSecrets(true, fakeVault{true, true}, false, pubImg, ws),
		"community ns (public images) → no defer even when locked")
	// Only an untyped nil is exercised here. A typed-nil *storage.SecretService
	// boxed into this interface would panic inside IsEncrypted() (nil receiver
	// dereferences ss.mu) — production prevents that from ever reaching this
	// helper via the `in.SecretService != nil` guard at the namespace_loader.go
	// call site, so it's not exercised as a helper-level case.
	assert.False(t, shouldDeferStartForSecrets(true, nil, false, entImg, ws),
		"nil vault and no pending blob → no defer")
}

// TestShouldDeferStartForPendingKotlinSecrets covers the state that exists ONLY
// on the first boot after a 1.x → 2.x migration, and that both guards missed:
// the 1.x secrets are still an opaque blob waiting for the master password, so
// the SecretService is not encrypted and not locked — it is EMPTY.
//
// `encrypted && locked` is therefore false, the namespace auto-started, its
// private-registry pulls failed auth, and the registry-credentials dialog
// opened on top of the master-password prompt: a dead end, because picking a
// secret needs a vault that does not exist yet and saving a token answers 423.
// Reported by a migrating user, who was asked for a registry secret first and
// the master password second.
func TestShouldDeferStartForPendingKotlinSecrets(t *testing.T) {
	ws := &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
		{ID: "ent", URL: "enterprise-registry.citeck.ru", AuthType: "BASIC"},
	}}
	entImg := []string{"enterprise-registry.citeck.ru/ecos/emodel:2.40.0"}
	pubImg := []string{"postgres:17.5"}

	// The post-migration shape: nothing encrypted, nothing locked, blob pending.
	fresh := fakeVault{encrypted: false, locked: false}

	assert.True(t, shouldDeferStartForSecrets(true, fresh, true, entImg, ws),
		"a pending 1.x secrets blob must defer the start just like a locked vault")
	assert.False(t, shouldDeferStartForSecrets(true, fresh, false, entImg, ws),
		"no pending blob and an open vault: nothing to wait for")
	assert.False(t, shouldDeferStartForSecrets(true, fresh, true, pubImg, ws),
		"public images never need the vault, pending blob or not")
	assert.False(t, shouldDeferStartForSecrets(false, fresh, true, entImg, ws),
		"server mode auto-unlocks and never defers")
	assert.True(t, shouldDeferStartForSecrets(true, nil, true, entImg, ws),
		"a pending blob gates even before a SecretService exists")
}
