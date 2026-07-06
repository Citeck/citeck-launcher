package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generatedDefForApp must return the patch-free baseline from generatedDefs,
// never a (possibly effective) lastApps entry.
func TestGeneratedDefForApp_ReadsBaselineNotLastApps(t *testing.T) {
	r := &Runtime{
		generatedDefs: map[string]appdef.ApplicationDef{
			"rabbitmq": {Name: "rabbitmq", Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}},
		},
		// lastApps holds a DIFFERENT (e.g. effective) value — must be ignored.
		lastApps: []appdef.ApplicationDef{
			{Name: "rabbitmq", Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "2g"}}},
		},
	}
	got, ok := r.generatedDefForApp("rabbitmq")
	require.True(t, ok)
	assert.Equal(t, "1g", got.Resources.Limits.Memory, "must return the baseline, not lastApps")
}
