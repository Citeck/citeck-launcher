package bundle

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const contentStorageBlock = `
contentStorage:
  s3:
    app: seaweedfs
    url: http://seaweedfs:8333
    bucket: ecos-content
    accessKey: citeck
    secretKey: ${secret:seaweedfs-s3}
`

// The block as the workspace declares it reaches the reader whole.
func TestWorkspaceContentStorage_IsRead(t *testing.T) {
	cfg := parseTestWorkspace(t, contentStorageBlock)
	s3 := cfg.ContentStorageS3()
	require.NotNil(t, s3)
	assert.Equal(t, ContentStorageS3Config{
		App: "seaweedfs", URL: "http://seaweedfs:8333", Bucket: "ecos-content",
		AccessKey: "citeck", SecretKey: "${secret:seaweedfs-s3}",
	}, *s3)
}

// The point of the custom decoder: a block of the WRONG SHAPE must cost only
// itself. A plain struct field would turn it into a YAML error, and
// parseWorkspaceConfig answers a YAML error by dropping the whole workspace
// config — every imageRepo, webapp and bundleRepo of every namespace on it.
func TestWorkspaceContentStorage_AWrongShapeCostsOnlyItself(t *testing.T) {
	for name, block := range map[string]string{
		"s3 is a list":      "contentStorage:\n  s3: [a, b]\n",
		"block is scalar":   "contentStorage: yes\n",
		"field is a map":    "contentStorage:\n  s3:\n    app: {name: seaweedfs}\n",
		"block is a list":   "contentStorage:\n  - s3\n",
		"url is a sequence": "contentStorage:\n  s3:\n    app: x\n    url: [1]\n",
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			cfg := parseWorkspaceConfig([]byte(block+`
imageRepos:
  - id: core
    url: nexus.citeck.ru
webapps:
  - id: emodel
`), "workspace-v1.yml", slog.New(slog.NewTextHandler(&logs, nil)))
			require.NotNil(t, cfg, "the workspace config must survive an unreadable block")
			assert.Len(t, cfg.ImageRepos, 1, "the rest of the config is untouched")
			assert.Len(t, cfg.Webapps, 1)
			assert.Nil(t, cfg.ContentStorageS3(), "an unusable block is treated as absent")
			assert.Nil(t, cfg.ContentStorage, "and dropped, so no reader sees half of it")
			assert.Contains(t, logs.String(), "contentStorage block is unusable",
				"and it is said once, out loud")
		})
	}
}

// Every field is required — without the endpoint, the bucket or the
// credentials the provider is useless to ecos-content, so S3 must read as
// unavailable rather than half-configured.
func TestWorkspaceContentStorage_AMissingFieldMakesItAbsent(t *testing.T) {
	var logs bytes.Buffer
	cfg := parseWorkspaceConfig([]byte(`
contentStorage:
  s3:
    app: seaweedfs
    url: http://seaweedfs:8333
    accessKey: citeck
`), "workspace-v1.yml", slog.New(slog.NewTextHandler(&logs, nil)))
	require.NotNil(t, cfg)
	assert.Nil(t, cfg.ContentStorageS3())
	assert.Contains(t, logs.String(), "bucket")
	assert.Contains(t, logs.String(), "secretKey")
}

// A block with no s3 key at all is not a problem — it names no provider.
func TestWorkspaceContentStorage_AnEmptyBlockIsQuiet(t *testing.T) {
	var logs bytes.Buffer
	cfg := parseWorkspaceConfig([]byte("contentStorage: {}\n"), "workspace-v1.yml",
		slog.New(slog.NewTextHandler(&logs, nil)))
	require.NotNil(t, cfg)
	assert.Nil(t, cfg.ContentStorageS3())
	assert.NotContains(t, logs.String(), "contentStorage")
}

// A scalar value of another YAML type is still a string to a reader that wants
// one: an unquoted number is a bucket name, not an unusable block.
func TestWorkspaceContentStorage_ANumericScalarIsAString(t *testing.T) {
	cfg := parseTestWorkspace(t, `
contentStorage:
  s3:
    app: seaweedfs
    url: http://seaweedfs:8333
    bucket: 2026
    accessKey: citeck
    secretKey: s
`)
	require.NotNil(t, cfg.ContentStorageS3())
	assert.Equal(t, "2026", cfg.ContentStorageS3().Bucket)
}

func TestWorkspaceContentStorage_NilSafe(t *testing.T) {
	var ws *WorkspaceConfig
	assert.Nil(t, ws.ContentStorageS3())
	_, ok := ws.AdditionalApp("seaweedfs")
	assert.False(t, ok)
}

// AdditionalApp answers only an entry that becomes a raw container: a typed
// entry is a database or a store, and a disabled one runs nothing.
func TestWorkspaceAdditionalApp(t *testing.T) {
	off := false
	ws := &WorkspaceConfig{AdditionalApps: []AdditionalAppProps{
		{Name: "seaweedfs"},
		{Name: "off", Enabled: &off},
		{Name: "db", Type: AppTypePostgres, Postgres: &PostgresAppProps{Name: "db"}},
	}}
	_, ok := ws.AdditionalApp("seaweedfs")
	assert.True(t, ok)
	_, ok = ws.AdditionalApp("off")
	assert.False(t, ok)
	_, ok = ws.AdditionalApp("db")
	assert.False(t, ok)
	_, ok = ws.AdditionalApp("missing")
	assert.False(t, ok)
}
