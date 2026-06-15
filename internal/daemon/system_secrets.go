package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
)

// resolveSystemSecrets reads or generates JWT, OIDC, admin, and citeck-SA values.
//
// The storage scheme (plain `_sys_*` launcher_state keys, the 4-level
// priority/migration chain, why system secrets are NOT in the encrypted
// SecretService) is owned by internal/systemsecrets — see that package's doc.
// This function only supplies the daemon-specific generate policies and the
// desktop-mode defaults; every read/write goes through systemsecrets.Get so
// the daemon can never drift from the rotation handler or the CLI.
//
// In desktop mode the admin password is always "admin" and citeck SA defaults
// to "citeck" — the Kotlin v1.x developer-tool convention.
func resolveSystemSecrets(store storage.Store, svc *storage.SecretService, desktop bool) (namespace.SystemSecrets, error) {
	var secrets namespace.SystemSecrets

	// JWT
	jwt, err := systemsecrets.Get(store, svc, systemsecrets.IDJWT, func() string {
		b := make([]byte, 64)
		if _, err := rand.Read(b); err != nil {
			slog.Error("Failed to generate JWT secret", "err", err)
			return ""
		}
		return base64.StdEncoding.EncodeToString(b)
	})
	if err != nil {
		return secrets, fmt.Errorf("resolve JWT secret: %w", err)
	}
	secrets.JWT = jwt

	// OIDC
	oidc, err := systemsecrets.Get(store, svc, systemsecrets.IDOIDC, func() string {
		b := make([]byte, 32)
		if _, randErr := rand.Read(b); randErr != nil {
			slog.Error("Failed to generate OIDC secret", "err", randErr)
			return ""
		}
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	})
	if err != nil {
		return secrets, fmt.Errorf("resolve OIDC secret: %w", err)
	}
	secrets.OIDC = oidc

	// ecos-app realm admin password.
	if desktop {
		secrets.AdminPassword = "admin"
	} else {
		adminPass, adminErr := systemsecrets.Get(store, svc, systemsecrets.IDAdminPassword, func() string {
			p, genErr := namespace.GenerateSimpleAdminPassword()
			if genErr != nil {
				slog.Error("Failed to generate admin password", "err", genErr)
				return ""
			}
			return p
		})
		if adminErr != nil {
			return secrets, fmt.Errorf("resolve admin password: %w", adminErr)
		}
		secrets.AdminPassword = adminPass
	}

	// citeck SA password. Desktop default is "citeck" (username = password
	// convenience on a dev workstation), server keeps a 32-char random.
	citeckSA, saErr := systemsecrets.Get(store, svc, systemsecrets.IDCiteckSA, func() string {
		if legacy, err := svc.GetSecret("_launcher_sa"); err == nil && legacy.Value != "" {
			slog.Info("Migrating legacy _launcher_sa secret to _citeck_sa")
			return legacy.Value
		}
		if desktop {
			return "citeck"
		}
		p, genErr := namespace.GenerateCiteckSAPassword()
		if genErr != nil {
			slog.Error("Failed to generate citeck SA password", "err", genErr)
			return ""
		}
		return p
	})
	if saErr != nil {
		return secrets, fmt.Errorf("resolve citeck SA: %w", saErr)
	}
	if citeckSA == "" {
		return secrets, fmt.Errorf("citeck SA password is empty (generation failed)")
	}
	secrets.CiteckSA = citeckSA

	// Legacy cleanup: delete _launcher_sa from BOTH the new plain state and
	// the SecretService once migration produced a fresh _citeck_sa. Errors
	// non-fatal — migration already succeeded.
	_ = store.SetStateValue(systemsecrets.Key("_launcher_sa"), "")
	if legacy, err := svc.GetSecret("_launcher_sa"); err == nil && legacy.Value != "" {
		if delErr := svc.DeleteSecret("_launcher_sa"); delErr != nil {
			slog.Warn("Failed to delete legacy _launcher_sa secret after migration", "err", delErr)
		} else {
			slog.Info("Deleted legacy _launcher_sa secret after migration to _citeck_sa")
		}
	}

	return secrets, nil
}
