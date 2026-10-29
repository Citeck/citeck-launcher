package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The list is normalized on read — trimmed, de-duplicated, order kept — and
// written back only when non-empty, so a namespace that never chose a feature
// keeps a namespace.yml without the key.
func TestNamespaceConfig_ExperimentalFeaturesRoundTrip(t *testing.T) {
	cfg, err := ParseNamespaceConfig([]byte("id: ns1\nexperimentalFeatures: [' documentScanner ', backupEngineV2, documentScanner, '']\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"documentScanner", "backupEngineV2"}, cfg.ExperimentalFeatures)
	assert.Equal(t, map[string]bool{"documentScanner": true, "backupEngineV2": true}, cfg.ExperimentalEnabled())

	out, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(out), "experimentalFeatures:")
	back, err := ParseNamespaceConfig(out)
	require.NoError(t, err)
	assert.Equal(t, cfg.ExperimentalFeatures, back.ExperimentalFeatures)

	none, err := ParseNamespaceConfig([]byte("id: ns2\n"))
	require.NoError(t, err)
	assert.Nil(t, none.ExperimentalFeatures)
	assert.Nil(t, none.ExperimentalEnabled())
	out, err = MarshalNamespaceConfig(none)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "experimentalFeatures", "no choice, no key")
}

// A list of blanks is no list.
func TestNamespaceConfig_ExperimentalFeaturesBlankListIsNil(t *testing.T) {
	cfg, err := ParseNamespaceConfig([]byte("id: ns1\nexperimentalFeatures: ['', '  ']\n"))
	require.NoError(t, err)
	assert.Nil(t, cfg.ExperimentalFeatures)
}

// An id outside the grammar is refused at parse time — the ONE check the stored
// list gets. An id the bundle does not offer is deliberately not refused (see
// Config.ExperimentalFeatures): the parser has no bundle and must never need one.
func TestNamespaceConfig_ExperimentalFeatureIDGrammar(t *testing.T) {
	for _, ok := range []string{"documentScanner", "backup-engine_v2", "a.b", "7x"} {
		assert.True(t, bundle.IsValidExperimentalFeatureID(ok), ok)
	}
	for _, bad := range []string{"", "-x", ".x", "has space", "with/slash", "ключ"} {
		assert.False(t, bundle.IsValidExperimentalFeatureID(bad), bad)
	}

	_, err := ParseNamespaceConfig([]byte("id: ns1\nexperimentalFeatures: ['has space']\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature id")

	_, err = ParseNamespaceConfig([]byte("id: ns1\nexperimentalFeatures: [graduatedLongAgo]\n"))
	require.NoError(t, err, "an id no bundle offers is kept, not refused")
}

func TestNormalizeExperimentalFeatures(t *testing.T) {
	assert.Nil(t, NormalizeExperimentalFeatures(nil))
	assert.Nil(t, NormalizeExperimentalFeatures([]string{" ", ""}))
	assert.Equal(t, []string{"b", "a"}, NormalizeExperimentalFeatures([]string{" b", "a", "b ", "a"}))

	var nilCfg *Config
	assert.Nil(t, nilCfg.ExperimentalEnabled())
}
