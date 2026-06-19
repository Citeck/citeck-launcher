package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
)

// TestRegenOnAttachToggle pins the Kotlin-parity set of apps whose attach/detach
// state changes OTHER apps' generated config (proxy upstreams, AI↔STT wiring),
// so toggling them at runtime must regenerate the namespace rather than just
// start/stop the single container. Kotlin: NamespaceGenerator's static
// dependsOnDetachedApps set {ONLYOFFICE, AI, STT_SIDECAR} + detachedAppsChanged
// (v1.4.1 changelog).
func TestRegenOnAttachToggle(t *testing.T) {
	for _, name := range []string{appdef.AppOnlyoffice, appdef.AppAi, appdef.AppSttSidecar} {
		assert.True(t, regenOnAttachToggle(name), "toggling %q must regenerate the namespace", name)
	}
	for _, name := range []string{appdef.AppGateway, appdef.AppProxy, appdef.AppEmodel, "postgres", ""} {
		assert.False(t, regenOnAttachToggle(name), "toggling %q must NOT regenerate the namespace", name)
	}
}
