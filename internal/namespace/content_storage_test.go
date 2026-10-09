package namespace

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
)

const seaweedfsApp = "seaweedfs"

// contentStorageWorkspace is the reference declaration (testdata), read the
// way a workspace config is: the provider entry, its secrets, its sidebar link
// and the contentStorage block that names it.
func contentStorageWorkspace(t *testing.T) *bundle.WorkspaceConfig {
	t.Helper()
	data, err := os.ReadFile("testdata/content-storage-workspace.yml")
	require.NoError(t, err)
	var ws bundle.WorkspaceConfig
	require.NoError(t, yaml.Unmarshal(data, &ws))
	require.NoError(t, bundle.ValidateAdditionalApps(ws.AdditionalApps))
	require.NotNil(t, ws.ContentStorageS3(), "the reference block must be usable as written")
	return &ws
}

// enterpriseBundle names ecos-content and, in its dependencies section, the
// provider's image — the shape of the release that ships S3 content storage.
func enterpriseBundle() *bundle.Def {
	return &bundle.Def{
		Applications: map[string]bundle.AppDef{
			appdef.AppContent: {Image: "enterprise/ecos-content:3.1.0"},
			appdef.AppEmodel:  {Image: "core/ecos-model:2.40.0"},
		},
		Dependencies: map[string]bundle.AppDef{
			seaweedfsApp: {Image: "chrislusf/seaweedfs:4.48"},
		},
	}
}

func s3Cfg() *Config {
	cfg := basicCfg()
	cfg.ContentStorage = &ContentStorageProps{Type: ContentStorageS3}
	return cfg
}

// contentStorageSecrets is what a namespace has stored for the reference
// declaration's secrets. Both are GENERATED, so the workspace carries no value
// for either: the generator only ever sees the namespace's own copies, and a
// namespace missing either one gets no provider at all.
func contentStorageSecrets() GenerateOpts {
	return GenerateOpts{NamespaceSecrets: map[string]string{
		"seaweedfs-s3-key": "S3keyS3keyS3keyS3keyS3keyS3keyS3keyS3key",
		"seaweedfs-jwt":    "JwtKeyJwtKeyJwtKeyJwtKeyJwtKeyJwtKeyJwtKeyJwtKey",
	}}
}

func generateContentStorage(t *testing.T, cfg *Config, bun *bundle.Def, ws *bundle.WorkspaceConfig, opts GenerateOpts) *GenResp {
	t.Helper()
	config.ResetDesktopMode()
	resp, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "j", OIDC: "o", CiteckSA: "sa"}, opts)
	require.NoError(t, err, "a content-storage problem must never fail the generation")
	return resp
}

// --- namespace.yml model ---

func TestContentStorageType_DefaultsToDBAndNormalizesTheSpelling(t *testing.T) {
	cfg, err := ParseNamespaceConfig([]byte("id: ns\n"))
	require.NoError(t, err)
	assert.Nil(t, cfg.ContentStorage, "never seeded: absent stays absent")
	assert.Equal(t, ContentStorageDB, cfg.ContentStorageType())

	for _, spelling := range []string{"s3", " S3 ", "S3"} {
		cfg, err = ParseNamespaceConfig([]byte("id: ns\ncontentStorage:\n  type: '" + spelling + "'\n"))
		require.NoError(t, err, spelling)
		assert.Equal(t, ContentStorageS3, cfg.ContentStorageType(), spelling)
		assert.Equal(t, ContentStorageS3, cfg.ContentStorage.Type, "normalized in place, once")
	}

	cfg, err = ParseNamespaceConfig([]byte("id: ns\ncontentStorage: {}\n"))
	require.NoError(t, err)
	assert.Equal(t, ContentStorageDB, cfg.ContentStorageType(), "a block with no type is DB")
}

