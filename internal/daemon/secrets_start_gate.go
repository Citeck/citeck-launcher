package daemon

import (
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
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
		host := imageRegistryHost(img)
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
// for the user to hand over the master password: desktop mode, a vault the user
// cannot read from yet, and a namespace that pulls from an auth-required
// registry. Returns false in server mode, with a readable vault, or for a
// namespace that needs no user secrets.
//
// There are TWO ways the vault can be unusable, and keying only on the first is
// what let the reported bug through:
//
//   - encrypted && locked — the steady state, a custom master password not yet
//     entered this session.
//   - pendingKotlinImport — the state that exists ONLY on the first boot after a
//     1.x → 2.x migration: the migrated secrets are still one opaque blob
//     waiting for the master password, so SecretService is neither encrypted NOR
//     locked, it is EMPTY. `encrypted && locked` is false, the namespace
//     auto-started, its private-registry pulls failed auth, and the
//     registry-credentials dialog opened on TOP of the master-password prompt —
//     a dead end, since picking a secret needs a vault that does not exist yet
//     and saving a token answers 423.
//
// A pending import gates on its own, without consulting the vault: at that
// point there is nothing in it to consult.
func shouldDeferStartForSecrets(desktop bool, vault secretVaultState, pendingKotlinImport bool,
	images []string, wsCfg *bundle.WorkspaceConfig,
) bool {
	if !desktop {
		return false
	}
	lockedOut := vault != nil && vault.IsEncrypted() && vault.IsLocked()
	if !lockedOut && !pendingKotlinImport {
		return false
	}
	// A locked vault also hides the namespace's OWN secret values, so the apps
	// that reference them were generated out. Starting now would run the stand
	// without them; waiting lets the unlock regenerate with them in.
	// A pending Kotlin import does not count here: that vault is unencrypted
	// and empty, and namespace secrets are stored in it as is.
	if lockedOut && namespaceDeclaresSecrets(wsCfg) {
		return true
	}
	return namespaceNeedsUserSecrets(images, wsCfg)
}

// namespaceDeclaresSecrets reports whether the workspace gives its namespaces
// secret values (the `secrets:` section) — the apps referencing them cannot be
// generated while the vault that holds the namespace's copies is locked.
func namespaceDeclaresSecrets(wsCfg *bundle.WorkspaceConfig) bool {
	return wsCfg != nil && len(wsCfg.Secrets) > 0
}

// hasPendingKotlinSecrets reports whether a migrated 1.x secrets blob is still
// waiting for the master password. While it is, the user's registry credentials
// exist on disk but are unreadable — the same practical state as a locked vault,
// and the reason the start gate cannot key on encrypted+locked alone.
func hasPendingKotlinSecrets(store storage.Store) bool {
	if store == nil {
		return false
	}
	blob, err := store.GetSecretBlob()
	return err == nil && blob != ""
}
