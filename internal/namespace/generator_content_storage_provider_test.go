package namespace

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
)

// The S3 content storage provider is CONFIGURATION: the launcher has no code
// for it, and no provider name is spelled in production Go. These tests
// generate it from testdata/content-storage-workspace.yml — the reference copy
// of the blocks the public workspace config carries — and pin what that
// declaration promises. Two halves describe one store: the additionalApps entry
// configures the CONTAINER, and `contentStorage.s3` is what the daemon writes
// into ECOS when it connects ecos-content. Nothing ties them together but the
// file, so a copy edited in one place and not the other would connect
// ecos-content to a bucket or a key the container does not serve — found only
// at the first upload, on a stand that cannot go back to DB.

// referenceNamespaceSecrets is what a NEW namespace stores the first time it is
// generated: every default the workspace declares (the daemon's seeding).
func referenceNamespaceSecrets(ws *bundle.WorkspaceConfig) GenerateOpts {
	secrets := make(map[string]string, len(ws.Secrets))
	for _, s := range ws.Secrets {
		secrets[s.ID] = s.Value
	}
	return GenerateOpts{NamespaceSecrets: secrets}
}

// generateReferenceProvider generates an S3 namespace on the reference
// declaration and answers the block, the provider's def and the response.
func generateReferenceProvider(t *testing.T) (*bundle.ContentStorageS3Config, *appdef.ApplicationDef, *GenResp) {
	t.Helper()
	ws := contentStorageWorkspace(t)
	s3 := ws.ContentStorageS3()
	resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, referenceNamespaceSecrets(ws))
	provider := findGeneratedApp(resp, s3.App)
	require.NotNil(t, provider, "the reference declaration must produce its provider")
	return s3, provider, resp
}

// The endpoint, the bucket and the credentials the daemon hands ecos-content
// are the ones the provider's container is configured with.
func TestContentStorageProvider_TheBlockDescribesTheEntry(t *testing.T) {
	s3, provider, resp := generateReferenceProvider(t)

	t.Run("url: the entry's name and S3 port on the namespace network", func(t *testing.T) {
		u, err := url.Parse(s3.URL)
		require.NoError(t, err)
		assert.Equal(t, "http", u.Scheme, "the provider serves plain HTTP inside the namespace network")
		assert.True(t, u.Hostname() == s3.App || slices.Contains(provider.NetworkAliases, u.Hostname()),
			"ecos-content resolves the endpoint by the container's name or one of its aliases, got %q", u.Hostname())
		require.NotEmpty(t, provider.StartupConditions)
		require.NotNil(t, provider.StartupConditions[0].Probe)
		require.NotNil(t, provider.StartupConditions[0].Probe.HTTP)
		assert.Equal(t, strconv.Itoa(provider.StartupConditions[0].Probe.HTTP.Port), u.Port(),
			"the S3 port is the one the entry's readiness probe asks")
	})

	t.Run("bucket: the one the container creates at start", func(t *testing.T) {
		bucket, ok := provider.Environments.Get("S3_BUCKET")
		require.True(t, ok)
		assert.Equal(t, bucket, s3.Bucket)
	})

	t.Run("accessKey: the container's identity", func(t *testing.T) {
		key, ok := provider.Environments.Get("AWS_ACCESS_KEY_ID")
		require.True(t, ok)
		assert.Equal(t, key, s3.AccessKey)
	})

	// The secret key is the one value that differs per namespace, so it must be
	// a REFERENCE to the namespace's own secret — resolved for the record the
	// same way it is resolved for the container — and the workspace must declare
	// a default for it, or a new namespace has no key at all and its provider is
	// not generated.
	t.Run("secretKey: the namespace's secret, resolved as the container's is", func(t *testing.T) {
		m := secretRefPattern.FindStringSubmatch(s3.SecretKey)
		require.Len(t, m, 2, "secretKey must be a ${secret:<id>} reference, never a literal: %q", s3.SecretKey)
		ws := contentStorageWorkspace(t)
		assert.True(t, slices.ContainsFunc(ws.Secrets, func(s bundle.SecretDefault) bool { return s.ID == m[1] }),
			"the workspace must declare a default for %q", m[1])

		// ResolveSecretRefs is what the daemon resolves the key with before it
		// writes the ECOS secret record.
		resolved, missing := ResolveSecretRefs(s3.SecretKey, referenceNamespaceSecrets(ws).NamespaceSecrets)
		assert.Empty(t, missing)
		container, ok := provider.Environments.Get("AWS_SECRET_ACCESS_KEY")
		require.True(t, ok)
		assert.NotContains(t, container, "${secret:", "the container's key is resolved")
		assert.Equal(t, container, resolved)
	})

	assert.Nil(t, resp.ContentStorageProblem, "the reference declaration is usable as written")
}

