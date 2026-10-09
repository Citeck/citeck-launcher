package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteNamespaceConfigOfflinePolicy(t *testing.T) {
	t.Setenv("CITECK_RUN", t.TempDir())
	t.Setenv("CITECK_HOST", "")
	ref := bundle.Ref{Repo: "enterprise", Key: "2026.2"}
	for _, tc := range []struct {
		name          string
		before, after namespace.ContentStorageType
		bundle        string
		allowed       bool
	}{
		{"ordinary DB", namespace.ContentStorageDB, namespace.ContentStorageDB, "2026.2", true},
		{"ordinary S3", namespace.ContentStorageS3, namespace.ContentStorageS3, "2026.2", true},
		{"S3 downgrade", namespace.ContentStorageS3, namespace.ContentStorageDB, "2026.2", false},
		{"new S3", namespace.ContentStorageDB, namespace.ContentStorageS3, "2026.2", false},
		{"S3 bundle change", namespace.ContentStorageS3, namespace.ContentStorageS3, "2026.3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "namespace.yml")
			require.NoError(t, os.WriteFile(path, []byte("unchanged"), 0o600))
			before := namespace.DefaultNamespaceConfig()
			before.BundleRef = ref
			before.ContentStorage = &namespace.ContentStorageProps{Type: tc.before}
			next, err := deepCopy(&before)
			require.NoError(t, err)
			next.ContentStorage.Type = tc.after
			next.BundleRef.Key = tc.bundle
			err = writeNamespaceConfig(path, &before, next)
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			if tc.allowed {
				require.NoError(t, err)
				assert.NotEqual(t, "unchanged", string(raw))
			} else {
				require.Error(t, err)
				assert.Equal(t, "unchanged", string(raw))
			}
		})
	}
}

func TestWriteNamespaceConfigOfflineExternalRemovalRefused(t *testing.T) {
	t.Setenv("CITECK_RUN", t.TempDir())
	t.Setenv("CITECK_HOST", "")
	path := filepath.Join(t.TempDir(), "namespace.yml")
	require.NoError(t, os.WriteFile(path, []byte("unchanged"), 0o600))
	before := namespace.DefaultNamespaceConfig()
	before.S3 = &namespace.S3Config{Endpoint: "https://external.example", Bucket: "custom-bucket", AccessKey: "access", SecretKey: "secret:s3.secretKey"}
	next, err := deepCopy(&before)
	require.NoError(t, err)
	next.S3 = nil
	require.Error(t, writeNamespaceConfig(path, &before, next))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(raw))
}
