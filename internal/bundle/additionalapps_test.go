package bundle

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestValidateAdditionalApps(t *testing.T) {
	valid := AdditionalAppProps{
		Name:  "edi-sim",
		Image: "registry.citeck.ru/community/citeck-edi-sim:0.1.0",
	}
	require.NoError(t, ValidateAdditionalApps([]AdditionalAppProps{valid}))

	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{{Image: "x:1"}}), "missing name")
	require.NoError(t, ValidateAdditionalApps([]AdditionalAppProps{{Name: "x"}}),
		"no image: the bundle names it, or the entry is not generated")
	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{
		{Name: appdef.AppZookeeper, Image: "x:1"}}), "reserved name")
	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{
		{Name: "dup", Image: "a:1"},
		{Name: "dup", Image: "b:1"}}), "duplicate name")
	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{{
		Name: "x", Image: "x:1",
		InitContainers: []appdef.InitContainerDef{{Image: ""}}}}), "init container without image")
	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{{
		Name: "x", Image: "x:1", StopTimeout: -1}}), "negative stopTimeout")
}

func TestWorkspaceSecretsSectionIsRead(t *testing.T) {
	var cfg WorkspaceConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
secrets:
  - id: observer-db
    value: observer
  - id: empty
    value: ""
`), &cfg))
	assert.Equal(t, []SecretDefault{{ID: "observer-db", Value: "observer"}, {ID: "empty", Value: ""}}, cfg.Secrets)
}
