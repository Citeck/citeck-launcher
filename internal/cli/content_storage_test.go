package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

func s3Namespace(cs *api.ContentStorageDto) *api.NamespaceDto {
	return &api.NamespaceDto{ID: "default", Name: "Citeck", Status: "RUNNING", ContentStorage: cs}
}

// --- citeck status ---

// A DB namespace — every namespace that predates the option — prints no
// content-storage line at all.
func TestContentStorageStatusLine_DBNamespacePrintsNothing(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	assert.Empty(t, contentStorageStatusLine(nil))
	assert.Empty(t, contentStorageStatusLine(&api.NamespaceDto{}))
}

func TestContentStorageStatusLine_SaysWhereTheConnectionIs(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	cases := []struct {
		state, err string
		want       []string
	}{
		{api.ContentStorageStatePending, "", []string{"S3 (seaweedfs)", "waiting for the platform"}},
		{"", "", []string{"waiting for the platform"}},
		{api.ContentStorageStateWaiting, "", []string{"waiting for ECOS"}},
		{api.ContentStorageStateProvisioning, "", []string{"connecting ecos-content"}},
		{api.ContentStorageStateConnected, "", []string{"S3 (seaweedfs)", "connected"}},
		{api.ContentStorageStateAdminManaged, "", []string{"administrator", "changes nothing"}},
		{api.ContentStorageStateError, "gateway answered 500", []string{"connection failed", "gateway answered 500", "retries automatically"}},
		// A state a newer daemon reports and this CLI does not know is shown
		// as it came, not dropped or guessed.
		{"migrating", "", []string{"migrating"}},
	}
	for _, tc := range cases {
		line := contentStorageStatusLine(s3Namespace(&api.ContentStorageDto{
			Type: "S3", ProviderApp: "seaweedfs", State: tc.state, Error: tc.err,
		}))
		for _, w := range tc.want {
			assert.Contains(t, line, w, "state %q", tc.state)
		}
	}
}

// A STOPPED namespace is not waiting for anything: the daemon keeps no
// "connected" flag, so every S3 namespace reads pending while stopped (and
// right after a daemon restart), connected or not. The web banner shows
// nothing there; the CLI must not print a yellow "waiting" line either.
func TestContentStorageStatusLine_AStoppedNamespaceIsNotWaiting(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	for _, state := range []string{api.ContentStorageStatePending, ""} {
		ns := s3Namespace(&api.ContentStorageDto{Type: "S3", ProviderApp: "seaweedfs", State: state})
		ns.Status = "STOPPED"
		line := contentStorageStatusLine(ns)
		assert.Contains(t, line, "checked when the namespace starts", "state %q", state)
		assert.NotContains(t, line, "waiting", "state %q", state)
	}

	// A verdict kept from before the stop is still said as it is, and a
	// problem still outranks everything.
	ns := s3Namespace(&api.ContentStorageDto{Type: "S3", ProviderApp: "seaweedfs", State: api.ContentStorageStateConnected})
	ns.Status = "STOPPED"
	assert.Contains(t, contentStorageStatusLine(ns), "connected")
	ns.ContentStorage.Problem = namespace.ContentStorageProviderNotGenerated
	assert.Contains(t, contentStorageStatusLine(ns), "not connected")
}

// The label is the workspace's product name when it gives one — the label
// the web dialog and the install wizard show.
func TestContentStorageStatusLine_UsesTheProviderTitle(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	line := contentStorageStatusLine(s3Namespace(&api.ContentStorageDto{
		Type: "S3", ProviderApp: "seaweedfs", ProviderTitle: "SeaweedFS", State: api.ContentStorageStateConnected,
	}))
	assert.True(t, strings.HasPrefix(line, "S3 (SeaweedFS) — "), line)
}

// A generation problem means the provider is not wired at all: the connection
// state is moot until the configuration is fixed, so the problem is what the
// line says.
func TestContentStorageStatusLine_AProblemOutranksTheState(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	cases := map[string]string{
		namespace.ContentStorageNoContentApp:         "no ecos-content",
		namespace.ContentStorageNoProviderConfig:     "contentStorage.s3",
		namespace.ContentStorageNoProviderApp:        "additionalApps entry seaweedfs",
		namespace.ContentStorageNoProviderImage:      "image for seaweedfs",
		namespace.ContentStorageProviderNotGenerated: "seaweedfs is not part of this namespace",
		namespace.ContentStorageProviderConflict:     "seaweedfs cannot be the S3 provider",
		"SOMETHING_NEW": "SOMETHING_NEW",
	}
	for code, want := range cases {
		line := contentStorageStatusLine(s3Namespace(&api.ContentStorageDto{
			Type: "S3", ProviderApp: "seaweedfs", State: api.ContentStorageStateConnected, Problem: code,
		}))
		assert.Contains(t, line, "not connected", code)
		assert.Contains(t, line, want, code)
		assert.NotContains(t, line, "— connected", "the state must not be reported beside a problem (%s)", code)
	}
}

