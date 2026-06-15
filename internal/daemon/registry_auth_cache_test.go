package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// fakeSecretReader implements secretReader for unit tests without touching
// disk or encryption — keeps the test focused on auth-cache routing.
type fakeSecretReader struct {
	secrets map[string]storage.Secret
}

func (f *fakeSecretReader) ListSecrets() ([]storage.SecretMeta, error) {
	out := make([]storage.SecretMeta, 0, len(f.secrets))
	for _, s := range f.secrets {
		out = append(out, s.SecretMeta)
	}
	return out, nil
}

func (f *fakeSecretReader) GetSecret(id string) (*storage.Secret, error) {
	s, ok := f.secrets[id]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

// TestBuildRegistryAuthCache_TypedBasic: a secret with typed Username + a
// password containing ':' must round-trip into RegistryAuth without
// truncation (the bug this issue fixes).
func TestBuildRegistryAuthCache_TypedBasic(t *testing.T) {
	host := "registry.example.com"
	reposByHost := map[string]bundle.ImageRepo{
		host: {ID: "private", URL: host, AuthType: "BASIC"},
	}
	reader := &fakeSecretReader{secrets: map[string]storage.Secret{
		"registry-private": {
			SecretMeta: storage.SecretMeta{
				ID:       "registry-private",
				Type:     storage.SecretRegistryAuth,
				Scope:    host,
				Username: "alice",
			},
			Value: "pa:ss:wo:rd",
		},
	}}
	got := buildRegistryAuthCache(reposByHost, reader, nil)
	auth, ok := got[host]
	if !ok || auth == nil {
		t.Fatal("expected RegistryAuth for host, got nil")
	}
	if auth.Username != "alice" {
		t.Errorf("Username = %q, want 'alice'", auth.Username)
	}
	if auth.Password != "pa:ss:wo:rd" {
		t.Errorf("Password = %q, want full 'pa:ss:wo:rd' (no truncation)", auth.Password)
	}
}

// TestBuildRegistryAuthCache_BindingWins: an explicit host→secret binding
// resolves the credential by id and takes precedence over the scope
// heuristics (the reusable-secret model — pick once, reuse everywhere).
func TestBuildRegistryAuthCache_BindingWins(t *testing.T) {
	host := "enterprise-registry.citeck.ru"
	reposByHost := map[string]bundle.ImageRepo{
		host: {ID: "enterprise", URL: host, AuthType: "BASIC"},
	}
	reader := &fakeSecretReader{secrets: map[string]storage.Secret{
		"bound": {
			SecretMeta: storage.SecretMeta{ID: "bound", Type: storage.SecretRegistryAuth, Username: "svc"},
			Value:      "pw",
		},
		// A scope-matching secret that MUST be ignored because the binding wins.
		"scoped": {
			SecretMeta: storage.SecretMeta{ID: "scoped", Type: storage.SecretRegistryAuth, Scope: "images-repo:" + host, Username: "other"},
			Value:      "nope",
		},
	}}
	got := buildRegistryAuthCache(reposByHost, reader, map[string]string{host: "bound"})
	auth := got[host]
	if auth == nil {
		t.Fatal("expected RegistryAuth from binding, got nil")
	}
	if auth.Username != "svc" || auth.Password != "pw" {
		t.Errorf("got (%q,%q), want ('svc','pw') from the bound secret", auth.Username, auth.Password)
	}
}

// TestBuildRegistryAuthCache_BindingWithoutImageRepo: a binding takes effect
// even when the workspace config doesn't list the host under imageRepos.
func TestBuildRegistryAuthCache_BindingWithoutImageRepo(t *testing.T) {
	host := "extra-registry.citeck.ru"
	reader := &fakeSecretReader{secrets: map[string]storage.Secret{
		"s": {SecretMeta: storage.SecretMeta{ID: "s", Type: storage.SecretRegistryAuth, Username: "u"}, Value: "p"},
	}}
	got := buildRegistryAuthCache(map[string]bundle.ImageRepo{}, reader, map[string]string{host: "s"})
	auth := got[host]
	if auth == nil {
		t.Fatal("expected RegistryAuth for a binding host absent from imageRepos, got nil")
	}
	if auth.Username != "u" || auth.Password != "p" {
		t.Errorf("got (%q,%q), want ('u','p')", auth.Username, auth.Password)
	}
}

// TestBuildRegistryAuthCache_LegacyPackedValue: a secret with empty Username
// and "user:pass" in Value is split as a backward-compat fallback. Used by
// secrets that somehow survived the storage-layer migration without a
// populated Username column.
func TestBuildRegistryAuthCache_LegacyPackedValue(t *testing.T) {
	host := "registry.example.com"
	reposByHost := map[string]bundle.ImageRepo{
		host: {ID: "private", URL: host, AuthType: "BASIC"},
	}
	reader := &fakeSecretReader{secrets: map[string]storage.Secret{
		"registry-private": {
			SecretMeta: storage.SecretMeta{
				ID:    "registry-private",
				Type:  storage.SecretRegistryAuth,
				Scope: host,
			},
			Value: "alice:simple",
		},
	}}
	got := buildRegistryAuthCache(reposByHost, reader, nil)
	auth := got[host]
	if auth == nil {
		t.Fatal("expected RegistryAuth, got nil")
	}
	if auth.Username != "alice" || auth.Password != "simple" {
		t.Errorf("got (%q,%q), want ('alice','simple')", auth.Username, auth.Password)
	}
}

// TestBuildRegistryAuthCache_KotlinMigrationScope: secrets imported from the
// Kotlin H2 launcher are scoped as "images-repo:{host}"; the cache must look
// up by that compatibility scope when the workspace-config AuthType doesn't
// match.
func TestBuildRegistryAuthCache_KotlinMigrationScope(t *testing.T) {
	host := "harbor.citeck.ru"
	reposByHost := map[string]bundle.ImageRepo{
		host: {ID: "harbor", URL: host, AuthType: "BASIC"},
	}
	reader := &fakeSecretReader{secrets: map[string]storage.Secret{
		"images-repo:" + host: {
			SecretMeta: storage.SecretMeta{
				ID:       "images-repo:" + host,
				Type:     storage.SecretRegistryAuth,
				Scope:    "images-repo:" + host,
				Username: "harbor-user",
			},
			Value: "harbor:p:w",
		},
	}}
	got := buildRegistryAuthCache(reposByHost, reader, nil)
	auth := got[host]
	if auth == nil {
		t.Fatal("expected RegistryAuth via images-repo scope, got nil")
	}
	if auth.Username != "harbor-user" || auth.Password != "harbor:p:w" {
		t.Errorf("got (%q,%q), want ('harbor-user','harbor:p:w')", auth.Username, auth.Password)
	}
}
