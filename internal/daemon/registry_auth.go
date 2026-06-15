package daemon

import (
	"strings"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// Registry / git-token auth caches, rebuilt from the secret store on every
// namespace (re)load and secret mutation so credential changes apply without
// a daemon restart.

// makeTokenLookup creates a function that looks up auth tokens from the secret store.
// Tokens are pre-fetched at creation time into an immutable map for efficiency.
// Rebuilt on each reload to reflect secret mutations.
func makeTokenLookup(reader secretReader) bundle.TokenLookupFunc {
	if reader == nil {
		return func(string) string { return "" }
	}
	// Pre-fetch all secrets into a lookup map
	tokensByScope := make(map[string]string)
	secrets, err := reader.ListSecrets()
	if err == nil {
		for _, s := range secrets {
			sec, err := reader.GetSecret(s.ID)
			if err != nil {
				continue // ErrSecretsLocked → skip gracefully
			}
			if string(s.Type) != "" {
				tokensByScope[string(s.Type)] = sec.Value
			}
			if s.Scope != "" {
				tokensByScope[s.Scope] = sec.Value
			}
		}
	}
	return func(authType string) string {
		return tokensByScope[authType]
	}
}

// makeRegistryAuthFunc creates a function that returns Docker registry credentials
// by matching image host against workspace config's imageReposByHost.
// Registry secrets are pre-fetched into a map at creation time for efficiency.
// The function is rebuilt on namespace reload to reflect secret mutations.
func makeRegistryAuthFunc(wsCfg *bundle.WorkspaceConfig, reader secretReader, bindings map[string]string) namespace.RegistryAuthFunc {
	if wsCfg == nil || reader == nil {
		return nil
	}
	reposByHost := wsCfg.ImageReposByHost()

	// Pre-fetch all registry credentials into an immutable map
	authByHost := buildRegistryAuthCache(reposByHost, reader, bindings)
	if len(authByHost) == 0 {
		return nil
	}

	return func(img string) *docker.RegistryAuth {
		host := img
		if idx := strings.Index(host, "/"); idx > 0 {
			host = host[:idx]
		}
		auth, ok := authByHost[host]
		if !ok {
			return nil
		}
		return auth
	}
}

// buildRegistryAuthCache pre-fetches all registry secrets into a map keyed by host.
//
// Username and password are read via Secret.Credentials (BASIC_AUTH parity
// with Kotlin AuthSecret.Basic): the typed fields win, and a legacy
// "user:pass" packed Value is split as a last-resort fallback for any secret
// that somehow survived the FileStore / SQLite-v3 migration paths without a
// Username column populated.
func buildRegistryAuthCache(reposByHost map[string]bundle.ImageRepo, reader secretReader, bindings map[string]string) map[string]*docker.RegistryAuth {
	result := make(map[string]*docker.RegistryAuth)
	secrets, err := reader.ListSecrets()
	if err != nil {
		return result
	}
	scopeSecrets := make(map[string]*storage.Secret)
	for _, s := range secrets {
		if s.Scope != "" {
			sec, err := reader.GetSecret(s.ID)
			if err != nil {
				continue // ErrSecretsLocked → skip gracefully
			}
			scopeSecrets[s.Scope] = sec
		}
	}
	addAuth := func(host string, sec *storage.Secret) {
		username, password, ok := sec.Credentials()
		if !ok {
			return
		}
		result[host] = &docker.RegistryAuth{
			Username: username,
			Password: password,
			Registry: "https://" + host,
		}
	}
	// bindSecret resolves the explicit host → secret-id binding (the new
	// reusable model). Returns false when no binding exists or the bound
	// secret is gone/locked, so the caller falls back to scope heuristics.
	bindSecret := func(host string) bool {
		id := bindings[host]
		if id == "" {
			return false
		}
		sec, err := reader.GetSecret(id)
		if err != nil || sec == nil {
			return false
		}
		addAuth(host, sec)
		return result[host] != nil
	}
	for host, repo := range reposByHost {
		// Explicit binding wins over the legacy scope heuristics.
		if bindSecret(host) {
			continue
		}
		if repo.AuthType == "" {
			continue
		}
		sec := scopeSecrets[repo.AuthType]
		if sec == nil {
			sec = scopeSecrets[host]
		}
		if sec == nil {
			// Kotlin migration compat: scope = "images-repo:{host}"
			sec = scopeSecrets["images-repo:"+host]
		}
		if sec == nil {
			continue
		}
		addAuth(host, sec)
	}
	// Bindings for hosts the workspace config doesn't list under imageRepos
	// still take effect (e.g. a registry referenced only by a deployment def).
	for host := range bindings {
		if _, already := result[host]; already {
			continue
		}
		bindSecret(host)
	}
	// Kotlin v1.x parity: a secret with scope "images-repo:<host>" should
	// authenticate pulls from <host> even when workspace-v1.yml doesn't
	// declare that host under imageRepos. Kotlin built the secret ID from
	// the image host directly. Skipping this fallback strands migrated
	// secrets for any registry the current workspace config no longer lists
	// (e.g. enterprise-registry.citeck.ru when only harbor.citeck.ru is
	// declared) — pulls silently degrade to anonymous → 401.
	const scopePrefix = "images-repo:"
	for scope, sec := range scopeSecrets {
		host, ok := strings.CutPrefix(scope, scopePrefix)
		if !ok || host == "" {
			continue
		}
		if _, already := result[host]; already {
			continue
		}
		addAuth(host, sec)
	}
	return result
}
