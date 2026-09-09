package docker

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
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

	name, overridden := effectiveName(app, opts)

	assert.Equal(t, "depsmig-src", name)
	assert.True(t, overridden)
	assert.Equal(t, "citeck_depsmig-src_prod", c.ContainerName(name))
	assert.Equal(t, "depsmig-src", c.containerLabels(app, name, nil)[LabelAppName])
	assert.Equal(t, []string{"depsmig-src"}, networkAliases(app, name, overridden))
}

// TestAnOverriddenContainerDoesNotAnswerToTheAppByHostnameEither is the third
// identity a name override has to move, and the least obvious one: moby
// registers a container's HOSTNAME as a DNS name on a user-defined network, so
// a temp container whose Config.Hostname was still "postgres" would keep
// answering to it even with the aliases cleared — the same silent traffic
// split networkAliases exists to prevent, arriving through a different door.
func TestAnOverriddenContainerDoesNotAnswerToTheAppByHostnameEither(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}

	name, _ := effectiveName(app, ContainerCreateOpts{Name: "depsmig-src"})
	cfg := buildContainerConfig(app, name, nil, nil, nil)

	assert.Equal(t, "depsmig-src", cfg.Hostname)
	assert.NotEqual(t, app.Name, cfg.Hostname)
}

// TestEffectiveNameWithoutAnOverrideIsTheAppName pins the existing caller:
// CreateContainer passes the zero opts and must keep getting app.Name.
func TestEffectiveNameWithoutAnOverrideIsTheAppName(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres"}
	name, overridden := effectiveName(app, ContainerCreateOpts{})
	assert.Equal(t, "postgres", name)
	assert.False(t, overridden)
}

// TestNetworkAliasesAreUnchangedWithoutAnOverride pins byte-identical
// behavior for every container the runtime creates: the app name first, then
// the def's additional aliases (mailhog's MAILHOG_TARGET host, the additional
// apps' declared aliases).
func TestNetworkAliasesAreUnchangedWithoutAnOverride(t *testing.T) {
	app := appdef.ApplicationDef{Name: "mailhog", NetworkAliases: []string{"mail", "smtp"}}
	name, overridden := effectiveName(app, ContainerCreateOpts{})
	assert.Equal(t, []string{"mailhog", "mail", "smtp"}, networkAliases(app, name, overridden))
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

	aliases := networkAliases(app, "depsmig-src", true)

	assert.Equal(t, []string{"depsmig-src"}, aliases)
	assert.NotContains(t, aliases, "postgres")
	assert.NotContains(t, aliases, "db")
	assert.NotContains(t, aliases, "primary")
}

// TestATempContainerIsCreatedWithNoRestartPolicy is the guard against an
// interrupted migration outliving the launcher. A temp container belongs to
// ONE operation, but it is created from the namespace's real def, so without
// an override it inherits unless-stopped: a launcher killed mid-migration — or
// a host reboot — would have Docker bring citeck_depsmig-dst_<ns> back with
// the TARGET data volume mounted, and the next start of the namespace's own
// postgres would put a second server on that same PGDATA. Nothing reads
// LabelTemp, so this policy is the only thing preventing that.
func TestATempContainerIsCreatedWithNoRestartPolicy(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}

	hc := buildHostConfig(app, ContainerCreateOpts{Name: "depsmig-dst", NoRestart: true},
		nil, nil, "citeck_net", 0, 0)

	assert.Equal(t, container.RestartPolicyDisabled, hc.RestartPolicy.Name)
}