// The labels line up whichever lines a namespace has. A DB namespace keeps the
// header it always had (labels padded to "License:"); an S3 namespace widens
// it to its own longest label instead of leaving one value out of line.
func TestStatusHeaderLines_ContentStorageLineIsAlignedAndOnlyOnS3(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)

	db := statusHeaderLines(&api.NamespaceDto{Name: "Citeck", Status: "RUNNING", BundleRef: "community:2026.3"}, nil, nil)
	require.Equal(t, []string{
		"Name:     Citeck",
		"Status:   RUNNING",
		"Bundle:   community:2026.3",
	}, db, "a DB namespace's header is byte-identical to the one before the content-storage line existed")

	s3 := statusHeaderLines(s3Namespace(&api.ContentStorageDto{
		Type: "S3", ProviderApp: "seaweedfs", State: api.ContentStorageStateConnected,
	}), nil, nil)
	require.Len(t, s3, 3)
	assert.Equal(t, "Name:             Citeck", s3[0])
	assert.Equal(t, "Content storage:  S3 (seaweedfs) — connected", s3[2])
	valueCol := strings.Index(s3[2], "S3 (")
	assert.Equal(t, valueCol, strings.Index(s3[0], "Citeck"), "values start in one column")
}

// --- citeck stop <provider> ---

func TestContentStorageStopWarnings(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	s3 := s3Namespace(&api.ContentStorageDto{Type: "S3", ProviderApp: "seaweedfs"})

	got := contentStorageStopWarnings(s3, []string{"onlyoffice", "seaweedfs"})
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "seaweedfs is the S3 content storage")
	assert.Contains(t, got[0], "citeck start seaweedfs")

	assert.Nil(t, contentStorageStopWarnings(s3, []string{"onlyoffice"}), "another app is no concern of it")
	assert.Nil(t, contentStorageStopWarnings(&api.NamespaceDto{}, []string{"seaweedfs"}),
		"a DB namespace does not need the app, so stopping it is unremarkable")
	assert.Nil(t, contentStorageStopWarnings(nil, []string{"seaweedfs"}))
}

// End to end: stopping the provider of an S3 namespace is NOT refused — the
// stop goes through and the command exits 0 — but the operator is told, on
// stderr (stdout belongs to --format json), what that costs.
func TestStopProviderWarnsButStops(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	f := &appDaemonSocket{contentStorage: &api.ContentStorageDto{Type: "S3", ProviderApp: "seaweedfs"}}
	f.serve(t)

	cmd := newStopCmd()
	cmd.SetArgs([]string{"seaweedfs"})
	var runErr error
	stderr := captureStderr(t, func() { runErr = cmd.Execute() })

	require.NoError(t, runErr, "a warning, never a refusal")
	assert.Equal(t, []string{"seaweedfs"}, f.stopped)
	assert.Contains(t, stderr, "seaweedfs is the S3 content storage")
}

// Stopping any other app on an S3 namespace says nothing extra.
func TestStopAnotherAppOnAnS3NamespaceIsQuiet(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	f := &appDaemonSocket{contentStorage: &api.ContentStorageDto{Type: "S3", ProviderApp: "seaweedfs"}}
	f.serve(t)

	cmd := newStopCmd()
	cmd.SetArgs([]string{"onlyoffice"})
	var runErr error
	stderr := captureStderr(t, func() { runErr = cmd.Execute() })

	require.NoError(t, runErr)
	assert.Empty(t, stderr)
}

// --- citeck install ---

const csInstallWorkspace = `bundleRepos:
  - id: community
    path: community
contentStorage:
  s3:
    app: seaweedfs
    url: http://seaweedfs:8333
    bucket: ecos-content
    accessKey: citeck
    secretKey: ${secret:seaweedfs-s3}
secrets:
  - id: seaweedfs-s3
    value: seaweedfs-s3-secret
additionalApps:
  - name: seaweedfs
    environments:
      AWS_SECRET_ACCESS_KEY: ${secret:seaweedfs-s3}
`

