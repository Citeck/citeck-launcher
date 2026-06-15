package namespace

import (
	"context"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	dcontainer "github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostPortOf(t *testing.T) {
	cases := map[string]int{
		"1025:1025":           1025, // host:container
		"8070:80":             8070, // host:container (differing)
		"127.0.0.1:1025:1025": 1025, // ip:host:container
		"8025":                0,    // bare container port (not published)
		"":                    0,
		"abc:def":             0,
	}
	for in, want := range cases {
		assert.Equal(t, want, hostPortOf(in), "hostPortOf(%q)", in)
	}
}

// TestDetectHostPortConflicts covers the cross-namespace fixed-port collision
// that the mailhog/onlyoffice leak produced: a running launcher container from
// a DIFFERENT namespace publishes a host port that one of our apps needs.
func TestDetectHostPortConflicts(t *testing.T) {
	apps := []appdef.ApplicationDef{
		{Name: "mailhog", Ports: []string{"1025:1025"}},
		{Name: "onlyoffice", Ports: []string{"8070:80"}},
		{Name: "eapps"}, // no published host ports
	}
	foreign := []dcontainer.Summary{
		{ // foreign ns holds 1025 — our launcher container
			ID:     "id-mailhog-nwkzgya",
			Names:  []string{"/citeck_mailhog_nwkzgya_default"},
			State:  "running",
			Labels: map[string]string{docker.LabelLauncher: "true", docker.LabelNamespace: "nwkzgya"},
			Ports:  []dcontainer.PortSummary{{PublicPort: 1025, PrivatePort: 1025}},
		},
		{ // foreign ns holds 8070 — our launcher container
			ID:     "id-onlyoffice-nwkzgya",
			Names:  []string{"/citeck_onlyoffice_nwkzgya_default"},
			State:  "running",
			Labels: map[string]string{docker.LabelLauncher: "true", docker.LabelNamespace: "nwkzgya"},
			Ports:  []dcontainer.PortSummary{{PublicPort: 8070, PrivatePort: 80}},
		},
		{ // OUR namespace — handled by regenerate/adopt, must be ignored
			Names:  []string{"/citeck_mailhog_3v3ithq_tbxzxxa"},
			State:  "running",
			Labels: map[string]string{docker.LabelLauncher: "true", docker.LabelNamespace: "3v3ithq"},
			Ports:  []dcontainer.PortSummary{{PublicPort: 1025}},
		},
		{ // foreign but not running — must be ignored
			Names:  []string{"/citeck_pgadmin_nwkzgya_default"},
			State:  "exited",
			Labels: map[string]string{docker.LabelLauncher: "true", docker.LabelNamespace: "nwkzgya"},
			Ports:  []dcontainer.PortSummary{{PublicPort: 8070}},
		},
	}

	conflicts := detectHostPortConflicts(apps, foreign, "3v3ithq")

	require.Len(t, conflicts, 2)
	byPort := map[int]portConflict{}
	for _, c := range conflicts {
		byPort[c.hostPort] = c
	}
	assert.Equal(t, "mailhog", byPort[1025].app)
	assert.Equal(t, "nwkzgya", byPort[1025].ns)
	assert.Equal(t, "id-mailhog-nwkzgya", byPort[1025].id)
	assert.Equal(t, "onlyoffice", byPort[8070].app)
	assert.Equal(t, "nwkzgya", byPort[8070].ns)
	assert.Equal(t, "id-onlyoffice-nwkzgya", byPort[8070].id)
}

// TestResolveHostPortConflictsStopsOnlyOurForeignContainer verifies the
// end-to-end behavior: a launcher container from another namespace holding a
// needed published port is stopped, while a third-party container on the same
// port is left untouched.
func TestResolveHostPortConflictsStopsOnlyOurForeignContainer(t *testing.T) {
	md := newMockDocker()
	md.allLauncherList = []dcontainer.Summary{
		{ // ours, other namespace — must be stopped
			ID:     "id-foreign-ours",
			Names:  []string{"/citeck_mailhog_nwkzgya_default"},
			State:  "running",
			Labels: map[string]string{docker.LabelLauncher: "true", docker.LabelNamespace: "nwkzgya"},
			Ports:  []dcontainer.PortSummary{{PublicPort: 1025}},
		},
		{ // NOT ours — must never be stopped, even though it holds the port
			ID:     "id-thirdparty",
			Names:  []string{"/someones-smtp"},
			State:  "running",
			Labels: map[string]string{},
			Ports:  []dcontainer.PortSummary{{PublicPort: 1025}},
		},
	}
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()
	r.nsID = "3v3ithq" // distinct from the foreign "nwkzgya"

	r.resolveHostPortConflicts(context.Background(), []appdef.ApplicationDef{
		{Name: "mailhog", Ports: []string{"1025:1025"}},
	})

	md.mu.Lock()
	stopped := append([]string(nil), md.stoppedNames...)
	md.mu.Unlock()
	assert.Contains(t, stopped, "id-foreign-ours", "our container from another namespace must be stopped to free the port")
	assert.NotContains(t, stopped, "id-thirdparty", "third-party container must never be stopped")
}

// Safety: a non-launcher container (one we did NOT create) holding the port
// must never be flagged — we must never stop containers we don't own.
func TestDetectHostPortConflictsIgnoresNonLauncherContainer(t *testing.T) {
	apps := []appdef.ApplicationDef{{Name: "mailhog", Ports: []string{"1025:1025"}}}
	foreign := []dcontainer.Summary{{
		ID:     "id-some-other-app",
		Names:  []string{"/someones-smtp-server"},
		State:  "running",
		Labels: map[string]string{}, // no citeck.launcher label
		Ports:  []dcontainer.PortSummary{{PublicPort: 1025}},
	}}
	assert.Empty(t, detectHostPortConflicts(apps, foreign, "3v3ithq"))
}

// Our own launcher container, same namespace, holding the port must be ignored
// (regenerate/adopt handles it). The fixture carries the launcher label so the
// label guard passes and the test actually exercises the own-namespace branch.
func TestDetectHostPortConflictsIgnoresOwnNamespace(t *testing.T) {
	apps := []appdef.ApplicationDef{{Name: "mailhog", Ports: []string{"1025:1025"}}}
	foreign := []dcontainer.Summary{{
		ID:     "id-own",
		Names:  []string{"/citeck_mailhog_3v3ithq_tbxzxxa"},
		State:  "running",
		Labels: map[string]string{docker.LabelLauncher: "true", docker.LabelNamespace: "3v3ithq"},
		Ports:  []dcontainer.PortSummary{{PublicPort: 1025}},
	}}
	assert.Empty(t, detectHostPortConflicts(apps, foreign, "3v3ithq"))
}