// TestTheRestartPolicyIsUnchangedForEveryOtherContainer pins the two policies
// the launcher has always used, so the new override cannot leak into the
// namespace's own containers: an ordinary app restarts unless-stopped (that is
// what survives a host reboot), an init container runs once.
func TestTheRestartPolicyIsUnchangedForEveryOtherContainer(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres"}
	initApp := appdef.ApplicationDef{Name: "keycloak-init", IsInit: true}

	assert.Equal(t, container.RestartPolicyUnlessStopped,
		restartPolicyFor(app, ContainerCreateOpts{}).Name)
	assert.Equal(t, container.RestartPolicyUnlessStopped,
		restartPolicyFor(app, ContainerCreateOpts{Name: "depsmig-src"}).Name,
		"a name override alone is not a reason to change the policy")
	assert.Equal(t, container.RestartPolicyDisabled,
		restartPolicyFor(initApp, ContainerCreateOpts{}).Name)
	assert.Equal(t, container.RestartPolicyDisabled,
		restartPolicyFor(initApp, ContainerCreateOpts{NoRestart: true}).Name)
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

// TestAnOverrideEqualToTheAppsOwnNameIsStillAnOverride removes the last place
// the create path decided "is this a temp container?" by COMPARING STRINGS.
// effectiveName answered "" with app.Name, so an override a caller explicitly
// set to the app's own name was indistinguishable from no override at all, and
// networkAliases — which keyed off name != app.Name — silently handed that
// container the app's full DNS identity (its name plus every alias in the def).
// The answer is structural: an override is what the caller SET, never what it
// happens to equal.
func TestAnOverrideEqualToTheAppsOwnNameIsStillAnOverride(t *testing.T) {
	app := appdef.ApplicationDef{Name: "postgres", NetworkAliases: []string{"db", "primary"}}

	name, overridden := effectiveName(app, ContainerCreateOpts{Name: "postgres"})

	assert.Equal(t, "postgres", name)
	assert.True(t, overridden)
	assert.Equal(t, []string{"postgres"}, networkAliases(app, name, overridden),
		"an override drops the def's own aliases, whatever the override is spelled")

	name, overridden = effectiveName(app, ContainerCreateOpts{})

	assert.Equal(t, "postgres", name)
	assert.False(t, overridden)
	assert.Equal(t, []string{"postgres", "db", "primary"}, networkAliases(app, name, overridden),
		"the namespace's own container keeps its full identity")
}

// TestAnOverrideThatIsTheAppsOwnNameIsRefused is the loud half of the same
// finding. "It collides on the container name anyway" is only true while the
// app's container EXISTS: a migration runs with the namespace stopped, and if
// the real container has been removed (a recreate, a purge, a first start that
// never happened) the create would SUCCEED — producing a container named
// citeck_postgres_<ns> and labeled app.name=postgres, i.e. one the reconciler
// adopts as the namespace's own postgres while a migration owns its data
// volume. Docker's 409 is not a guard we may rely on, so the seam refuses
// before it asks the engine, and the message says what to use instead.
func TestAnOverrideThatIsTheAppsOwnNameIsRefused(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}

	_, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{
		Name:        "postgres",
		ExtraLabels: map[string]string{LabelTemp: LabelTempValue},
		NoRestart:   true,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres")

	// The namespace's own container — no override — is unaffected.
	own, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{})
	require.NoError(t, err)
	assert.Equal(t, "citeck_postgres_prod", own.Name)

	// So is a genuine override.
	temp, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{Name: "depsmig-src"})
	require.NoError(t, err)
	assert.Equal(t, "citeck_depsmig-src_prod", temp.Name)
}

// TestCreateOptionsCarryTheCallersOptsIntoEveryPartOfTheRequest pins the
// WIRING the reviewers could not reach: buildContainerConfig, containerLabels,
// buildHostConfig and networkAliases were each covered on their own, but
// nothing checked that CreateContainerWith hands its OWN opts to all four.
// Dropping opts at any one of those call sites — passing a zero
// ContainerCreateOpts to buildHostConfig is the easiest slip — brings back
// exactly the failure the override exists to prevent, with every unit test
// still green: a temp container that Docker restarts after a reboot, or one
// that answers to "postgres" on the namespace network.
//
// The whole request is assembled without touching the engine; only the final
// ContainerCreate call needs one.
func TestCreateOptionsCarryTheCallersOptsIntoEveryPartOfTheRequest(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{
		Name:           "postgres",
		Image:          "postgres:17.5",
		NetworkAliases: []string{"db"},
		Environments:   appdef.OrderedMap{{Key: "POSTGRES_PASSWORD", Value: "s3cret"}},
		Ports:          []string{"5432:5432"},
		Volumes:        []string{"/host/pgdata:/var/lib/postgresql/data"},
		Resources:      &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}},
		ShmSize:        "64m",
	}

	got, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{
		Name:        "depsmig-dst",
		ExtraLabels: map[string]string{LabelTemp: LabelTempValue},
		NoRestart:   true,
	})
	require.NoError(t, err)

	// opts.Name → the container name, the hostname, the app-name label and the
	// only network alias.
	assert.Equal(t, "citeck_depsmig-dst_prod", got.Name)
	assert.Equal(t, "depsmig-dst", got.Config.Hostname)
	assert.Equal(t, "depsmig-dst", got.Config.Labels[LabelAppName])
	assert.Equal(t, []string{"depsmig-dst"},
		got.NetworkingConfig.EndpointsConfig["citeck_network_prod"].Aliases)

	// opts.NoRestart → the host config. This is the assertion with no other
	// home: buildHostConfig is the only consumer of opts outside the name.
	assert.Equal(t, container.RestartPolicyDisabled, got.HostConfig.RestartPolicy.Name)

	// opts.ExtraLabels → the labels, without losing the launcher's own.
	assert.Equal(t, LabelTempValue, got.Config.Labels[LabelTemp])
	assert.Equal(t, "prod", got.Config.Labels[LabelNamespace])

	// ...and the def itself still reaches the request unchanged.
	assert.Equal(t, "postgres:17.5", got.Config.Image)
	assert.Equal(t, []string{"POSTGRES_PASSWORD=s3cret"}, got.Config.Env)
	assert.Equal(t, []string{"/host/pgdata:/var/lib/postgresql/data"}, got.HostConfig.Binds)
	assert.Equal(t, int64(512*1024*1024), got.HostConfig.Memory)
	assert.Equal(t, int64(512*1024*1024), got.HostConfig.MemorySwap)
	assert.Equal(t, int64(64*1024*1024), got.HostConfig.ShmSize)
	assert.Equal(t, container.NetworkMode("citeck_network_prod"), got.HostConfig.NetworkMode)
}

