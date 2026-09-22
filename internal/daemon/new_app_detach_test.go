package daemon

import (
	"context"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ndApps(names ...string) []appdef.ApplicationDef {
	out := make([]appdef.ApplicationDef, 0, len(names))
	for _, n := range names {
		out = append(out, appdef.ApplicationDef{Name: n})
	}
	return out
}

// ndWorkspace builds a workspace whose "default" template carries the given
// list, beside a second template that must never be read.
func ndWorkspace(detached ...string) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{NamespaceTemplates: []bundle.NamespaceTemplate{
		{ID: "other", DetachedApps: []string{"everything"}},
		{ID: "default", DetachedApps: detached},
	}}
}

func ndConfig(templateID string) *namespace.Config {
	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	cfg.Template = templateID
	return &cfg
}

// The cheap gate in front of the expensive part. Every ordinary load and reload
// — all of them but the one after a bundle grew the app set — must not pay a
// forced git fetch or a Docker probe, so nothing may be consulted at all.
func TestNothingIsConsultedWhenNoAppIsNew(t *testing.T) {
	lookups, probes := 0, 0
	res := decideNewAppDetach(
		[]string{appdef.AppPostgres, appdef.AppRag},
		ndApps(appdef.AppPostgres, appdef.AppRag),
		ndConfig("default"), ndWorkspace(appdef.AppRag),
		func(string, *bundle.WorkspaceConfig) ([]string, bool) { lookups++; return nil, true },
		func(string) namespace.AppPresence { probes++; return namespace.AppPresenceAbsent },
	)

	assert.Zero(t, lookups, "no new app, no forced workspace pull")
	assert.Zero(t, probes, "and no Docker call either")
	assert.Equal(t, []string{appdef.AppPostgres, appdef.AppRag}, res.Known,
		"the known set is handed back untouched so the caller can install it unconditionally")
}

// The whole point, at the wiring level: the template of the namespace's OWN
// template id decides, and the answer reaches the operator's detach set.
func TestANewAppListedByTheNamespacesOwnTemplateIsDetached(t *testing.T) {
	res := decideNewAppDetach(
		[]string{appdef.AppPostgres},
		ndApps(appdef.AppPostgres, appdef.AppRag, appdef.AppQdrant),
		ndConfig("default"), ndWorkspace(appdef.AppRag),
		nil, // no seam: read the config in hand
		func(string) namespace.AppPresence { return namespace.AppPresenceAbsent },
	)

	assert.Equal(t, []string{appdef.AppRag}, res.Detach,
		"the 'other' template's list must not reach this namespace")
	assert.Contains(t, res.Known, appdef.AppQdrant)
}

// The freshness rule: when the set DID grow, the template list is re-read
// through the force-pull seam rather than from the config this reload happens
// to hold, which can be an hour older than the bundle that introduced the app.
func TestAGrownAppSetRereadsTheTemplateThroughTheSeam(t *testing.T) {
	called := 0
	res := decideNewAppDetach(
		[]string{appdef.AppPostgres},
		ndApps(appdef.AppPostgres, appdef.AppRag),
		ndConfig("default"),
		ndWorkspace(), // the stale config does NOT list rag yet
		func(templateID string, fallback *bundle.WorkspaceConfig) ([]string, bool) {
			called++
			assert.Equal(t, "default", templateID)
			require.NotNil(t, fallback, "the seam gets the config in hand as its fallback")
			return []string{appdef.AppRag}, true // what the fresh workspace repo says
		},
		func(string) namespace.AppPresence { return namespace.AppPresenceAbsent },
	)

	assert.Equal(t, 1, called)
	assert.Equal(t, []string{appdef.AppRag}, res.Detach,
		"deciding from the stale config would have started the new app once, permanently")
}

func TestTemplateDetachedAppsReadsOnlyTheNamedTemplate(t *testing.T) {
	ws := ndWorkspace(appdef.AppAi)
	assert.Equal(t, []string{appdef.AppAi}, templateDetachedApps(ws, "default"))
	assert.Nil(t, templateDetachedApps(ws, "gone"), "a template the workspace dropped answers nothing")
	assert.Nil(t, templateDetachedApps(ws, ""), "and a namespace created without one has nobody to ask")
	assert.Nil(t, templateDetachedApps(nil, "default"))
}

func TestHasUnseenAppFallsBackToTheBaseline(t *testing.T) {
	assert.False(t, hasUnseenApp(nil, []string{appdef.AppPostgres, appdef.AppAi}),
		"a state file with no known set is not a namespace where everything is new")
	assert.True(t, hasUnseenApp(nil, []string{appdef.AppPostgres, appdef.AppQdrant}))
	assert.False(t, hasUnseenApp([]string{appdef.AppQdrant}, []string{appdef.AppQdrant}))
	assert.False(t, hasUnseenApp([]string{appdef.AppPostgres}, nil))
}

