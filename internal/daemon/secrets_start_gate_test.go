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
}