// TestCreateOptionsWithoutOptsAreTheNamespacesOwnContainer is the other side of
// the same seam: CreateContainer passes the zero opts, and everything a
// namespace container has always been created with must be byte-identical.
func TestCreateOptionsWithoutOptsAreTheNamespacesOwnContainer(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{
		Name:           "mailhog",
		Image:          "mailhog:latest",
		NetworkAliases: []string{"mail", "smtp"},
	}

	got, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{})
	require.NoError(t, err)

	assert.Equal(t, "citeck_mailhog_prod", got.Name)
	assert.Equal(t, "mailhog", got.Config.Hostname)
	assert.Equal(t, "mailhog", got.Config.Labels[LabelAppName])
	assert.NotContains(t, got.Config.Labels, LabelTemp)
	assert.Equal(t, []string{"mailhog", "mail", "smtp"},
		got.NetworkingConfig.EndpointsConfig["citeck_network_prod"].Aliases)
	assert.Equal(t, container.RestartPolicyUnlessStopped, got.HostConfig.RestartPolicy.Name)
}

// TestCreateOptionsRejectAnUnparsableContainerPort keeps the one error the
// assembly can raise on its own reaching the caller instead of a half-built
// request going to the engine.
func TestCreateOptionsRejectAnUnparsableContainerPort(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5", Ports: []string{"5432:not-a-port"}}

	_, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-port")
}

// TestExtraHostsReachTheContainerAndNotTheNetwork pins the mechanism that
// gives a temp container the app's NODE identity without giving it the app's
// DNS identity.
//
// RabbitMQ derives its node name from the container hostname and its data
// directory contains that node name, so a temp container running under the
// override name boots a fresh EMPTY node inside the data volume and reports
// healthy. Pinning the node identity needs the node name in the environment
// AND a container-local /etc/hosts entry for its host part — the environment
// alone fails the boot with "epmd error for host rabbitmq: nxdomain"
// (measured). ExtraHosts is that second half.
//
// It is deliberately NOT a Hostname override: moby registers a container's
// hostname as a DNS name on a user-defined network, so overriding it would
// make a temp container answer to "rabbitmq" on the namespace network. Both
// halves of that are asserted here — the hostname and the network aliases
// stay the TEMP name — because the whole point of choosing /etc/hosts was
// that it is container-local and invisible to Docker's DNS.
func TestExtraHostsReachTheContainerAndNotTheNetwork(t *testing.T) {
	c := &Client{namespace: "prod"}
	app := appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:4.1.2-management"}

	got, err := c.buildCreateOptions(context.Background(), app, "", ContainerCreateOpts{
		Name:       "depsmig-src",
		ExtraHosts: []string{"rabbitmq:127.0.0.1"},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"rabbitmq:127.0.0.1"}, got.HostConfig.ExtraHosts)
	assert.Equal(t, "depsmig-src", got.Config.Hostname,
		"the DNS identity stays the temp one: /etc/hosts is container-local, a hostname is not")
	assert.Equal(t, []string{"depsmig-src"},
		got.NetworkingConfig.EndpointsConfig["citeck_network_prod"].Aliases)
}

// A container the launcher creates for the namespace itself must carry no
// /etc/hosts entries of ours: the zero ContainerCreateOpts is what every app
// container is built from, and an entry leaking in there would shadow the
// namespace network's own DNS for that name.
func TestAnOrdinaryContainerGetsNoExtraHosts(t *testing.T) {
	c := &Client{namespace: "prod"}
	got, err := c.buildCreateOptions(context.Background(),
		appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:4.1.2-management"}, "", ContainerCreateOpts{})
	require.NoError(t, err)
	assert.Empty(t, got.HostConfig.ExtraHosts)
}
