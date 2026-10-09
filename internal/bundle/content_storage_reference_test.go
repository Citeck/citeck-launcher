package bundle

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reference S3 workspace fixture uses the current secret-options format
// (internal/namespace/testdata/content-storage-workspace.yml). The generator tests read it with a plain
// yaml.Unmarshal, which keeps every entry; production reads it through
// parseWorkspaceConfig, which DROPS a secrets entry it cannot use (a length out
// of range, a value under `generated:`, an id declared twice), a contentStorage
// block with a field missing, or the whole additionalApps section over one bad
// entry — each with a WARN and nothing else. Such a copy would keep every
// generator test green while every real stand left the store out
// (PROVIDER_NOT_GENERATED) and S3 namespaces lost their content storage. So the
// copy goes through the production parser here, once, and nothing of it may be
// dropped.
var contentStorageReferenceCopy = filepath.Join("..", "namespace", "testdata", "content-storage-workspace.yml")

var secretRefRe = regexp.MustCompile(`\$\{secret:(` + SecretIDPattern + `)\}`)

func TestContentStorageReferenceCopySurvivesTheProductionParser(t *testing.T) {
	raw, err := os.ReadFile(contentStorageReferenceCopy)
	require.NoError(t, err)
	var logs bytes.Buffer
	cfg := parseWorkspaceConfig(raw, contentStorageReferenceCopy,
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	require.NotNil(t, cfg, "the copy must parse as a workspace config")
	assert.NotContains(t, logs.String(), "level=WARN", "production dropped part of the reference copy")

	s3 := cfg.ContentStorageS3()
	require.NotNil(t, s3, "the contentStorage block must survive")
	entry, ok := cfg.AdditionalApp(s3.App)
	require.True(t, ok, "the provider's additionalApps entry must survive")

	// Every secret the store is configured with is declared, and GENERATED:
	// a namespace draws its own value, so no stand runs on a key published in
	// the workspace repository.
	declared := map[string]SecretDefault{}
	for _, s := range cfg.Secrets {
		declared[s.ID] = s
	}
	values := make([]string, 0, 2+len(entry.Cmd)+len(entry.Environments))
	values = append(values, s3.AccessKey, s3.SecretKey)
	values = append(values, entry.Cmd...)
	for _, v := range entry.Environments {
		values = append(values, v)
	}
	var referenced []string
	for _, v := range values {
		for _, m := range secretRefRe.FindAllStringSubmatch(v, -1) {
			if !slices.Contains(referenced, m[1]) {
				referenced = append(referenced, m[1])
			}
		}
	}
	slices.Sort(referenced)
	require.NotEmpty(t, referenced)
	for _, id := range referenced {
		s, ok := declared[id]
		if assert.Truef(t, ok, "%s is referenced but not declared", id) {
			assert.Truef(t, s.Generated, "%s must be a generated secret", id)
			assert.GreaterOrEqualf(t, s.GeneratedLength(), 40, "%s is a key: at least the default length", id)
		}
	}
	assert.Len(t, cfg.Secrets, len(referenced), "the copy declares exactly the store's secrets")

	// The sidebar link, depending on the store alone.
	i := slices.IndexFunc(cfg.Links, func(l WorkspaceLink) bool { return slices.Equal(l.DependsOn, []string{s3.App}) })
	assert.GreaterOrEqual(t, i, 0, "the store's sidebar link must survive")
}
