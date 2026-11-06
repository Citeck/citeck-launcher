package docker

import (
	"context"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/require"
)

func TestCPUQuotaReachesDockerAndAbsentQuotaIsUnchanged(t *testing.T) {
	def := appdef.ApplicationDef{Name: "app", Image: "app:1"}
	host := buildHostConfig(def, ContainerCreateOpts{}, nil, nil, "network", 0, 0)
	require.Zero(t, host.NanoCPUs)
	def.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{CPU: 0.5}}
	host = buildHostConfig(def, ContainerCreateOpts{}, nil, nil, "network", 0, 0)
	require.Equal(t, int64(500000000), host.NanoCPUs)
	def.Resources.Limits.CPU = -1
	_, err := (&Client{}).buildCreateOptions(context.Background(), def, "", ContainerCreateOpts{})
	require.ErrorContains(t, err, "CPU")
}