// What the entry's own comments promise about the container. The data
// directory and the volume are one rule: `weed mini -dir` outside the mounted
// volume writes every file into the container layer, and a recreate — any
// change to the entry recreates it — loses all of ecos-content's files.
func TestContentStorageProvider_IsTheDeclaredContainer(t *testing.T) {
	_, provider, _ := generateReferenceProvider(t)

	assert.Equal(t, "chrislusf/seaweedfs:4.48", provider.Image, "the image is the bundle's: the entry names none")
	assert.Equal(t, appdef.KindThirdParty, provider.Kind)
	assert.Contains(t, provider.Volumes, "seaweedfs2:/data", "the volume name IS how the data is found")

	var dataDir string
	for _, arg := range provider.Cmd {
		if v, ok := strings.CutPrefix(arg, "-dir="); ok {
			dataDir = v
		}
	}
	require.NotEmpty(t, dataDir, "the store's data directory must be stated, not left to the image")
	var mountedBy string
	for _, v := range provider.Volumes {
		if parts := strings.SplitN(v, ":", 3); len(parts) >= 2 && parts[1] == dataDir {
			mountedBy = parts[0]
		}
	}
	assert.Equal(t, "seaweedfs2", mountedBy, "the data directory must be the persisted named volume")

	require.NotNil(t, provider.Resources)
	assert.Equal(t, "1g", provider.Resources.Limits.Memory)
	assert.Equal(t, 15, provider.StopTimeout)
	assert.False(t, provider.IsJVM, "a Go binary: no JVM budget, no heap-dump flags")
	_, hasJavaOpts := provider.Environments.Get("JAVA_OPTS")
	assert.False(t, hasJavaOpts)
}

// The S3 port is published on a desktop — on loopback, since the spec names no
// address — so S3 tools and the HTTP probes reach the store from the host;
// server mode publishes nothing but the proxy.
func TestContentStorageProvider_PortsFollowTheMode(t *testing.T) {
	t.Run("desktop", func(t *testing.T) {
		t.Setenv("CITECK_DESKTOP", "true")
		defer config.ResetDesktopMode()

		_, provider, _ := generateReferenceProvider(t)
		require.Len(t, provider.Ports, 1)
		spec, err := appdef.ParsePortSpec(provider.Ports[0])
		require.NoError(t, err)
		assert.False(t, spec.HostIP.IsValid() || spec.AllInterfaces,
			"no address: published on 127.0.0.1 only — the store's other ports need no credentials")
		assert.Equal(t, "8333", spec.ContainerPort)
	})

	t.Run("server mode publishes only the proxy", func(t *testing.T) {
		_, provider, _ := generateReferenceProvider(t)
		assert.Empty(t, provider.Ports)
	})
}

// A config entry cannot name the launcher's liveness constant, so the
// reference copy states its value and this keeps the two from drifting (the
// observer's declared probes are held to the same rule).
func TestContentStorageProvider_DeclaredLivenessUsesTheLaunchersTolerance(t *testing.T) {
	_, provider, _ := generateReferenceProvider(t)
	require.NotNil(t, provider.LivenessProbe)
	assert.Equal(t, livenessFailureThreshold, provider.LivenessProbe.FailureThreshold)
}

// Choosing S3 changes ecos-content's dependencies and nothing about the
// provider: a DB → S3 switch must not recreate the store a DB namespace may
// already run by hand (and connected to ECOS manually), or its identity and
// files would be rebuilt under the records that point at it.
func TestContentStorageProvider_IsTheSameContainerWhateverTheNamespaceChose(t *testing.T) {
	ws := contentStorageWorkspace(t)
	name := ws.ContentStorageS3().App
	db := findGeneratedApp(generateContentStorage(t, basicCfg(), enterpriseBundle(), ws, referenceNamespaceSecrets(ws)), name)
	s3 := findGeneratedApp(generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, referenceNamespaceSecrets(ws)), name)
	require.NotNil(t, db, "a DB namespace generates the provider too — detached by its template, not absent")
	require.NotNil(t, s3)
	assert.Equal(t, db.GetHashInput(), s3.GetHashInput())
}

// The default template holds the provider back, which is right for a DB
// namespace and wrong for an S3 one: that is why the daemon subtracts
// RequiredApps from the template's detach set. The two answers are pinned
// together, so a renamed provider cannot leave a template line behind that
// keeps an S3 namespace's store down.
func TestContentStorageProvider_TheDefaultTemplateHoldsItBackAndS3RequiresIt(t *testing.T) {
	ws := contentStorageWorkspace(t)
	name := ws.ContentStorageS3().App

	idx := slices.IndexFunc(ws.NamespaceTemplates, func(tmpl bundle.NamespaceTemplate) bool { return tmpl.ID == "default" })
	require.GreaterOrEqual(t, idx, 0)
	assert.Contains(t, ws.NamespaceTemplates[idx].DetachedApps, name,
		"a DB namespace must not pay for a store nothing uses")
	assert.Equal(t, []string{name}, RequiredApps(s3Cfg(), ws))
	assert.Empty(t, RequiredApps(basicCfg(), ws))
}
