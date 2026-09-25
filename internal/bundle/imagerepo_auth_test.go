package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// One harbor host serving an auth-required project and a public one — the
// shape the public workspace's observer (harbor.citeck.ru/public) needs beside
// its enterprise images (harbor.citeck.ru/enterprise).
func sharedHostWorkspace(publicFirst bool) *WorkspaceConfig {
	ent := ImageRepo{ID: "enterprise", URL: "harbor.citeck.ru/enterprise", AuthType: "BASIC"}
	pub := ImageRepo{ID: "public", URL: "harbor.citeck.ru/public"}
	core := ImageRepo{ID: "core", URL: "nexus.citeck.ru"}
	if publicFirst {
		return &WorkspaceConfig{ImageRepos: []ImageRepo{core, pub, ent}}
	}
	return &WorkspaceConfig{ImageRepos: []ImageRepo{core, ent, pub}}
}

// Credentials are per host, so the host must keep the repo that carries them
// whichever order the repos are declared in. Last-writer-wins handed the host
// to the auth-free repo declared after the auth one, and the enterprise images
// lost their credentials.
func TestImageReposByHostKeepsTheAuthRepoOfASharedHost(t *testing.T) {
	for _, publicFirst := range []bool{false, true} {
		got := sharedHostWorkspace(publicFirst).ImageReposByHost()
		assert.Equal(t, "enterprise", got["harbor.citeck.ru"].ID, "publicFirst=%v", publicFirst)
		assert.Equal(t, "core", got["nexus.citeck.ru"].ID)
	}
}

func TestRepoForImagePicksTheLongestPrefixOnAPathBoundary(t *testing.T) {
	w := &WorkspaceConfig{ImageRepos: []ImageRepo{
		{ID: "host", URL: "harbor.citeck.ru"},
		{ID: "pub", URL: "harbor.citeck.ru/pub"},
		{ID: "public", URL: "harbor.citeck.ru/public/"},
	}}
	cases := map[string]string{
		"harbor.citeck.ru/public/citeck-observer:v1.5.2": "public", // trailing '/' on the URL is ignored
		"harbor.citeck.ru/pub/x:1":                       "pub",
		"harbor.citeck.ru/publicity/x:1":                 "host", // "pub"/"public" are not path prefixes of it
		"harbor.citeck.ru/community/stt-sidecar:1.0.0":   "host",
	}
	for img, want := range cases {
		repo, ok := w.RepoForImage(img)
		assert.True(t, ok, img)
		assert.Equal(t, want, repo.ID, img)
	}
	_, ok := w.RepoForImage("postgres:17.5")
	assert.False(t, ok)
}

func TestImageNeedsAuth(t *testing.T) {
	shared := sharedHostWorkspace(false)
	cases := map[string]bool{
		"harbor.citeck.ru/enterprise/citeck-rag:1.2.2":   true,
		"harbor.citeck.ru/public/citeck-observer:v1.5.2": false, // declared public: anonymous pull is fine
		"harbor.citeck.ru/community/stt-sidecar:1.0.0":   true,  // under no declared URL: the host decides
		"nexus.citeck.ru/ecos-model:2.44.0":              false,
		"postgres:17.5":                                  false,
		"qdrant/qdrant:v1.19.1":                          false,
	}
	for img, want := range cases {
		assert.Equal(t, want, shared.ImageNeedsAuth(img), img)
	}

	// Without the public repo declared, the public path keeps the rule every
	// earlier launcher applied: its host needs auth, so it does too.
	enterpriseOnly := &WorkspaceConfig{ImageRepos: []ImageRepo{
		{ID: "core", URL: "nexus.citeck.ru"},
		{ID: "enterprise", URL: "harbor.citeck.ru/enterprise", AuthType: "BASIC"},
	}}
	assert.True(t, enterpriseOnly.ImageNeedsAuth("harbor.citeck.ru/public/citeck-observer:v1.5.2"))

	var none *WorkspaceConfig
	assert.False(t, none.ImageNeedsAuth("harbor.citeck.ru/enterprise/x:1"))
}
