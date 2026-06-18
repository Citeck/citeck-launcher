package appdef

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// An omitted probe field must inherit the Kotlin 1.x default, not Go's zero —
// failureThreshold:0 would be a probe that fails on the first check.
func TestAppProbeDef_DefaultsOnDecode(t *testing.T) {
	// YAML with only http set: all four numeric fields default.
	var py AppProbeDef
	require.NoError(t, yaml.Unmarshal([]byte("http:\n  path: /health\n  port: 8080\n"), &py))
	assert.Equal(t, 5, py.InitialDelaySeconds)
	assert.Equal(t, 10, py.PeriodSeconds)
	assert.Equal(t, 10_000, py.FailureThreshold)
	assert.Equal(t, 5, py.TimeoutSeconds)
	assert.Equal(t, 8080, py.HTTP.Port)

	// Explicit values are preserved (only zero fields are filled).
	var px AppProbeDef
	require.NoError(t, yaml.Unmarshal([]byte("periodSeconds: 3\nfailureThreshold: 60\n"), &px))
	assert.Equal(t, 3, px.PeriodSeconds)
	assert.Equal(t, 60, px.FailureThreshold)
	assert.Equal(t, 5, px.InitialDelaySeconds) // still defaulted

	// Same via JSON (migrated / persisted state path).
	var pj AppProbeDef
	require.NoError(t, json.Unmarshal([]byte(`{"http":{"path":"/h","port":80}}`), &pj))
	assert.Equal(t, 10_000, pj.FailureThreshold)
	assert.Equal(t, 10, pj.PeriodSeconds)
}

// WithProbeDefaults fills the same defaults a decode would, WITHOUT mutating the
// caller's shared probe pointers — encodeDefYAML relies on it to make the
// app-config baseline (a generator-built def, never decoded) match the patched
// content's probe defaults.
func TestWithProbeDefaults(t *testing.T) {
	startProbe := &AppProbeDef{HTTP: &HTTPProbeDef{Path: "/h", Port: 17023}, PeriodSeconds: 10, FailureThreshold: 10000, TimeoutSeconds: 5}
	liveProbe := &AppProbeDef{HTTP: &HTTPProbeDef{Path: "/h", Port: 17023}, FailureThreshold: 3, TimeoutSeconds: 5}
	d := ApplicationDef{
		Name:              "eapps",
		StartupConditions: []StartupCondition{{Probe: startProbe}, {Log: &LogStartupCondition{Pattern: "ready"}}},
		LivenessProbe:     liveProbe,
	}

	got := d.WithProbeDefaults()

	// Defaults filled on the returned copy.
	assert.Equal(t, 5, got.StartupConditions[0].Probe.InitialDelaySeconds)
	assert.Equal(t, 10, got.StartupConditions[0].Probe.PeriodSeconds)
	assert.Equal(t, 60, got.StartupConditions[1].Log.TimeoutSeconds)
	assert.Equal(t, 5, got.LivenessProbe.InitialDelaySeconds)
	assert.Equal(t, 10, got.LivenessProbe.PeriodSeconds)

	// Originals untouched (no shared-pointer mutation).
	assert.Equal(t, 0, startProbe.InitialDelaySeconds, "source startup probe mutated")
	assert.Equal(t, 0, liveProbe.InitialDelaySeconds, "source liveness probe mutated")
	assert.NotSame(t, startProbe, got.StartupConditions[0].Probe)

	// Nil-probe / empty def is a no-op (no panic).
	_ = ApplicationDef{Name: "x"}.WithProbeDefaults()
}

// LogStartupCondition.timeoutSeconds defaults to 60 when omitted.
func TestLogStartupCondition_DefaultTimeout(t *testing.T) {
	var ly LogStartupCondition
	require.NoError(t, yaml.Unmarshal([]byte("pattern: 'Started'\n"), &ly))
	assert.Equal(t, "Started", ly.Pattern)
	assert.Equal(t, 60, ly.TimeoutSeconds)

	var lj LogStartupCondition
	require.NoError(t, json.Unmarshal([]byte(`{"pattern":"Started","timeoutSeconds":5}`), &lj))
	assert.Equal(t, 5, lj.TimeoutSeconds) // explicit preserved
}