func TestContentStorageType_UnknownTypeIsRefused(t *testing.T) {
	_, err := ParseNamespaceConfig([]byte("id: ns\ncontentStorage:\n  type: MINIO\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MINIO")
}

// Two answers to "where does ecos-content store its files" is a stand whose
// content lands in whichever store happened to win.
func TestContentStorageType_S3BesideTheExternalS3BlockIsRefused(t *testing.T) {
	_, err := ParseNamespaceConfig([]byte(`id: ns
contentStorage:
  type: S3
s3:
  endpoint: http://minio:9000
  bucket: b
  accessKey: a
  secretKey: s
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3 block")

	// The external block with DB content storage is today's configuration.
	_, err = ParseNamespaceConfig([]byte(`id: ns
contentStorage:
  type: DB
s3:
  endpoint: http://minio:9000
  bucket: b
  accessKey: a
  secretKey: s
`))
	require.NoError(t, err)
}

// Every namespace.yml in the field lacks the key; saving one must not add it.
func TestContentStorage_AnExistingConfigRoundTripsWithoutTheKey(t *testing.T) {
	cfg, err := ParseNamespaceConfig([]byte("id: ns\nname: N\n"))
	require.NoError(t, err)
	out, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "contentStorage")

	cfg.ContentStorage = &ContentStorageProps{Type: ContentStorageS3}
	out, err = MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(out), "contentStorage:\n  type: S3\n")
}

// --- availability ---

func TestContentStorageS3Availability(t *testing.T) {
	ws := contentStorageWorkspace(t)

	t.Run("available", func(t *testing.T) {
		got := ContentStorageS3Availability(enterpriseBundle(), ws)
		assert.Equal(t, ContentStorageAvailability{Available: true, ProviderApp: seaweedfsApp, ProviderTitle: "SeaweedFS"}, got)

		untitled := *ws
		untitled.ContentStorage = &bundle.ContentStorageConfig{S3: new(*ws.ContentStorageS3())}
		untitled.ContentStorage.S3.Title = ""
		assert.Equal(t, seaweedfsApp, ContentStorageS3Availability(enterpriseBundle(), &untitled).ProviderTitle,
			"no title: the app id is the label")
	})

	// A bundle without ecos-content HIDES the option, so its answer must win
	// over every configuration problem — even a workspace with no block at all.
	t.Run("no content app wins", func(t *testing.T) {
		bun := enterpriseBundle()
		delete(bun.Applications, appdef.AppContent)
		assert.Equal(t, ContentStorageNoContentApp, ContentStorageS3Availability(bun, ws).Reason)
		assert.Equal(t, ContentStorageNoContentApp, ContentStorageS3Availability(bun, nil).Reason)
		assert.Equal(t, ContentStorageNoContentApp, ContentStorageS3Availability(nil, ws).Reason)
	})

	t.Run("no provider config", func(t *testing.T) {
		got := ContentStorageS3Availability(enterpriseBundle(), &bundle.WorkspaceConfig{AdditionalApps: ws.AdditionalApps})
		assert.Equal(t, ContentStorageNoProviderConfig, got.Reason)
		assert.Empty(t, got.ProviderApp)
		assert.False(t, got.Available)
	})

	t.Run("no provider app", func(t *testing.T) {
		noEntry := *ws
		noEntry.AdditionalApps = nil
		got := ContentStorageS3Availability(enterpriseBundle(), &noEntry)
		assert.Equal(t, ContentStorageNoProviderApp, got.Reason)
		assert.Equal(t, seaweedfsApp, got.ProviderApp, "named even when unusable — the hint quotes it")
	})

	t.Run("a disabled entry is no provider", func(t *testing.T) {
		off := false
		disabled := *ws
		disabled.AdditionalApps = slices.Clone(ws.AdditionalApps)
		disabled.AdditionalApps[0].Enabled = &off
		assert.Equal(t, ContentStorageNoProviderApp, ContentStorageS3Availability(enterpriseBundle(), &disabled).Reason)
	})

	t.Run("no provider image", func(t *testing.T) {
		bun := enterpriseBundle()
		bun.Dependencies = nil
		assert.Equal(t, ContentStorageNoProviderImage, ContentStorageS3Availability(bun, ws).Reason)
	})

	// A name the generator keeps for a platform app — a built-in, or a webapp
	// the workspace lists and the bundle carries — would get the platform app,
	// never the provider: the collision guard skips the entry.
	t.Run("a provider named like a platform app", func(t *testing.T) {
		renamed := func(name string, webapps ...string) *bundle.WorkspaceConfig {
			out := *ws
			out.AdditionalApps = slices.Clone(ws.AdditionalApps)
			out.AdditionalApps[0].Name = name
			out.ContentStorage = &bundle.ContentStorageConfig{S3: new(*ws.ContentStorageS3())}
			out.ContentStorage.S3.App = name
			for _, w := range webapps {
				out.Webapps = append(out.Webapps, bundle.WebappConfig{ID: w})
			}
			return &out
		}
		bun := enterpriseBundle()
		bun.Applications["edi"] = bundle.AppDef{Image: "enterprise/ecos-edi:1.0"}
		bun.Dependencies[appdef.AppGateway] = bundle.AppDef{Image: "chrislusf/seaweedfs:4.48"}

		assert.Equal(t, ContentStorageProviderConflict, ContentStorageS3Availability(bun, renamed(appdef.AppGateway)).Reason)
		assert.Equal(t, ContentStorageProviderConflict, ContentStorageS3Availability(bun, renamed(appdef.AppContent)).Reason)
		assert.Equal(t, ContentStorageProviderConflict, ContentStorageS3Availability(bun, renamed("edi", "edi", "content")).Reason,
			"a webapp the workspace lists stays a webapp")
		assert.True(t, ContentStorageS3Availability(bun, renamed("edi")).Available,
			"with no webapps list the entry claims the bundle application of its name")
	})

	// A provider that depends on ecos-content — directly or through another
	// entry — would close a dependsOn cycle once content depends on it.
	t.Run("a provider that depends on content", func(t *testing.T) {
		withDeps := func(entryDeps []string, extra ...bundle.AdditionalAppProps) *bundle.WorkspaceConfig {
			out := *ws
			out.AdditionalApps = append(slices.Clone(ws.AdditionalApps), extra...)
			out.AdditionalApps[0].DependsOn = entryDeps
			return &out
		}
		assert.Equal(t, ContentStorageProviderConflict,
			ContentStorageS3Availability(enterpriseBundle(), withDeps([]string{appdef.AppContent})).Reason)
		helper := bundle.AdditionalAppProps{Name: "helper", Image: "busybox", DependsOn: []string{appdef.AppContent}}
		assert.Equal(t, ContentStorageProviderConflict,
			ContentStorageS3Availability(enterpriseBundle(), withDeps([]string{"helper"}, helper)).Reason)
		assert.True(t, ContentStorageS3Availability(enterpriseBundle(), withDeps([]string{appdef.AppGateway})).Available)
	})

	// The image rule is the generator's: the entry's own image is a fallback.
	t.Run("the entry's own image is enough", func(t *testing.T) {
		bun := enterpriseBundle()
		bun.Dependencies = nil
		own := *ws
		own.AdditionalApps = slices.Clone(ws.AdditionalApps)
		own.AdditionalApps[0].Image = "chrislusf/seaweedfs:4.40"
		assert.True(t, ContentStorageS3Availability(bun, &own).Available)
	})
}

func TestRequiredApps(t *testing.T) {
	ws := contentStorageWorkspace(t)
	assert.Equal(t, []string{seaweedfsApp}, RequiredApps(s3Cfg(), ws))
	assert.Empty(t, RequiredApps(basicCfg(), ws), "a DB namespace requires nothing")
	assert.Empty(t, RequiredApps(s3Cfg(), nil), "no workspace, no provider to name")
	assert.Empty(t, RequiredApps(s3Cfg(), &bundle.WorkspaceConfig{}))
}

// --- generation ---

func TestWireContentStorage_ContentWaitsForTheProvider(t *testing.T) {
	resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), contentStorageWorkspace(t), contentStorageSecrets())
	content := findGeneratedApp(resp, appdef.AppContent)
	require.NotNil(t, content)
	require.NotNil(t, findGeneratedApp(resp, seaweedfsApp))
	assert.Contains(t, content.DependsOn, seaweedfsApp)
	assert.Nil(t, resp.ContentStorageProblem)

	for key, want := range map[string]string{
		"ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL":         "http://seaweedfs:8333",
		"ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_CREDENTIALS": "content-storage-s3-credentials",
		"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_TYPE":       "BASIC",
		"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME":   "citeck",
		"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD":   "S3keyS3keyS3keyS3keyS3keyS3keyS3keyS3key",
	} {
		got, ok := content.Environments.Get(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got, key)
	}
	assert.Empty(t, resp.GatingApps[seaweedfsApp], "the provider is not a gating app")
}

// The release-safety property: a DB namespace — every namespace in the field —
// must generate a content def byte-identical to one generated by a workspace
// that has never heard of content storage, or the release recreates every
// ecos-content on upgrade.
func TestWireContentStorage_ADBNamespaceIsByteIdentical(t *testing.T) {
	plainWs := contentStorageWorkspace(t)
	plainWs.ContentStorage = nil
	plain := generateContentStorage(t, basicCfg(), enterpriseBundle(), plainWs, contentStorageSecrets())

	for name, cfg := range map[string]*Config{
		"absent": basicCfg(),
		"explicit DB": func() *Config {
			c := basicCfg()
			c.ContentStorage = &ContentStorageProps{Type: ContentStorageDB}
			return c
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			resp := generateContentStorage(t, cfg, enterpriseBundle(), contentStorageWorkspace(t), contentStorageSecrets())
			got := findGeneratedApp(resp, appdef.AppContent)
			want := findGeneratedApp(plain, appdef.AppContent)
			require.NotNil(t, got)
			require.NotNil(t, want)
			assert.Equal(t, want.GetHashInput(), got.GetHashInput())
			assert.NotContains(t, got.DependsOn, seaweedfsApp)
			assert.Nil(t, resp.ContentStorageProblem, "a DB namespace has no content-storage problem to report")
		})
	}
}

// The provider references the namespace's S3 secret, so a locked vault is what
// removes it. Wired anyway, the prune would take ecos-content — and everything
// that depends on it — along.
func TestWireContentStorage_AProviderWithoutItsSecretLeavesContentAlone(t *testing.T) {
	resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), contentStorageWorkspace(t), GenerateOpts{})
	assert.Nil(t, findGeneratedApp(resp, seaweedfsApp), "never run without its key")
	content := findGeneratedApp(resp, appdef.AppContent)
	require.NotNil(t, content, "ecos-content must survive its provider's exclusion")
	assert.NotContains(t, content.DependsOn, seaweedfsApp)
	require.NotNil(t, resp.ContentStorageProblem)
	assert.Equal(t, ContentStorageProblem{Reason: ContentStorageProviderNotGenerated, ProviderApp: seaweedfsApp},
		*resp.ContentStorageProblem)
}

// The same for a provider the prune is about to remove: wiring content to an
// app whose own dependency is missing hands content to the prune.
func TestWireContentStorage_AProviderThePruneRemovesLeavesContentAlone(t *testing.T) {
	ws := contentStorageWorkspace(t)
	ws.AdditionalApps = slices.Clone(ws.AdditionalApps)
	ws.AdditionalApps[0].DependsOn = []string{"no-such-app"}
	resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, contentStorageSecrets())
	assert.Nil(t, findGeneratedApp(resp, seaweedfsApp))
	content := findGeneratedApp(resp, appdef.AppContent)
	require.NotNil(t, content)
	assert.NotContains(t, content.DependsOn, seaweedfsApp)
	require.NotNil(t, resp.ContentStorageProblem)
	assert.Equal(t, ContentStorageProviderNotGenerated, resp.ContentStorageProblem.Reason)
}

// A bundle that stopped naming the provider is reported with the availability
// reason, and is still not a generation error.
func TestWireContentStorage_ABundleWithoutTheProviderImageIsReported(t *testing.T) {
	bun := enterpriseBundle()
	bun.Dependencies = nil
	resp := generateContentStorage(t, s3Cfg(), bun, contentStorageWorkspace(t), contentStorageSecrets())
	require.NotNil(t, findGeneratedApp(resp, appdef.AppContent))
	require.NotNil(t, resp.ContentStorageProblem)
	assert.Equal(t, ContentStorageNoProviderImage, resp.ContentStorageProblem.Reason)
}

func TestWireContentStorage_NoContentAppIsReported(t *testing.T) {
	bun := enterpriseBundle()
	delete(bun.Applications, appdef.AppContent)
	resp := generateContentStorage(t, s3Cfg(), bun, contentStorageWorkspace(t), contentStorageSecrets())
	require.NotNil(t, resp.ContentStorageProblem)
	assert.Equal(t, ContentStorageNoContentApp, resp.ContentStorageProblem.Reason)
}

// A provider that depends on ecos-content would close a dependsOn cycle once
// content depends on it — and a cycle FAILS the generation, i.e. the
// namespace's load and possibly the daemon's boot, over a workspace entry the
// operator may not be able to edit. Reported, never wired, never an error.
func TestWireContentStorage_AProviderThatDependsOnContentIsReported(t *testing.T) {
	t.Run("directly, in the workspace entry", func(t *testing.T) {
		ws := contentStorageWorkspace(t)
		ws.AdditionalApps = slices.Clone(ws.AdditionalApps)
		ws.AdditionalApps[0].DependsOn = []string{appdef.AppContent}
		resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, contentStorageSecrets())
		content := findGeneratedApp(resp, appdef.AppContent)
		require.NotNil(t, content)
		assert.NotContains(t, content.DependsOn, seaweedfsApp)
		require.NotNil(t, resp.ContentStorageProblem)
		assert.Equal(t, ContentStorageProviderConflict, resp.ContentStorageProblem.Reason)
	})

	// Only the generation sees this one: the provider depends on a webapp,
	// and it is the namespace's own config that makes the webapp wait for
	// ecos-content. The DB namespace generates fine; S3 must as well.
	t.Run("through an app the namespace makes wait for content", func(t *testing.T) {
		ws := contentStorageWorkspace(t)
		ws.AdditionalApps = slices.Clone(ws.AdditionalApps)
		ws.AdditionalApps[0].DependsOn = []string{appdef.AppEmodel}
		cfg := s3Cfg()
		cfg.Webapps = map[string]WebappProps{appdef.AppEmodel: {DependsOn: []string{appdef.AppContent}}}
		require.True(t, ContentStorageS3Availability(enterpriseBundle(), ws).Available,
			"the static answer cannot see the namespace's own dependsOn")

		resp := generateContentStorage(t, cfg, enterpriseBundle(), ws, contentStorageSecrets())
		content := findGeneratedApp(resp, appdef.AppContent)
		require.NotNil(t, content)
		assert.NotContains(t, content.DependsOn, seaweedfsApp)
		require.NotNil(t, resp.ContentStorageProblem)
		assert.Equal(t, ContentStorageProviderConflict, resp.ContentStorageProblem.Reason)
	})
}

// A provider whose name the generator gives to a platform app — here the
// vector store, which a bundle naming a qdrant image generates before any
// additionalApps entry — is skipped by the collision guard. The app that then
// exists under that name is NOT the S3 server, and content must not be wired
// to it.
func TestWireContentStorage_AProviderSkippedByTheCollisionGuardIsReported(t *testing.T) {
	ws := contentStorageWorkspace(t)
	ws.AdditionalApps = slices.Clone(ws.AdditionalApps)
	ws.AdditionalApps[0].Name = appdef.AppQdrant
	ws.ContentStorage = &bundle.ContentStorageConfig{S3: new(*ws.ContentStorageS3())}
	ws.ContentStorage.S3.App = appdef.AppQdrant
	bun := enterpriseBundle()
	bun.Dependencies[appdef.AppQdrant] = bundle.AppDef{Image: "qdrant/qdrant:v1.15.5"}

	resp := generateContentStorage(t, s3Cfg(), bun, ws, contentStorageSecrets())
	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant, "the platform app keeps its name")
	assert.Equal(t, "qdrant/qdrant:v1.15.5", qdrant.Image)
	content := findGeneratedApp(resp, appdef.AppContent)
	require.NotNil(t, content)
	assert.NotContains(t, content.DependsOn, appdef.AppQdrant)
	require.NotNil(t, resp.ContentStorageProblem)
	assert.Equal(t, ContentStorageProviderConflict, resp.ContentStorageProblem.Reason)
}

// The provider is detached by the default template; on an S3 namespace that
// detach (the operator's own, after the fact) is honored, and content then
// waits for it — the dependency is wired whatever the provider's detach state.
func TestWireContentStorage_ADetachedProviderIsStillWaitedFor(t *testing.T) {
	opts := contentStorageSecrets()
	opts.DetachedApps = map[string]bool{seaweedfsApp: true}
	resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), contentStorageWorkspace(t), opts)
	content := findGeneratedApp(resp, appdef.AppContent)
	require.NotNil(t, content)
	assert.Contains(t, content.DependsOn, seaweedfsApp)
	assert.True(t, resp.DependsOnDetachedApps[seaweedfsApp],
		"re-attaching the provider regenerates, like any detached dependency")
}

// One rule for the image: what the availability calls "no image" is exactly
// what the generator leaves out.
func TestAdditionalAppImageIsTheGeneratorsRule(t *testing.T) {
	ws := contentStorageWorkspace(t)
	entry, ok := ws.AdditionalApp(seaweedfsApp)
	require.True(t, ok)
	for _, bun := range []*bundle.Def{enterpriseBundle(), {Applications: enterpriseBundle().Applications}} {
		resp := generateContentStorage(t, basicCfg(), bun, ws, contentStorageSecrets())
		generated := findGeneratedApp(resp, seaweedfsApp)
		image := additionalAppImage(bun, ws, entry)
		if image == "" {
			assert.Nil(t, generated)
			continue
		}
		require.NotNil(t, generated)
		assert.Equal(t, image, generated.Image)
		assert.True(t, strings.HasPrefix(image, "chrislusf/seaweedfs:"))
	}
}

func TestWireContentStorage_KeyRotationRecreatesContentAndProvider(t *testing.T) {
	ws := contentStorageWorkspace(t)
	old := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, contentStorageSecrets())
	opts := contentStorageSecrets()
	opts.NamespaceSecrets["seaweedfs-s3-key"] = "rotated-namespace-key"
	updated := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, opts)
	for _, app := range []string{appdef.AppContent, seaweedfsApp} {
		before := findGeneratedApp(old, app)
		after := findGeneratedApp(updated, app)
		require.NotNil(t, before)
		require.NotNil(t, after)
		assert.NotEqual(t, before.GetHash(), after.GetHash(), "key change must recreate %s", app)
	}
	content := findGeneratedApp(updated, appdef.AppContent)
	provider := findGeneratedApp(updated, seaweedfsApp)
	contentKey, _ := content.Environments.Get("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD")
	providerKey, _ := provider.Environments.Get("AWS_SECRET_ACCESS_KEY")
	assert.Equal(t, "rotated-namespace-key", contentKey)
	assert.Equal(t, contentKey, providerKey)
}

func TestWireContentStorage_MissingConnectionSecretDoesNotWireContent(t *testing.T) {
	ws := contentStorageWorkspace(t)
	ws.ContentStorage.S3.SecretKey = "${secret:undeclared-connection-key}"
	resp := generateContentStorage(t, s3Cfg(), enterpriseBundle(), ws, contentStorageSecrets())
	require.NotNil(t, findGeneratedApp(resp, seaweedfsApp), "provider itself still generates")
	require.NotNil(t, resp.ContentStorageProblem)
	assert.Equal(t, ContentStorageProviderNotGenerated, resp.ContentStorageProblem.Reason)
	content := findGeneratedApp(resp, appdef.AppContent)
	require.NotNil(t, content)
	_, set := content.Environments.Get("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD")
	assert.False(t, set, "an unresolved reference must never become a credential")
	assert.NotContains(t, content.DependsOn, seaweedfsApp)
}

func TestExternalS3NativeArtifactAndBucketRecreate(t *testing.T) {
	cfg := basicCfg()
	cfg.S3 = &S3Config{Endpoint: "https://external.example", Bucket: "custom-bucket", AccessKey: "access", SecretKey: "secret:s3.secretKey"}
	opts := GenerateOpts{SecretReader: &mockSecretReader{secrets: map[string]string{"s3.secretKey": "private-fixture-key"}}}
	first := generateContentStorage(t, cfg, enterpriseBundle(), contentStorageWorkspace(t), opts)
	content := findGeneratedApp(first, appdef.AppContent)
	require.NotNil(t, content)
	assert.NotContains(t, content.DependsOn, seaweedfsApp)
	assert.Contains(t, content.Volumes, "./app/content/s3/content-storage-s3.yml:/app/resources/eapps/artifacts/content/storage/content-storage-s3.yml:ro")
	var artifact struct {
		ID     string            `yaml:"id"`
		Type   string            `yaml:"type"`
		Config map[string]string `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal(first.Files["app/content/s3/content-storage-s3.yml"], &artifact))
	assert.Equal(t, "content-storage-s3", artifact.ID)
	assert.Equal(t, "S3", artifact.Type)
	assert.Equal(t, "custom-bucket", artifact.Config["bucketName"])
	assert.NotContains(t, artifact.Config, "region", "omitted region retains SDK auto-detection")
	assert.Equal(t, "emodel/endpoint@content-storage-s3-endpoint", artifact.Config["endpoint"])
	assert.NotContains(t, string(first.Files["app/content/s3/content-storage-s3.yml"]), "private-fixture-key")
	cfg.S3.Bucket = "another-bucket"
	second := generateContentStorage(t, cfg, enterpriseBundle(), contentStorageWorkspace(t), opts)
	assert.NotEqual(t, content.VolumesContentHash, findGeneratedApp(second, appdef.AppContent).VolumesContentHash, "bucket changes recreate content to deploy the native artifact")
}

func TestExternalS3RegionInNativeArtifact(t *testing.T) {
	cfg := basicCfg()
	cfg.S3 = &S3Config{Endpoint: "https://external.example", Bucket: "custom-bucket", AccessKey: "access", SecretKey: "secret:s3.secretKey", Region: " eu-central-1 "}
	opts := GenerateOpts{SecretReader: &mockSecretReader{secrets: map[string]string{"s3.secretKey": "private-fixture-key"}}}
	first := generateContentStorage(t, cfg, enterpriseBundle(), contentStorageWorkspace(t), opts)
	var artifact struct {
		Config map[string]string `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal(first.Files["app/content/s3/content-storage-s3.yml"], &artifact))
	assert.Equal(t, "eu-central-1", artifact.Config["region"])
	cfg.S3.Region = " 	 "
	second := generateContentStorage(t, cfg, enterpriseBundle(), contentStorageWorkspace(t), opts)
	// Decode into a fresh map: YAML merge into an existing map retains absent keys.
	artifact.Config = nil
	require.NoError(t, yaml.Unmarshal(second.Files["app/content/s3/content-storage-s3.yml"], &artifact))
	assert.NotContains(t, artifact.Config, "region")
	assert.NotEqual(t, findGeneratedApp(first, appdef.AppContent).VolumesContentHash, findGeneratedApp(second, appdef.AppContent).VolumesContentHash)
}
