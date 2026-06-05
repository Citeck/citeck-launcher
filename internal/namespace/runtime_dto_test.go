package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
)

func TestResolveDisplayBundleRef(t *testing.T) {
	cached := &bundle.Def{Key: bundle.Key{Version: "2026.3-RC1"}}

	// LATEST + cached resolved bundle → show the concrete version.
	assert.Equal(t, "develop:2026.3-RC1",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "LATEST"}, cached))

	// LATEST is matched case-insensitively.
	assert.Equal(t, "develop:2026.3-RC1",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "latest"}, cached))

	// LATEST with no cached bundle → raw ref unchanged (graceful fallback).
	assert.Equal(t, "develop:LATEST",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "LATEST"}, nil))

	// LATEST with an empty cached version → raw ref unchanged.
	assert.Equal(t, "develop:LATEST",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "LATEST"}, &bundle.Def{}))

	// A concrete key is shown as-is, even when a cached bundle is present.
	assert.Equal(t, "develop:2025.12",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "2025.12"}, cached))
}
