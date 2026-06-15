package namespace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestStartableAppImages_ExcludesDetached verifies the registry pre-start check
// input: it lists the active bundle's app images, dedupes them, and excludes
// detached (manually stopped) apps — so a registry used only by a detached app
// never triggers a credential prompt.
func TestStartableAppImages_ExcludesDetached(t *testing.T) {
	r := NewRuntime(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()

	r.mu.Lock()
	r.apps["emodel"] = &AppRuntime{Name: "emodel", Def: appdef.ApplicationDef{
		Name: "emodel", Image: "enterprise-registry.citeck.ru/ecos/emodel:1",
	}}
	r.apps["ai"] = &AppRuntime{Name: "ai", Def: appdef.ApplicationDef{
		Name: "ai", Image: "ai-only-registry.citeck.ru/ecos/ai:1", // unique host, detached
	}}
	r.apps["postgres"] = &AppRuntime{Name: "postgres", Def: appdef.ApplicationDef{
		Name: "postgres", Image: "postgres:15",
	}}
	r.apps["postgres-dup"] = &AppRuntime{Name: "postgres-dup", Def: appdef.ApplicationDef{
		Name: "postgres-dup", Image: "postgres:15", // duplicate image
	}}
	r.manualStoppedApps = map[string]bool{"ai": true}
	r.mu.Unlock()

	imgs := r.StartableAppImages()

	assert.ElementsMatch(t, []string{
		"enterprise-registry.citeck.ru/ecos/emodel:1",
		"postgres:15",
	}, imgs, "detached app image excluded; duplicates collapsed")
	assert.NotContains(t, imgs, "ai-only-registry.citeck.ru/ecos/ai:1", "detached app's image must not appear")
}
