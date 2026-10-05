package namespace

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
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
// generated (the daemon's seeding): a constant's default, and for a generated
// entry a value of its declared length. The stand-in is spelled from the id,
// so no two secrets share a value and a reference to the wrong one shows.
func referenceNamespaceSecrets(ws *bundle.WorkspaceConfig) GenerateOpts {
	secrets := make(map[string]string, len(ws.Secrets))
	for _, s := range ws.Secrets {
		if s.Generated {
			secrets[s.ID] = generatedStandIn(s.ID, s.GeneratedLength())
			continue
		}
		secrets[s.ID] = s.Value
	}
	return GenerateOpts{NamespaceSecrets: secrets}
}

func generatedStandIn(id string, length int) string {
	alnum := strings.Map(func(r rune) rune {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return r
		}
		return -1
	}, id)
	return strings.Repeat(alnum, length/len(alnum)+1)[:length]
}

// referencedSecretIDs answers the ids every ${secret:<id>} in values names.
func referencedSecretIDs(values ...string) []string {
	var ids []string
	for _, v := range values {
		for _, m := range secretRefPattern.FindAllStringSubmatch(v, -1) {
			if !slices.Contains(ids, m[1]) {
				ids = append(ids, m[1])
			}
		}
	}
	slices.Sort(ids)
	return ids
}

// providerEntryValues answers every string of the provider's additionalApps
// entry a secret could be referenced from: its command and its environment.
func providerEntryValues(t *testing.T, ws *bundle.WorkspaceConfig) []string {
	t.Helper()
	entry, ok := ws.AdditionalApp(ws.ContentStorageS3().App)
	require.True(t, ok)
	values := slices.Clone(entry.Cmd)
	for _, v := range entry.Environments {
		values = append(values, v)
	}
	return values
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
			"the workspace must declare %q, or a new namespace has no value for it", m[1])

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