// The load path has no runtime yet, so a container is the only proof that an
// app is not new to this stand — and a failed inspect is not proof of anything.
func TestDockerPresenceSeparatesAbsentFromUnanswered(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.inspectErr[fake.ContainerName("gone")] = notFoundErr{}
	fake.inspectErr[fake.ContainerName("unreachable")] = assert.AnError

	presence := dockerPresence(context.Background(), fake)
	assert.Equal(t, namespace.AppPresenceAbsent, presence("gone"))
	assert.Equal(t, namespace.AppPresenceUnknown, presence("unreachable"))
	assert.Equal(t, namespace.AppPresencePresent, presence("running"),
		"an inspect that answers is an app this stand already has")

	assert.Equal(t, namespace.AppPresenceUnknown, dockerPresence(context.Background(), nil)("x"),
		"no Docker client is 'I could not ask', never 'it is not there'")
}

// The reload path's probe, and the trap it closes: a runtime ENTRY is not
// evidence that this stand has ever run the app. Every generated app gets one
// (doStart / doRegenerate seed r.apps from the generated set), so a candidate
// DEFERRED on the load path — Docker unreachable, nothing decided, nothing
// recorded — is in the table by the next reload, and reading the table alone
// would answer the deferred question with the one fact that cannot settle it.
func TestRuntimePresenceDemandsAContainerNotAnAppTableEntry(t *testing.T) {
	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	rt := namespace.NewRuntime(&cfg, nil, t.TempDir())
	rt.InjectAppsForTest(&namespace.AppRuntime{
		Name:   appdef.AppSttSidecar, // seeded by a generation, never started
		Status: namespace.AppStatusStopped,
		Def:    appdef.ApplicationDef{Name: appdef.AppSttSidecar},
	}, &namespace.AppRuntime{
		Name:        appdef.AppAi,
		Status:      namespace.AppStatusRunning,
		ContainerID: "container-1",
		Def:         appdef.ApplicationDef{Name: appdef.AppAi},
	})

	fake := newFakeDepsDocker()
	fake.inspectErr[fake.ContainerName(appdef.AppSttSidecar)] = notFoundErr{}
	fake.inspectErr[fake.ContainerName(appdef.AppRag)] = notFoundErr{}
	presence := runtimePresence(context.Background(), rt, fake)

	assert.Equal(t, namespace.AppPresencePresent, presence(appdef.AppAi),
		"a container id is the runtime's own evidence and short-circuits the probe")
	assert.Equal(t, namespace.AppPresenceAbsent, presence(appdef.AppSttSidecar),
		"an entry with no container is not proof this stand has run the app")
	assert.Equal(t, namespace.AppPresenceAbsent, presence(appdef.AppRag))

	// And with Docker unreachable the entry still decides nothing.
	down := newFakeDepsDocker()
	down.inspectErr[down.ContainerName(appdef.AppSttSidecar)] = assert.AnError
	assert.Equal(t, namespace.AppPresenceUnknown,
		runtimePresence(context.Background(), rt, down)(appdef.AppSttSidecar))
}

// The other half of the deferral promise, on the template side: the one load
// where the workspace repo would not sync is exactly the load where the entry
// for a brand-new app may be the one missing from the cached list. Recording it
// as known would close hasUnseenApp's gate behind it and the question would
// never be asked again.
func TestAStaleTemplateDefersInsteadOfRecording(t *testing.T) {
	res := decideNewAppDetach(
		[]string{appdef.AppPostgres},
		ndApps(appdef.AppPostgres, appdef.AppRag, appdef.AppAi),
		ndConfig("default"), ndWorkspace(appdef.AppAi),
		func(string, *bundle.WorkspaceConfig) ([]string, bool) {
			return []string{appdef.AppAi}, false // cached list; the pull failed
		},
		func(string) namespace.AppPresence { return namespace.AppPresenceAbsent },
	)

	assert.Equal(t, []string{appdef.AppRag}, res.Deferred,
		"the cached list does not name it, and a failed pull cannot tell that from 'not listed'")
	assert.NotContains(t, res.Known, appdef.AppRag, "so the next pass asks again")
	assert.Equal(t, []string{appdef.AppAi}, res.Detach,
		"an app the stale list DOES name is still the operator's stated intent")
}

// A namespace created from a template the workspace no longer declares has
// nobody to ask — the answer must be "nothing detached", never the first or the
// nearest template's list.
func TestANamespaceWhoseTemplateIsGoneRecordsButDetachesNothing(t *testing.T) {
	res := decideNewAppDetach(
		[]string{appdef.AppPostgres},
		ndApps(appdef.AppPostgres, appdef.AppRag),
		ndConfig("gone"), ndWorkspace(appdef.AppRag),
		nil,
		func(string) namespace.AppPresence { return namespace.AppPresenceAbsent },
	)

	assert.Empty(t, res.Detach)
	assert.Contains(t, res.Known, appdef.AppRag, "and it is recorded, so the question is asked once")
}
