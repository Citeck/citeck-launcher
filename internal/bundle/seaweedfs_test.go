package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSeaweedTypedDeclarationValidation(t *testing.T) {
	var a AdditionalAppProps
	require.NoError(t, yaml.Unmarshal([]byte(`name: storage
type: seaweedFS
volumeBase: sw_data
settings:
  master.volumeSizeLimitMB: 512
  admin.ui: true
  s3.domainName: 'objects.example.test'
`), &a))
	require.Equal(t, AppTypeSeaweedFS, a.Type)
	require.NoError(t, ValidateAdditionalApps([]AdditionalAppProps{a}))
	assert.Equal(t, "512", a.Settings["master.volumeSizeLimitMB"])
	assert.Equal(t, "true", a.Settings["admin.ui"])
	assert.Empty(t, a.Cmd)
	assert.Empty(t, a.Volumes)
	for _, bad := range []AdditionalAppProps{
		{Name: "storage", Type: a.Type, Volumes: []string{"other2:/data"}},
		{Name: "storage", Type: a.Type, Cmd: []string{"mini"}},
		{Name: "storage", Type: a.Type, Settings: map[string]string{"dir": "/other"}},
		{Name: "storage", Type: a.Type, Settings: map[string]string{"ip": "other"}},
		{Name: "storage", Type: a.Type, Settings: map[string]string{"s3.port": "9000"}},
		{Name: "storage", Type: a.Type, Settings: map[string]string{"--dir": "/data"}},
		{Name: "storage", Type: a.Type, Settings: map[string]string{"admin.ui": "invalid"}},
	} {
		require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{bad}))
	}
	for _, invalid := range []string{"settings: [x]", "settings: {x: [y]}", "settings: {x: {y: z}}"} {
		require.Error(t, yaml.Unmarshal([]byte("name: store\ntype: SEAWEEDFS\n"+invalid), &a))
	}
}

func TestSeaweedDefaultsDoNotMutateDeclaration(t *testing.T) {
	a := AdditionalAppProps{Name: "storage", Type: AppTypeSeaweedFS,
		Environments: map[string]string{"CUSTOM": "kept"}, Settings: map[string]string{"master.volumeSizeLimitMB": "512", "admin.ui": "true"}}
	first := a.WithSeaweedDefaults()
	second := a.WithSeaweedDefaults()
	assert.Equal(t, first, second)
	assert.Equal(t, "storage", a.SeaweedVolumeBase())
	assert.Contains(t, first.Cmd, "-master.volumeSizeLimitMB=512")
	assert.Contains(t, first.Cmd, "-dir=/data")
	assert.Contains(t, first.Cmd, "-ip=127.0.0.1")
	assert.True(t, first.SeaweedAdminEnabled())
	require.Len(t, first.StartupConditions, 1)
	assert.Equal(t, 8333, first.StartupConditions[0].Probe.HTTP.Port)
	assert.Equal(t, "/healthz", first.LivenessProbe.HTTP.Path)
	assert.Equal(t, "1g", first.Resources.Limits.Memory)
	first.Environments["CUSTOM"] = "changed"
	assert.Equal(t, "kept", a.Environments["CUSTOM"])
	assert.NotContains(t, a.Environments, "GOMEMLIMIT")
	assert.Nil(t, a.StartupConditions)
	assert.Nil(t, a.LivenessProbe)
	assert.Empty(t, a.Cmd)
	assert.Empty(t, a.Volumes)
}
