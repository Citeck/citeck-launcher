package bundle

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func parseWithLogs(t *testing.T, raw string) (cfg *WorkspaceConfig, logs string) {
	t.Helper()
	var buf bytes.Buffer
	cfg = parseWorkspaceConfig([]byte(raw), "workspace-v1.yml",
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	require.NotNil(t, cfg, "a secrets entry must never cost the whole workspace config")
	return cfg, buf.String()
}

// Both spellings are read: a constant exactly as before, a generated entry with
// its length or the default one.
func TestLegacyGeneratedSecretsAreRead(t *testing.T) {
	cfg, logs := parseWithLogs(t, `
secrets:
  - id: observer-db
    value: observer
  - generated: seaweedfs-s3
  - generated: seaweedfs-jwt
    length: 64
  - id: empty
    value: ""
`)
	assert.Equal(t, []SecretDefault{
		{ID: "observer-db", Value: "observer"},
		{ID: "seaweedfs-s3", Generated: true, Length: DefaultGeneratedSecretLength},
		{ID: "seaweedfs-jwt", Generated: true, Length: 64},
		{ID: "empty", Value: ""},
	}, cfg.Secrets)
	assert.NotContains(t, logs, "level=WARN")
}

// Every entry this launcher cannot use is dropped with exactly ONE warning
// naming why, and costs nothing else: the usable entries and the rest of the
// workspace config survive.
func TestUnusableSecretEntriesAreDroppedOneWarningEach(t *testing.T) {
	cases := []struct {
		name, entry, problem string
	}{
		{"both id and generated", "- id: a\n    generated: a", "both id and generated"},
		{"generated with a value", "- generated: a\n    value: x", "takes no value"},
		{"constant with a length", "- id: a\n    length: 40", "length applies only to a generated secret"},
		{"neither", "- value: x", "neither id nor generated"},
		{"a scalar", "- just-a-string", "unreadable"},
		{"invalid generated id", "- generated: \"has space\"", "is not a secret id"},
		{"invalid constant id", "- id: \"a/b\"\n    value: x", "is not a secret id"},
		{"empty generated id", "- generated: \"\"", "is not a secret id"},
		{"length below the floor", "- generated: a\n    length: 15", "length 15 is outside 16..128"},
		{"length above the ceiling", "- generated: a\n    length: 129", "length 129 is outside 16..128"},
		{"length not a number", "- generated: a\n    length: forty", "is not a whole number"},
		{"length not a scalar", "- generated: a\n    length: [40]", "is not a whole number"},
		{"value not a scalar", "- id: a\n    value: [x]", "unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, logs := parseWithLogs(t, `
imageRepos:
  - id: core
    url: example.org/core
secrets:
  - id: kept
    value: v
  `+tc.entry+`
  - generated: also-kept
`)
			assert.Equal(t, []SecretDefault{
				{ID: "kept", Value: "v"},
				{ID: "also-kept", Generated: true, Length: DefaultGeneratedSecretLength},
			}, cfg.Secrets)
			require.Len(t, cfg.ImageRepos, 1, "the rest of the workspace config is untouched")
			assert.Equal(t, 1, strings.Count(logs, "level=WARN"), logs)
			assert.Contains(t, logs, "Workspace secret entry is unusable")
			assert.Contains(t, logs, tc.problem)
		})
	}
}

// The range is inclusive at both ends.
func TestGeneratedSecretLengthBoundsAreInclusive(t *testing.T) {
	cfg, logs := parseWithLogs(t, `
secrets:
  - generated: lo
    length: 16
  - generated: hi
    length: 128
`)
	assert.Equal(t, []SecretDefault{
		{ID: "lo", Generated: true, Length: MinGeneratedSecretLength},
		{ID: "hi", Generated: true, Length: MaxGeneratedSecretLength},
	}, cfg.Secrets)
	assert.NotContains(t, logs, "level=WARN")
}

// An id declared twice keeps its first entry — what a namespace always seeded
// from — and the later one is reported instead of silently shadowed.
func TestASecretDeclaredTwiceKeepsTheFirst(t *testing.T) {
	cfg, logs := parseWithLogs(t, `
secrets:
  - id: k
    value: first
  - generated: k
`)
	assert.Equal(t, []SecretDefault{{ID: "k", Value: "first"}}, cfg.Secrets)
	assert.Equal(t, 1, strings.Count(logs, "level=WARN"), logs)
	assert.Contains(t, logs, "declared more than once")
}

// A warning names the problem, never a VALUE: the constant's value is a
// password.
func TestASecretEntryWarningDoesNotPrintTheValue(t *testing.T) {
	_, logs := parseWithLogs(t, `
secrets:
  - id: a
    generated: a
    value: hunter2-password
`)
	assert.Contains(t, logs, "Workspace secret entry is unusable")
	assert.NotContains(t, logs, "hunter2-password")
}

// The id grammar is the one `${secret:<id>}` matches.
func TestValidSecretID(t *testing.T) {
	for _, id := range []string{"a", "seaweedfs-s3", "A.b_c-9"} {
		assert.True(t, ValidSecretID(id), id)
	}
	for _, id := range []string{"", "a b", "a/b", "a:b", "${x}", "ä"} {
		assert.False(t, ValidSecretID(id), id)
	}
}

func TestGeneratedLengthDefaultsForAnEntryBuiltInCode(t *testing.T) {
	assert.Equal(t, DefaultGeneratedSecretLength, SecretDefault{ID: "x", Generated: true}.GeneratedLength())
	assert.Equal(t, 20, SecretDefault{ID: "x", Generated: true, Length: 20}.GeneratedLength())
}

func TestGeneratedSecretOptionsAreRead(t *testing.T) {
	cfg, logs := parseWithLogs(t, `
secrets:
  - id: observer-db
    value: observer
  - id: seaweedfs-s3-key
    generated: {}
  - id: seaweedfs-jwt
    generated:
      length: 48
  - id: lo
    generated:
      length: 16
  - id: hi
    generated:
      length: 128
  - id: anchored
    generated: &options
      length: 64
  - id: alias
    generated: *options
  - id: merged
    generated:
      <<: *options
      length: 32
`)
	assert.Equal(t, []SecretDefault{
		{ID: "observer-db", Value: "observer"},
		{ID: "seaweedfs-s3-key", Generated: true, Length: DefaultGeneratedSecretLength},
		{ID: "seaweedfs-jwt", Generated: true, Length: 48},
		{ID: "lo", Generated: true, Length: 16},
		{ID: "hi", Generated: true, Length: 128},
		{ID: "anchored", Generated: true, Length: 64},
		{ID: "alias", Generated: true, Length: 64},
		{ID: "merged", Generated: true, Length: 32},
	}, cfg.Secrets)
	assert.NotContains(t, logs, "level=WARN")
}

func TestUnusableGeneratedOptionsAreDropped(t *testing.T) {
	for _, tc := range []struct{ name, entry, problem string }{
		{"missing id", "- generated: {length: 48}", "require an id"},
		{"invalid id", "- id: 'has space'\n    generated: {}", "is not a secret id"},
		{"empty id", "- id: ''\n    generated: {}", "is not a secret id"},
		{"value conflict", "- id: k\n    generated: {}\n    value: do-not-log-this", "takes no value"},
		{"null value conflict", "- id: k\n    generated: {}\n    value: null", "takes no value"},
		{"length outside options", "- id: k\n    generated: {}\n    length: 48", "length belongs under generated"},
		{"length too short", "- id: k\n    generated: {length: 15}", "outside 16..128"},
		{"length too long", "- id: k\n    generated: {length: 129}", "outside 16..128"},
		{"length not integer", "- id: k\n    generated: {length: forty}", "is not a whole number"},
		{"length not scalar", "- id: k\n    generated: {length: [40]}", "is not a whole number"},
		{"unknown option", "- id: k\n    generated: {size: 48}", "unknown generated option"},
		{"null options", "- id: k\n    generated: null", "requires generated options"},
		{"sequence options", "- id: k\n    generated: []", "requires generated options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, logs := parseWithLogs(t, "imageRepos:\n  - id: core\n    url: example.org\nsecrets:\n  "+tc.entry+"\n  - id: kept\n    generated: {length: 48}\n")
			assert.Equal(t, []SecretDefault{{ID: "kept", Generated: true, Length: 48}}, cfg.Secrets)
			require.Len(t, cfg.ImageRepos, 1)
			assert.Equal(t, 1, strings.Count(logs, "level=WARN"), logs)
			assert.Contains(t, logs, tc.problem)
			assert.NotContains(t, logs, "do-not-log-this")
		})
	}
}

func TestSecretYAMLRoundTripUsesCanonicalOptions(t *testing.T) {
	cfg, _ := parseWithLogs(t, `
secrets:
  - generated: legacy
    length: 48
  - id: nested
    generated: {}
  - id: constant
    value: unchanged
`)
	data, err := yaml.Marshal(struct {
		Secrets []SecretDefault `yaml:"secrets"`
	}{cfg.Secrets})
	require.NoError(t, err)
	assert.Contains(t, string(data), "id: legacy")
	assert.Contains(t, string(data), "generated:\n")
	assert.NotContains(t, string(data), "generated: legacy")
	parsed, logs := parseWithLogs(t, string(data))
	assert.Equal(t, cfg.Secrets, parsed.Secrets)
	assert.NotContains(t, logs, "level=WARN")
}
