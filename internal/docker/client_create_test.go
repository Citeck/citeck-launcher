package docker

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
)

// TestContainerNameOverrideIsScopedLikeAnApp pins that a name override goes
// through the SAME scoping as an app name: a temp migration container must
// carry the namespace (and, on desktop, the workspace) in its name, or two
// namespaces migrating at once would collide on one Docker name.
func TestContainerNameOverrideIsScopedLikeAnApp(t *testing.T) {
	server := &Client{namespace: "prod"}
	assert.Equal(t, "citeck_depsmig-src_prod", server.ContainerName("depsmig-src"))

	desktop := &Client{workspace: "default", namespace: "prod"}
	assert.Equal(t, "citeck_depsmig-src_prod_default", desktop.ContainerName("depsmig-src"))
}

// TestContainerLabelsWithoutOptsAreTheAppContainerLabels pins that the zero
// ContainerCreateOpts produces byte-identical labels to what CreateContainer
// has always written — every existing container adoption path keys off these.
func TestContainerLabelsWithoutOptsAreTheAppContainerLabels(t *testing.T) {
	c := &Client{workspace: "default", namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:16"}

	labels := c.containerLabels(app, app.Name, nil)

	assert.Equal(t, map[string]string{
		LabelLauncher:    "true",
		LabelWorkspace:   "default",
		LabelNamespace:   "prod",
		LabelAppName:     "postgres",
		LabelAppHash:     app.GetHash(),
		LabelComposeProj: c.composeProject(),
	}, labels)
}

// TestTempContainerLabelsCarryTheOverrideNameNotTheAppName is the reason the
// override is threaded into the labels at all. buildExistingContainerMap
// (runtime_app.go) and runReconcileDiffTask (runtime_workers.go) index the
// namespace's containers by LabelAppName, so a leftover
// citeck_depsmig-src_<ns> still labeled app.name=postgres would be ADOPTED as
// the namespace's postgres container: the reconciler would consider postgres
// running when the real one is not, and the hash diff would compare against a
// container the migration owns. The temp label marks it as the launcher's own,
// and the namespace labels are untouched so PurgeNamespace (which filters on
// LabelNamespace + workspace, never on LabelAppName) still removes it.
func TestTempContainerLabelsCarryTheOverrideNameNotTheAppName(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:16"}

	labels := c.containerLabels(app, "depsmig-src", map[string]string{LabelTemp: "true"})

	assert.Equal(t, "depsmig-src", labels[LabelAppName])
	assert.Equal(t, "true", labels[LabelTemp])
	// Still a container of this namespace, so the purge finds it.
	assert.Equal(t, "prod", labels[LabelNamespace])
	assert.Equal(t, "true", labels[LabelLauncher])
}

// TestExtraLabelsOverrideTheStandardOnes pins the merge direction: opts win.
func TestExtraLabelsOverrideTheStandardOnes(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:16"}

	labels := c.containerLabels(app, app.Name, map[string]string{
		LabelAppHash: "forced",
		"extra":      "x",
	})

	assert.Equal(t, "forced", labels[LabelAppHash])
	assert.Equal(t, "x", labels["extra"])
}

// TestContainerLabelsDoesNotMutateTheCallersExtraMap guards the seam against
// writing back into the caller's map — a migration reuses one opts value for
// its source and target containers.
func TestContainerLabelsDoesNotMutateTheCallersExtraMap(t *testing.T) {
	c := &Client{namespace: "prod"}
	extra := map[string]string{LabelTemp: "true"}

	c.containerLabels(appdef.ApplicationDef{Name: "postgres"}, "depsmig-src", extra)

	assert.Equal(t, map[string]string{LabelTemp: "true"}, extra)
}

// TestLabelTempValue pins the label key; the daemon filters on it.
func TestLabelTempValue(t *testing.T) {
	assert.Equal(t, "citeck.launcher.temp", LabelTemp)
}