// writeInstallContentStorageWorkspace lays out a manual-import workspace (the
// --workspace zip path) with three releases: one that can store content in
// S3, one whose release names no provider image, and a Community one.
func writeInstallContentStorageWorkspace(t *testing.T) {
	t.Helper()
	t.Setenv("CITECK_HOME", t.TempDir())
	root := filepath.Join(config.DataDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace-v1.yml"), []byte(csInstallWorkspace), 0o600))
	bundles := map[string]string{
		"2026.3": "content:\n  image: enterprise/ecos-content:3.1.0\ndependencies:\n  seaweedfs:\n    image: chrislusf/seaweedfs:4.48\n",
		"2026.2": "content:\n  image: enterprise/ecos-content:3.1.0\n",
		"2026.1": "EcosModelApp:\n  image: core/ecos-model:1.0\n",
	}
	for key, body := range bundles {
		require.NoError(t, os.WriteFile(filepath.Join(root, "community", key+".yaml"), []byte(body), 0o600))
	}
}

func TestInstallContentStorageAvailability(t *testing.T) {
	writeInstallContentStorageWorkspace(t)
	ref := func(key string) bundle.Ref { return bundle.Ref{Repo: "community", Key: key} }

	avail := installContentStorageAvailability(ref("2026.3"), "2.16.0")
	assert.True(t, avail.Available)
	assert.Equal(t, "seaweedfs", avail.ProviderApp, "the provider comes from the workspace, not from Go")

	assert.Equal(t, namespace.ContentStorageNoProviderImage, installContentStorageAvailability(ref("2026.2"), "2.16.0").Reason)
	assert.Equal(t, namespace.ContentStorageNoContentApp, installContentStorageAvailability(ref("2026.1"), "2.16.0").Reason)
	assert.False(t, installContentStorageAvailability(ref("2099.1"), "2.16.0").Available,
		"a release that cannot be read is not offered S3: the choice cannot be undone")
	assert.False(t, installContentStorageAvailability(bundle.Ref{}, "2.16.0").Available)
}

// The wizard writes namespace.yml itself, past the daemon's write gate, so
// "only where S3 is available" must hold here: an unavailable release is not
// even asked, and only an explicit S3 answer writes the block.
func TestChooseInstallContentStorage(t *testing.T) {
	asked := 0
	answer := func(a namespace.ContentStorageType) func(namespace.ContentStorageAvailability) namespace.ContentStorageType {
		return func(avail namespace.ContentStorageAvailability) namespace.ContentStorageType {
			asked++
			assert.Equal(t, "seaweedfs", avail.ProviderApp)
			assert.Equal(t, "S3 (SeaweedFS)", avail.OptionLabel(), "labeled the way the web dialog labels it")
			return a
		}
	}
	available := namespace.ContentStorageAvailability{Available: true, ProviderApp: "seaweedfs", ProviderTitle: "SeaweedFS"}

	assert.Nil(t, chooseInstallContentStorage(
		namespace.ContentStorageAvailability{Reason: namespace.ContentStorageNoContentApp}, answer(namespace.ContentStorageS3)))
	assert.Zero(t, asked, "no question where S3 is not available")

	assert.Nil(t, chooseInstallContentStorage(available, answer(namespace.ContentStorageDB)),
		"DB is written as no block at all, like every other namespace")
	got := chooseInstallContentStorage(available, answer(namespace.ContentStorageS3))
	require.NotNil(t, got)
	assert.Equal(t, namespace.ContentStorageS3, got.Type)
	assert.Equal(t, 2, asked)
}

// What the wizard writes is what the daemon reads: an S3 choice survives the
// namespace.yml round trip, and a DB one leaves the file without the block.
func TestInstallContentStorageIsWrittenToNamespaceYml(t *testing.T) {
	cfg := namespace.DefaultNamespaceConfig()
	cfg.APIVersion = namespace.CurrentAPIVersion()
	cfg.BundleRef = bundle.Ref{Repo: "community", Key: "2026.3"}

	data, err := namespace.MarshalNamespaceConfig(&cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "contentStorage")

	cfg.ContentStorage = chooseInstallContentStorage(
		namespace.ContentStorageAvailability{Available: true, ProviderApp: "seaweedfs"},
		func(namespace.ContentStorageAvailability) namespace.ContentStorageType { return namespace.ContentStorageS3 })
	data, err = namespace.MarshalNamespaceConfig(&cfg)
	require.NoError(t, err)
	parsed, err := namespace.ParseNamespaceConfig(data)
	require.NoError(t, err)
	assert.Equal(t, namespace.ContentStorageS3, parsed.ContentStorageType())
}
