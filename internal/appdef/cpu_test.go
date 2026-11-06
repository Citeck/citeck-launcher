package appdef

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNanoCPUs(t *testing.T) {
	for _, v := range []struct {
		cores float64
		n     int64
	}{{0, 0}, {0.5, 500000000}, {2, 2000000000}, {0.000000001, 1}} {
		n, e := NanoCPUs(v.cores)
		require.NoError(t, e)
		require.Equal(t, v.n, n)
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 1e10, 1e-10, 0.1234567891} {
		_, e := NanoCPUs(v)
		require.Error(t, e)
	}
}
func TestCPUDefRoundTripAndLegacyHashStability(t *testing.T) {
	d := ApplicationDef{Name: "app", Image: "image:1", Resources: &AppResourcesDef{Limits: LimitsDef{Memory: "1g"}}}
	legacy := d.GetHashInput()
	require.NotContains(t, legacy, "cpu=")
	d.Resources.Limits.CPU = 0.25
	require.Equal(t, legacy+"cpu=0.25\n", d.GetHashInput())
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		b, e := marshal(d)
		require.NoError(t, e)
		var out ApplicationDef
		require.NoError(t, yaml.Unmarshal(b, &out))
		require.Equal(t, d.Resources, out.Resources)
	}
}
