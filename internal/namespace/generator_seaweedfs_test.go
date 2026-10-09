package namespace

import (
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSeaweedGenerationUsesPinAndVolumeGeneration(t *testing.T) {
	ws := contentStorageWorkspace(t)
	descriptor := SeaweedDescriptor(ws.AdditionalApps[len(ws.AdditionalApps)-1])
	// Select by declared type, not by position or provider name.
	for _, a := range ws.AdditionalApps {
		if a.Type == bundle.AppTypeSeaweedFS {
			descriptor = SeaweedDescriptor(a)
		}
	}
	require.NotNil(t, descriptor)
	deps.SetExtraDependencies([]deps.Descriptor{descriptor})
	t.Cleanup(deps.ResetExtraDependencies)
	bun := enterpriseBundle()
	bun.Dependencies[seaweedfsApp] = bundle.AppDef{Image: "chrislusf/seaweedfs:4.49"}
	opts := contentStorageSecrets()
	opts.DependencyStates = map[deps.ID]deps.DependencyState{descriptor.ID(): {Image: "chrislusf/seaweedfs:4.48", VolumeGen: 2}}
	resp := generateContentStorage(t, s3Cfg(), bun, ws, opts)
	app := findGeneratedApp(resp, seaweedfsApp)
	require.NotNil(t, app)
	assert.Equal(t, "chrislusf/seaweedfs:4.48", app.Image)
	assert.Contains(t, app.Volumes, "seaweedfs3:/data")
	require.Len(t, resp.DependencyUpgrades, 1)
	assert.True(t, resp.DependencyUpgrades[0].Migratable)
	assert.False(t, resp.DependencyUpgrades[0].VendorBlocked)
	bun.Dependencies[seaweedfsApp] = bundle.AppDef{Image: "chrislusf/seaweedfs:4.47"}
	resp = generateContentStorage(t, s3Cfg(), bun, ws, opts)
	assert.Equal(t, "chrislusf/seaweedfs:4.48", findGeneratedApp(resp, seaweedfsApp).Image)
	require.Len(t, resp.DependencyUpgrades, 1)
	assert.True(t, resp.DependencyUpgrades[0].BundleOlder)
}

func TestSeaweedPublishesS3PortOnlyOnDesktop(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		t.Run(map[bool]string{false: "server", true: "desktop"}[desktop], func(t *testing.T) {
			config.SetDesktopMode(desktop)
			t.Cleanup(config.ResetDesktopMode)
			ws := contentStorageWorkspace(t)
			for i := range ws.AdditionalApps {
				if ws.AdditionalApps[i].Type == bundle.AppTypeSeaweedFS {
					ws.AdditionalApps[i].Ports = nil
				}
			}
			resp, err := Generate(s3Cfg(), enterpriseBundle(), ws, SystemSecrets{JWT: "test", OIDC: "test"}, contentStorageSecrets())
			require.NoError(t, err)
			provider := findGeneratedApp(resp, seaweedfsApp)
			require.NotNil(t, provider)
			if desktop {
				assert.Contains(t, provider.Ports, "8333:8333")
			} else {
				assert.Empty(t, provider.Ports)
			}
		})
	}
}

func TestMinimalSeaweedDeclarationOwnsItsFrame(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	entry := bundle.AdditionalAppProps{Name: "storage-lab", Type: bundle.AppTypeSeaweedFS, Image: "chrislusf/seaweedfs:4.48"}
	require.NoError(t, bundle.ValidateAdditionalApps([]bundle.AdditionalAppProps{entry}))
	ws := wsWithApps([]bundle.AdditionalAppProps{entry})
	bun := &bundle.Def{}
	generate := func() *GenResp {
		resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "test", OIDC: "test"})
		require.NoError(t, err)
		return resp
	}
	resp := generate()
	app := findGeneratedApp(resp, entry.Name)
	require.NotNil(t, app)
	assert.Contains(t, app.Volumes, "storage-lab2:/data")
	assert.Equal(t, []string{"8333:8333"}, app.Ports)
	require.Len(t, app.StartupConditions, 1)
	assert.Equal(t, 8333, app.StartupConditions[0].Probe.HTTP.Port)
	require.NotNil(t, app.LivenessProbe)
	assert.Equal(t, "/healthz", app.LivenessProbe.HTTP.Path)
	assert.Contains(t, app.Cmd, "-disableHttp=true")
	assert.NotContains(t, app.Cmd, "-admin.ui=true")
	assert.Empty(t, ws.AdditionalApps[0].Cmd)
	assert.Empty(t, ws.AdditionalApps[0].Volumes)

	// A bundle may name the store in applications too: it must stay a mini
	// container, rather than becoming a JVM webapp and colliding with itself.
	bun.Applications = map[string]bundle.AppDef{entry.Name: {Image: "chrislusf/seaweedfs:4.48"}}
	app = findGeneratedApp(generate(), entry.Name)
	require.NotNil(t, app)
	require.NotEmpty(t, app.Cmd)
	assert.Equal(t, "mini", app.Cmd[0])
	assert.Contains(t, app.Volumes, "storage-lab2:/data")

	ws.AdditionalApps[0].VolumeBase = "retained-data"
	ws.AdditionalApps[0].Settings = map[string]string{"admin.ui": "true", "master.volumeSizeLimitMB": "512"}
	ws.AdditionalApps[0].Ports = []string{"38333:8333", "33646:23646"}
	resp = generate()
	app = findGeneratedApp(resp, entry.Name)
	require.NotNil(t, app)
	assert.Contains(t, app.Volumes, "retained-data2:/data")
	assert.Equal(t, []string{"38333:8333", "33646:23646"}, app.Ports)
	assert.Contains(t, app.Cmd, "-master.volumeSizeLimitMB=512")
	assert.Contains(t, app.Cmd, "-admin.ui=true")
}
