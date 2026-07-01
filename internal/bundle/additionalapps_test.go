package bundle

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/require"
)

func TestValidateAdditionalApps(t *testing.T) {
	valid := AdditionalAppProps{
		Name:  "edi-sim",
		Image: "registry.citeck.ru/community/citeck-edi-sim:0.1.0",
	}
	require.NoError(t, ValidateAdditionalApps([]AdditionalAppProps{valid}))

	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{{Image: "x:1"}}), "missing name")
	require.Error(t, ValidateAdditionalApps([]AdditionalAppProps{{Name: "x"}}), "missing image")
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
