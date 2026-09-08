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

	labels := c.containerLabels(app, "depsmig-src", map[string]string{LabelTemp: LabelTempValue})

	assert.Equal(t, "depsmig-src", labels[LabelAppName])
	assert.Equal(t, "true", labels[LabelTemp])
	// Still a container of this namespace, so the purge finds it.
	assert.Equal(t, "prod", labels[LabelNamespace])
	assert.Equal(t, "true", labels[LabelLauncher])
}

// TestExtraLabelsCannotClobberLauncherOwnedLabels pins the merge DIRECTION: a
// caller may add labels but may never take one away. LabelNamespace is the
// dangerous one — PurgeNamespace finds a container only by that label
// (purge.go filters on it and matches the workspace; it never reads
// LabelAppName), so a container whose extra labels blanked it would survive
// the purge still holding its data volume, invisible to every launcher
// surface. The same goes for LabelLauncher, LabelWorkspace, LabelAppName and
// LabelAppHash: each one is how some part of the launcher recognizes its own
// containers, and none of them is a caller's to redefine.
func TestExtraLabelsCannotClobberLauncherOwnedLabels(t *testing.T) {
	c := &Client{workspace: "default", namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:16"}

	labels := c.containerLabels(app, "depsmig-src", map[string]string{
		LabelLauncher:    "nope",
		LabelWorkspace:   "somewhere-else",
		LabelNamespace:   "", // the leak: purge would never see this container
		LabelAppName:     "postgres",
		LabelAppHash:     "forced",
		LabelComposeProj: "hijacked",
		LabelTemp:        LabelTempValue,
		"extra":          "x",
	})

	assert.Equal(t, "true", labels[LabelLauncher])
	assert.Equal(t, "default", labels[LabelWorkspace])
	assert.Equal(t, "prod", labels[LabelNamespace])
	assert.Equal(t, "depsmig-src", labels[LabelAppName])
	assert.Equal(t, app.GetHash(), labels[LabelAppHash])
	assert.Equal(t, c.composeProject(), labels[LabelComposeProj])

	// Labels the launcher does not own still land — that is the whole point of
	// the seam.
	assert.Equal(t, LabelTempValue, labels[LabelTemp])
	assert.Equal(t, "x", labels["extra"])
}

// TestEffectiveNameFeedsTheContainerNameNotJustTheLabels pins that the ONE
// resolved name reaches the Docker container name. Labels and the container
// name are read by different parts of the launcher, so a version that named
// the container after app.Name while labeling it with the override would look
// correct in every label test and still do the damage this seam exists to
// prevent: a temp container created as citeck_postgres_<ns> collides with the
// namespace's real postgres container.
func TestEffectiveNameFeedsTheContainerNameNotJustTheLabels(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:16"}
	opts := ContainerCreateOpts{Name: "depsmig-src"}

	name := effectiveName(app, opts)

	assert.Equal(t, "depsmig-src", name)
	assert.Equal(t, "citeck_depsmig-src_prod", c.ContainerName(name))
	assert.Equal(t, "depsmig-src", c.containerLabels(app, name, nil)[LabelAppName])
	assert.Equal(t, []string{"depsmig-src"}, networkAliases(app, name))
}

// TestAnOverriddenContainerDoesNotAnswerToTheAppByHostnameEither is the third
// identity a name override has to move, and the least obvious one: moby
// registers a container's HOSTNAME as a DNS name on a user-defined network, so
// a temp container whose Config.Hostname was still "postgres" would keep
// answering to it even with the aliases cleared — the same silent traffic
// split networkAliases exists to prevent, arriving through a different door.
func TestAnOverriddenContainerDoesNotAnswerToTheAppByHostnameEither(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}

	cfg := buildContainerConfig(app, effectiveName(app, ContainerCreateOpts{Name: "depsmig-src"}), nil, nil, nil)

	assert.Equal(t, "depsmig-src", cfg.Hostname)
	assert.NotEqual(t, app.Name, cfg.Hostname)
}

// TestEffectiveNameWithoutAnOverrideIsTheAppName pins the existing caller:
// CreateContainer passes the zero opts and must keep getting app.Name.
func TestEffectiveNameWithoutAnOverrideIsTheAppName(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres"}
	assert.Equal(t, "postgres", effectiveName(app, ContainerCreateOpts{}))
}

// TestNetworkAliasesAreUnchangedWithoutAnOverride pins byte-identical
// behavior for every container the runtime creates: the app name first, then
// the def's additional aliases (mailhog's MAILHOG_TARGET host, the additional
// apps' declared aliases).
func TestNetworkAliasesAreUnchangedWithoutAnOverride(t *testing.T) {
	app := appdef.ApplicationDef{Name: "mailhog", NetworkAliases: []string{"mail", "smtp"}}
	assert.Equal(t, []string{"mailhog", "mail", "smtp"}, networkAliases(app, effectiveName(app, ContainerCreateOpts{})))
}

// TestAnOverriddenContainerDoesNotAnswerToTheAppOnTheNetwork is the other half
// of the collision guard, and the one a call site could not have fixed for
// itself: the primary alias comes from app.Name, not from def.NetworkAliases,
// so clearing that field before the call would have left "postgres" in place.
// Docker allows duplicate aliases on one network and round-robins between the
// endpoints holding them, so a temp container answering to "postgres" would
// take a silent share of the real container's connections — no error, no log,
// a different database per connection.
func TestAnOverriddenContainerDoesNotAnswerToTheAppOnTheNetwork(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres", NetworkAliases: []string{"db", "primary"}}

	aliases := networkAliases(app, "depsmig-src")

	assert.Equal(t, []string{"depsmig-src"}, aliases)
	assert.NotContains(t, aliases, "postgres")
	assert.NotContains(t, aliases, "db")
	assert.NotContains(t, aliases, "primary")
}

// TestContainerLabelsDoesNotMutateTheCallersExtraMap guards the seam against
// writing back into the caller's map — a migration reuses one opts value for
// its source and target containers.
func TestContainerLabelsDoesNotMutateTheCallersExtraMap(t *testing.T) {
	c := &Client{namespace: "prod"}
	extra := map[string]string{LabelTemp: LabelTempValue}

	c.containerLabels(appdef.ApplicationDef{Name: "postgres"}, "depsmig-src", extra)

	assert.Equal(t, map[string]string{LabelTemp: LabelTempValue}, extra)
}

// TestLabelTempKeyAndValue pins both halves of the temp marker. The key is
// what every filter matches on (Docker's label filter treats a bare key as
// "present with any value", so lookups are key-only); the value exists so the
// writers cannot drift into "1" vs "true" for a reader that ever does compare.
func TestLabelTempKeyAndValue(t *testing.T) {
	assert.Equal(t, "citeck.launcher.temp", LabelTemp)
	assert.Equal(t, "true", LabelTempValue)
}