// The S3 port and the admin UI are published on a desktop — on loopback, since
// the specs name no address — so S3 tools, the HTTP probes and a browser reach
// the store from the host; server mode publishes nothing but the proxy.
func TestContentStorageProvider_PortsFollowTheMode(t *testing.T) {
	t.Run("desktop", func(t *testing.T) {
		t.Setenv("CITECK_DESKTOP", "true")
		defer config.ResetDesktopMode()

		_, provider, _ := generateReferenceProvider(t)
		published := make([]string, 0, len(provider.Ports))
		for _, p := range provider.Ports {
			spec, err := appdef.ParsePortSpec(p)
			require.NoError(t, err)
			assert.False(t, spec.HostIP.IsValid() || spec.AllInterfaces,
				"no address: %s is published on 127.0.0.1 only — the store's gRPC ports need no credentials", p)
			published = append(published, spec.ContainerPort)
		}
		assert.ElementsMatch(t, []string{"8333", "23646"}, published,
			"S3 and the admin UI, and nothing else: the filer, volume, master and gRPC ports stay unpublished")
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

// The store's keys are GENERATED, one value per namespace. A constant default
// lives in a workspace repository every stand reads, so it would be every
// stand's key; and a launcher that predates generated secrets skips a
// `generated:` entry, so there the store is left out of a namespace that has
// no values yet instead of running with an empty key (authentication off).
func TestContentStorageProvider_KeysAreGeneratedPerNamespace(t *testing.T) {
	ws := contentStorageWorkspace(t)
	ids := referencedSecretIDs(append(providerEntryValues(t, ws), ws.ContentStorageS3().SecretKey)...)
	require.NotEmpty(t, ids)
	for _, id := range ids {
		idx := slices.IndexFunc(ws.Secrets, func(s bundle.SecretDefault) bool { return s.ID == id })
		require.GreaterOrEqual(t, idx, 0, "%q is referenced but not declared", id)
		assert.True(t, ws.Secrets[idx].Generated,
			"%q must be generated, never a constant: a value written in the workspace is every stand's key", id)
		assert.Empty(t, ws.Secrets[idx].Value)
	}

	// Each one is load-bearing: a namespace that lacks any of them gets no
	// store at all, and ecos-content is not wired to one.
	for _, id := range ids {
		t.Run("without "+id, func(t *testing.T) {
			opts := referenceNamespaceSecrets(ws)
			delete(opts.NamespaceSecrets, id)
			resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, opts)
			assert.Nil(t, findGeneratedApp(resp, ws.ContentStorageS3().App), "never run without its key")
			require.NotNil(t, resp.ContentStorageProblem)
			assert.Equal(t, ContentStorageProviderNotGenerated, resp.ContentStorageProblem.Reason)
		})
	}
}

// Every HTTP port the store serves files on asks for a credential: S3 the
// namespace's key pair, the filer's HTTP API is closed, the volume server's
// wants a token signed with a key only the store holds, and the admin UI the
// stand's admin login. The gRPC ports cannot be closed from configuration —
// that limit is stated in the entry and in docs/content-storage.md.
func TestContentStorageProvider_EveryHTTPPortAsksForACredential(t *testing.T) {
	ws := contentStorageWorkspace(t)
	entry, ok := ws.AdditionalApp(ws.ContentStorageS3().App)
	require.True(t, ok)
	opts := referenceNamespaceSecrets(ws)
	config.ResetDesktopMode()
	resp, err := Generate(s3Cfg(), enterpriseBundle(), ws,
		SystemSecrets{JWT: "j", OIDC: "o", CiteckSA: "sa", AdminPassword: "the-stands-admin-password"}, opts)
	require.NoError(t, err)
	provider := findGeneratedApp(resp, ws.ContentStorageS3().App)
	require.NotNil(t, provider)
	env := func(t *testing.T, key string) string {
		t.Helper()
		v, set := provider.Environments.Get(key)
		require.True(t, set, "%s is not set", key)
		return v
	}

	t.Run("the filer's HTTP API is closed, and the master's /dir/assign that hands out volume tokens", func(t *testing.T) {
		// -disableHttp closes both: with the master's HTTP API open, any HTTP
		// client on the namespace network could ask it for the very token the
		// volume server's JWT check wants, and the JWT keys would protect
		// nothing.
		assert.Contains(t, provider.Cmd, "-disableHttp")
	})

	t.Run("file tokens are signed with a generated key of their own", func(t *testing.T) {
		s3Key := env(t, "AWS_SECRET_ACCESS_KEY")
		for _, key := range []string{
			"WEED_JWT_SIGNING_KEY", "WEED_JWT_SIGNING_READ_KEY",
			"WEED_JWT_FILER_SIGNING_KEY", "WEED_JWT_FILER_SIGNING_READ_KEY",
		} {
			ids := referencedSecretIDs(entry.Environments[key])
			require.Len(t, ids, 1, "%s must be the namespace's own secret, never a literal", key)
			v := env(t, key)
			assert.NotEmpty(t, v, "an empty signing key switches the check off")
			assert.Equal(t, opts.NamespaceSecrets[ids[0]], v)
			assert.NotEqual(t, s3Key, v,
				"%s must not be the S3 key: that one is also written into ECOS, the signing key never leaves the store", key)
		}
	})

	t.Run("the admin UI asks for the stand's admin login", func(t *testing.T) {
		assert.Contains(t, provider.Cmd, "-admin.ui=true")
		assert.Equal(t, "admin", env(t, "WEED_ADMIN_USER"))
		assert.Equal(t, "the-stands-admin-password", env(t, "WEED_ADMIN_PASSWORD"))
	})
}

// No credential may reach the command line: `weed mini` saves its flags into
// /data/mini.options — into the volume, which every snapshot carries — while
// it never saves its environment.
func TestContentStorageProvider_NoCredentialOnTheCommandLine(t *testing.T) {
	ws := contentStorageWorkspace(t)
	entry, ok := ws.AdditionalApp(ws.ContentStorageS3().App)
	require.True(t, ok)
	for _, arg := range entry.Cmd {
		assert.NotContains(t, arg, "${", "a value resolved on the command line is saved into the volume: %q", arg)
		for _, flag := range []string{"-admin.password", "-admin.user", "-s3.config"} {
			assert.False(t, strings.HasPrefix(arg, flag), "%q belongs in the environment", arg)
		}
	}
}

// The sidebar link is the admin UI the entry publishes — on loopback (a port
// with no address is published on 127.0.0.1 only) and on the admin port — and
// it follows the store: hidden where the bundle ships none, greyed out where
// the store is detached (a DB namespace), live where it runs.
func TestContentStorageProvider_TheLinkIsTheAdminUI(t *testing.T) {
	t.Setenv("CITECK_DESKTOP", "true")
	defer config.ResetDesktopMode()

	ws := contentStorageWorkspace(t)
	app := ws.ContentStorageS3().App
	idx := slices.IndexFunc(ws.Links, func(l bundle.WorkspaceLink) bool { return slices.Contains(l.DependsOn, app) })
	require.GreaterOrEqual(t, idx, 0, "the store's link must depend on it")
	link := ws.Links[idx]
	assert.Equal(t, []string{app}, link.DependsOn, "any other dependency would hide or grey out a working link")

	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", u.Hostname(), "the admin port is published on loopback only")

	_, provider, _ := generateReferenceProvider(t)
	hostPorts := make([]string, 0, len(provider.Ports))
	for _, p := range provider.Ports {
		spec, parseErr := appdef.ParsePortSpec(p)
		require.NoError(t, parseErr)
		hostPorts = append(hostPorts, spec.HostPort)
	}
	assert.Contains(t, hostPorts, u.Port(), "the link must point at a port the entry publishes")
	s3URL, err := url.Parse(ws.ContentStorageS3().URL)
	require.NoError(t, err)
	assert.NotEqual(t, s3URL.Port(), u.Port(), "the S3 port answers no browser")

	linkIn := func(t *testing.T, resp *GenResp, status AppRuntimeStatus) (api.LinkDto, bool) {
		t.Helper()
		r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
		r.SetGeneratedDefs(resp.Applications)
		r.SetCustomLinks(resp.CustomLinks)
		if status != "" {
			r.mu.Lock()
			r.apps[app] = &AppRuntime{Name: app, Status: status}
			r.mu.Unlock()
		}
		for _, l := range r.ToNamespaceDto().Links {
			if l.Name == link.Name {
				return l, true
			}
		}
		return api.LinkDto{}, false
	}

	t.Run("hidden where the bundle ships no store", func(t *testing.T) {
		bun := enterpriseBundle()
		delete(bun.Dependencies, app)
		_, shown := linkIn(t, generateContentStorage(t, basicCfg(), bun, ws, referenceNamespaceSecrets(ws)), "")
		assert.False(t, shown)
	})
	t.Run("greyed out where the store is detached", func(t *testing.T) {
		opts := referenceNamespaceSecrets(ws)
		opts.DetachedApps = map[string]bool{app: true}
		l, shown := linkIn(t, generateContentStorage(t, basicCfg(), enterpriseBundle(), ws, opts), AppStatusStopped)
		require.True(t, shown)
		assert.True(t, l.Disabled)
	})
	t.Run("live where the store runs", func(t *testing.T) {
		l, shown := linkIn(t, generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, referenceNamespaceSecrets(ws)), AppStatusRunning)
		require.True(t, shown)
		assert.False(t, l.Disabled)
	})
}
