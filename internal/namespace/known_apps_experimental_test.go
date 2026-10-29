package namespace

import (
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An opted-in beta module is the operator's explicit choice: the template's
// detach list does not hold it back, and it is recorded as known so a release
// that graduates it to a regular application keeps it running.
func TestAnOptedInModuleIsNeverDetachedAndBecomesKnown(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Known:            []string{appdef.AppPostgres},
		Generated:        []string{appdef.AppPostgres, "citeck-scanner", appdef.AppRag},
		OptedIn:          []string{"citeck-scanner"},
		TemplateDetached: []string{"citeck-scanner", appdef.AppRag},
	})
	assert.Equal(t, []string{appdef.AppRag}, res.Detach,
		"the template still decides for an app nobody asked for")
	assert.Contains(t, res.Known, "citeck-scanner")
}

// OptedIn is applied AFTER the baseline fallback: with no persisted Known, the
// regular apps still come from the frozen baseline and are not read as new.
func TestAnOptedInModuleDoesNotDefeatTheBaselineFallback(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Generated:        []string{appdef.AppOnlyoffice, "citeck-scanner", appdef.AppSttSidecar},
		OptedIn:          []string{"citeck-scanner"},
		TemplateDetached: []string{appdef.AppOnlyoffice, "citeck-scanner", appdef.AppSttSidecar},
	})
	assert.Equal(t, []string{appdef.AppSttSidecar}, res.Detach)
	assert.Contains(t, res.Known, appdef.AppOnlyoffice, "the baseline still stands in")
	assert.Contains(t, res.Known, "citeck-scanner")
}

// The marker is json:"-": a patch can neither remove it from a beta module nor
// put it on a regular application.
func TestApplyAppDefPatchKeepsTheExperimentalMarker(t *testing.T) {
	out, err := ApplyAppDefPatch(appdef.ApplicationDef{Name: "citeck-scanner", Image: "x:1", Experimental: true},
		json.RawMessage(`{"image":"x:2"}`))
	require.NoError(t, err)
	assert.Equal(t, "x:2", out.Image)
	assert.True(t, out.Experimental)

	plain, err := ApplyAppDefPatch(appdef.ApplicationDef{Name: "emodel", Image: "e:1"},
		json.RawMessage(`{"experimental":true}`))
	require.NoError(t, err)
	assert.False(t, plain.Experimental)
}
