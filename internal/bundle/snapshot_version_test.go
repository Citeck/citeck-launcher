package bundle

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSnapshotMinimumBundleVersion(t *testing.T) {
	for _, tc := range []struct {
		name, minimum, current, refusal string
	}{
		{"legacy", "", "", ""},
		{"blank", "  ", "2025.1", ""},
		{"older", "2026.3", "2026.2", "snapshot.bundleMinimum"},
		{"equal", "2026.3", "2026.3", ""},
		{"newer", "2026.3", "2026.10", ""},
		{"numeric", "2026.10", "2026.9", "snapshot.bundleMinimum"},
		{"trailing zero", "2026.3.0", "2026.3", ""},
		{"before RC", "2026.3-RC6", "2026.3-RC5", "snapshot.bundleMinimum"},
		{"equal RC", "2026.3-RC6", "2026.3-RC6", ""},
		{"numeric RC", "2026.3-RC6", "2026.3-RC10", ""},
		{"release after RC", "2026.3-RC6", "2026.3", ""},
		{"RC before release", "2026.3", "2026.3-RC9", "snapshot.bundleMinimum"},
		{"scoped key", "2026.3", "archive/2026.3", ""},
		{"unknown", "2026.3", "", "snapshot.bundleVersionUnknown"},
		{"bad floor", "not-a-version", "2026.3", "snapshot.bundleMinimumInvalid"},
		{"concrete floor only", "LATEST", "2026.3", "snapshot.bundleMinimumInvalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := SnapshotDef{ID: "demo", MinBundleVersion: tc.minimum}
			require.Equal(t, tc.refusal, def.BundleVersionRefusal(tc.current).Key)
		})
	}
}

func TestSnapshotMinimumBundleVersionYAMLRoundTrip(t *testing.T) {
	// Unquoted numeric versions must retain their spelling, not turn 2026.10
	// into 2026.1 through a generic numeric map conversion.
	var ws WorkspaceConfig
	require.NoError(t, yaml.Unmarshal([]byte("snapshots:\n  - id: demo\n    minBundleVersion: 2026.10\n  - id: legacy\n"), &ws))
	require.Equal(t, "2026.10", ws.Snapshots[0].MinBundleVersion)
	require.Empty(t, ws.Snapshots[1].MinBundleVersion)
	raw, err := yaml.Marshal(ws)
	require.NoError(t, err)
	var roundTrip WorkspaceConfig
	require.NoError(t, yaml.Unmarshal(raw, &roundTrip))
	require.Equal(t, ws.Snapshots, roundTrip.Snapshots)
}
