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
// way a workspace config is: the provider entry, its secret and the
// contentStorage block that names it.
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

func contentStorageSecrets() GenerateOpts {
	return GenerateOpts{NamespaceSecrets: map[string]string{"seaweedfs-s3": "seaweedfs-s3-secret"}}
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
		assert.Equal(t, ContentStorageAvailability{Available: true, ProviderApp: seaweedfsApp}, got)
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

	// Nothing else: the connection is made through ecos-content's records,
	// not its environment.
	db := generateContentStorage(t, basicCfg(), enterpriseBundle(), contentStorageWorkspace(t), contentStorageSecrets())
	dbContent := findGeneratedApp(db, appdef.AppContent)
	require.NotNil(t, dbContent)
	assert.Equal(t, dbContent.Environments, content.Environments, "no env is added for S3")
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
