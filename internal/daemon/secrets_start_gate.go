package daemon

import (
	"strings"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// namespaceNeedsUserSecrets reports whether any of the namespace's app images
// pull from a registry host the workspace marks as auth-required (an ImageRepo
// with a non-empty AuthType). Such a namespace cannot pull correctly while the
// user-secret vault is locked, so its start must wait for unlock. Public images
// (Docker Hub library refs with no host, or configured repos with no AuthType)
// never gate.
func namespaceNeedsUserSecrets(images []string, wsCfg *bundle.WorkspaceConfig) bool {
	if wsCfg == nil {
		return false
	}
	reposByHost := wsCfg.ImageReposByHost()
	if len(reposByHost) == 0 {
		return false
	}
	for _, img := range images {
		host := imageHost(img)
		if host == "" {
			continue
		}
		if repo, ok := reposByHost[host]; ok && repo.AuthType != "" {
			return true
		}
	}
	return false
}

// secretVaultState is the subset of SecretService the start gate needs
// (satisfied by *storage.SecretService).
type secretVaultState interface {
	IsEncrypted() bool
	IsLocked() bool
}

// shouldDeferStartForSecrets reports whether a namespace's auto-start must wait
// for the user to unlock the secret vault: desktop mode, an encrypted+locked
// vault, and a namespace that pulls from an auth-required registry. Returns
// false in server mode, with an unlocked/plain vault, or for a namespace that
// needs no user secrets.
func shouldDeferStartForSecrets(desktop bool, vault secretVaultState, images []string, wsCfg *bundle.WorkspaceConfig) bool {
	if !desktop || vault == nil || !vault.IsEncrypted() || !vault.IsLocked() {
		return false
	}
	return namespaceNeedsUserSecrets(images, wsCfg)
}

// imageHost returns the registry host of an image reference, or "" for a
// hostless Docker Hub library image. The first path segment is a host only when
// it looks like one (contains "." or ":"), matching bundle.ImageReposByHost's
// host derivation (substring before the first "/").
func imageHost(image string) string {
	slash := strings.Index(image, "/")
	if slash <= 0 {
		return ""
	}
	first := image[:slash]
	if strings.ContainsAny(first, ".:") {
		return first
	}
	return ""
}
