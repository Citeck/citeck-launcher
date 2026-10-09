package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
)

func TestContentStorageTransitionPolicy(t *testing.T) {
	old := bundle.Ref{Repo: "enterprise", Key: "2026.2"}
	newer := bundle.Ref{Repo: "enterprise", Key: "2026.3"}
	for _, tc := range []struct {
		name          string
		before, after ContentStorageType
		ref           bundle.Ref
		want          ContentStorageTransition
	}{
		{"DB unchanged", ContentStorageDB, ContentStorageDB, old, ContentStorageTransition{}},
		{"DB bundle change", ContentStorageDB, ContentStorageDB, newer, ContentStorageTransition{}},
		{"select S3", ContentStorageDB, ContentStorageS3, old, ContentStorageTransition{RequiresAvailability: true}},
		{"S3 unchanged", ContentStorageS3, ContentStorageS3, old, ContentStorageTransition{}},
		{"S3 bundle change", ContentStorageS3, ContentStorageS3, newer, ContentStorageTransition{RequiresAvailability: true}},
		{"S3 downgrade", ContentStorageS3, ContentStorageDB, old, ContentStorageTransition{Downgrade: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{BundleRef: tc.ref, ContentStorage: &ContentStorageProps{Type: tc.after}}
			assert.Equal(t, tc.want, CheckContentStorageTransition(tc.before, old, cfg))
		})
	}
	assert.Equal(t, ContentStorageTransition{}, CheckContentStorageTransition(ContentStorageS3, old, nil))
}

func TestExternalS3DowngradePolicy(t *testing.T) {
	assert.True(t, ExternalS3Downgrade(true, &Config{}))
	assert.True(t, ExternalS3Downgrade(true, &Config{ContentStorage: &ContentStorageProps{Type: ContentStorageDB}}))
	assert.False(t, ExternalS3Downgrade(false, &Config{}))
	assert.False(t, ExternalS3Downgrade(true, &Config{S3: &S3Config{Endpoint: "https://external.example"}}))
	assert.False(t, ExternalS3Downgrade(true, &Config{ContentStorage: &ContentStorageProps{Type: ContentStorageS3}}))
}
