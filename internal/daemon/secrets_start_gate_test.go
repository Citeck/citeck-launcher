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

	assert.True(t, shouldDeferStartForSecrets(true, fakeVault{true, true}, entImg, ws),
		"desktop + encrypted+locked + needs-secrets ns → defer")
	assert.False(t, shouldDeferStartForSecrets(false, fakeVault{true, true}, entImg, ws),
		"server mode → never defer")
	assert.False(t, shouldDeferStartForSecrets(true, fakeVault{true, false}, entImg, ws),
		"unlocked vault → no defer")
	assert.False(t, shouldDeferStartForSecrets(true, fakeVault{false, false}, entImg, ws),
		"plain (unencrypted) vault → no defer")
	assert.False(t, shouldDeferStartForSecrets(true, fakeVault{true, true}, pubImg, ws),
		"community ns (public images) → no defer even when locked")
	// Only an untyped nil is exercised here. A typed-nil *storage.SecretService
	// boxed into this interface would panic inside IsEncrypted() (nil receiver
	// dereferences ss.mu) — production prevents that from ever reaching this
	// helper via the `in.SecretService != nil` guard at the namespace_loader.go
	// call site, so it's not exercised as a helper-level case.
	assert.False(t, shouldDeferStartForSecrets(true, nil, entImg, ws),
		"nil vault → no defer")
}
