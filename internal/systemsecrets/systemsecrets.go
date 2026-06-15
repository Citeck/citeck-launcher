// Package systemsecrets owns the `_sys_*` launcher_state key scheme for
// system-managed secrets (JWT, OIDC client secret, admin password, citeck SA).
//
// System secrets are plain (unencrypted) in launcher_state — they only protect
// the local machine itself (Kotlin v1.x parity: JWT was a hardcoded constant,
// OIDC was hardcoded in realm.json, KK admin password was "admin"). Their
// secrecy adds nothing on a developer workstation, and on a server they're
// already constrained by the binary's own filesystem permissions. The
// SecretService keeps holding USER-added auth secrets (Harbor / nexus / git
// tokens) where encryption matters — those reach external resources.
//
// Why a dedicated package: two HIGH-severity bugs (silently-reverting admin
// password rotation, install wizard printing a stale default) came from call
// sites hand-rolling the storage location instead of going through one
// resolver. Every read/write of a system secret MUST go through Get/Set/Lookup
// here so the priority chain and key scheme can never drift between the
// daemon, the rotation handler, and the CLI. The package depends only on
// internal/storage + internal/config so both daemon and cli can import it.
package systemsecrets

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// Canonical system-secret ids. The daemon resolves exactly these four at
// startup (resolveSystemSecrets); `citeck diagnose --secrets` audits the same
// list. Adding a new system secret means adding its id here so every consumer
// (resolver, rotation, doctor) picks it up from one place.
const (
	// IDJWT is the webapp HMAC-SHA256 signing secret (standard base64).
	IDJWT = "_jwt"
	// IDOIDC is the Keycloak ecos-app realm OIDC client secret.
	IDOIDC = "_oidc"
	// IDAdminPassword is the human-administrator password seeded into
	// Keycloak (master + ecos-app), RabbitMQ, and PgAdmin.
	IDAdminPassword = "_admin_password" //nolint:gosec // G101: id constant, not a credential
	// IDCiteckSA is the stable `citeck` service-account password (Keycloak
	// master admin role + RabbitMQ monitoring) — never rotated with the
	// admin password.
	IDCiteckSA = "_citeck_sa"
)

// KnownIDs returns every system-secret id the launcher manages, in the order
// resolveSystemSecrets resolves them. Returned as a fresh slice so callers
// can't mutate the canonical list.
func KnownIDs() []string {
	return []string{IDJWT, IDOIDC, IDAdminPassword, IDCiteckSA}
}

// Key returns the launcher_state key for a system secret id.
// Keeps the `_sys_` prefix as the source-of-truth namespace for plain
// system values so they never collide with SecretService-managed user secrets.
func Key(id string) string {
	return "_sys" + id // e.g. "_jwt" → "_sys_jwt"
}

// PlainFilePath returns the pre-Store launcher's plain-file location for a
// system secret id: conf/<id-without-underscore>-secret. Level 3 of the
// resolution chain; only ever read (and deleted after migration), never
// written by current code.
func PlainFilePath(id string) string {
	return filepath.Join(config.ConfDir(), strings.TrimPrefix(id, "_")+"-secret")
}

// Get returns a system secret value, sourcing it in this priority order and
// migrating older locations into the new plain state:
//  1. launcher_state plain (new home — `_sys<id>`).
//  2. SecretService SYSTEM row (older installs); migrate to plain + delete.
//  3. conf/secrets/<id-without-underscore>-secret plain file (pre-Store
//     launcher); migrate to plain + delete.
//  4. Generate fresh.
func Get(store storage.Store, svc *storage.SecretService, id string, generate func() string) (string, error) {
	stateKey := Key(id)

	// 1. Try launcher_state plain
	if v, err := store.GetStateValue(stateKey); err == nil && v != "" {
		return v, nil
	}

	// 2. Fallback: SecretService SYSTEM row — migrate to plain + delete.
	if sec, err := svc.GetSecret(id); err == nil && sec.Value != "" {
		value := sec.Value
		if id == IDJWT {
			value = migrateJWTSecretToStdBase64(value)
		}
		if saveErr := store.SetStateValue(stateKey, value); saveErr != nil {
			return "", fmt.Errorf("save migrated secret %s: %w", id, saveErr)
		}
		slog.Info("Migrated SYSTEM secret out of SecretService into plain state", "id", id)
		if delErr := svc.DeleteSecret(id); delErr != nil {
			slog.Warn("Failed to delete migrated SYSTEM secret from SecretService", "id", id, "err", delErr)
		}
		return value, nil
	}

	// 3. Fallback: read plain file (pre-Store launcher migration).
	plainFile := PlainFilePath(id)
	if data, readErr := os.ReadFile(plainFile); readErr == nil && len(data) > 0 { //nolint:gosec // path from trusted confDir
		value := string(data)
		if id == IDJWT {
			value = migrateJWTSecretToStdBase64(value)
		}
		slog.Info("Migrating system secret from plain file to launcher_state", "id", id)
		if saveErr := store.SetStateValue(stateKey, value); saveErr != nil {
			return "", fmt.Errorf("save migrated secret %s: %w", id, saveErr)
		}
		_ = os.Remove(plainFile)
		return value, nil
	}

	// 4. Generate new.
	value := generate()
	if value == "" {
		return "", fmt.Errorf("failed to generate secret %s", id)
	}
	slog.Info("Generated new system secret", "id", id)
	if saveErr := store.SetStateValue(stateKey, value); saveErr != nil {
		return "", fmt.Errorf("save generated secret %s: %w", id, saveErr)
	}
	return value, nil
}

// Set persists a new value where Get actually reads FIRST: the plain
// `_sys<id>` launcher_state key. Persisting only a SecretService row used to
// silently revert the admin-password rotation — the stale state value won on
// restart and the Keycloak init script re-applied it on every container
// start. After a successful persist, any legacy SecretService row for the
// same id is best-effort deleted so the priority-2 fallback can never
// resurrect a stale value once the state key is (ever) cleared.
//
// When the state write itself fails, the legacy row is deliberately NOT
// deleted: it may be the only persisted copy left (even a stale one beats a
// freshly-generated mismatch on next start), and the caller gets the error to
// surface.
func Set(store storage.Store, svc *storage.SecretService, id, value string) error {
	if err := store.SetStateValue(Key(id), value); err != nil {
		return fmt.Errorf("persist system secret %s: %w", id, err)
	}
	if svc != nil {
		if legacy, err := svc.GetSecret(id); err == nil && legacy.Value != "" {
			if delErr := svc.DeleteSecret(id); delErr != nil {
				slog.Warn("Failed to delete legacy SecretService row after system secret update", "id", id, "err", delErr)
			}
		}
	}
	return nil
}

// Source identifies one of the three homes a system secret can live in.
type Source string

const (
	// SourceState is the plain launcher_state `_sys_*` key — the current
	// home and resolution priority 1.
	SourceState Source = "launcher_state"
	// SourceSecretService is a legacy encrypted SecretService row —
	// priority 2; migrated out by Get on first daemon read.
	SourceSecretService Source = "secret_service"
	// SourcePlainFile is the pre-Store conf/<id>-secret plain file —
	// priority 3; migrated out by Get on first daemon read.
	SourcePlainFile Source = "plain_file"
)

// Location is one home's view of a system secret, captured by Inspect.
type Location struct {
	Source Source
	Value  string
	// CreatedAt is the best-available timestamp: SecretService row
	// CreatedAt or plain-file mtime. Zero for launcher_state (the
	// key-value store keeps no metadata).
	CreatedAt time.Time
	// Detail is a human-readable pointer to the concrete slot: the state
	// key, the SecretService row id, or the plain file path.
	Detail string
}

// Inspect reports every home currently holding a value for id, ordered by
// resolution priority (the first entry is what Get would return). Strictly
// read-only — unlike Get it never migrates, deletes, or generates, which is
// what makes it safe for `citeck diagnose --secrets` and `--show` to run
// against a live install. A locked SecretService simply contributes nothing
// (GetSecret errors are treated as absence — callers that care report the
// lock state separately via svc.IsLocked).
func Inspect(store storage.Store, svc *storage.SecretService, id string) []Location {
	locs := make([]Location, 0, 3)
	if store != nil {
		if v, err := store.GetStateValue(Key(id)); err == nil && v != "" {
			locs = append(locs, Location{Source: SourceState, Value: v, Detail: Key(id)})
		}
	}
	if svc != nil {
		if sec, err := svc.GetSecret(id); err == nil && sec != nil && sec.Value != "" {
			locs = append(locs, Location{Source: SourceSecretService, Value: sec.Value, CreatedAt: sec.CreatedAt, Detail: id})
		}
	}
	plainFile := PlainFilePath(id)
	if data, err := os.ReadFile(plainFile); err == nil && len(data) > 0 { //nolint:gosec // path from trusted confDir
		loc := Location{Source: SourcePlainFile, Value: string(data), Detail: plainFile}
		if fi, statErr := os.Stat(plainFile); statErr == nil {
			loc.CreatedAt = fi.ModTime()
		}
		locs = append(locs, loc)
	}
	return locs
}

// Lookup returns the effective value for id — the same answer Get would give
// for an existing value — without mutating anything (no migration, no
// generation). ok is false when no home holds a value (a fresh install where
// the daemon hasn't generated secrets yet).
func Lookup(store storage.Store, svc *storage.SecretService, id string) (loc Location, ok bool) {
	locs := Inspect(store, svc, id)
	if len(locs) == 0 {
		return Location{}, false
	}
	return locs[0], true
}

// OpenDefaultUnlocked builds a SecretService over store and unlocks it with
// the default master password when applicable (the common case: encryption
// was initialized by the daemon with the default password). A custom master
// password leaves the service locked — read-only callers (Inspect/Lookup)
// then simply skip the SecretService level, mirroring what the install
// wizard has always done. Error only on construction failure.
func OpenDefaultUnlocked(store storage.Store) (*storage.SecretService, error) {
	svc, err := storage.NewSecretService(store)
	if err != nil {
		return nil, fmt.Errorf("secret service: %w", err)
	}
	if svc.IsEncrypted() && svc.IsDefaultPassword() {
		if unlockErr := svc.Unlock(storage.DefaultMasterPassword); unlockErr != nil {
			// Stays locked; callers degrade to state+file-only inspection.
			slog.Debug("Failed to unlock SecretService with default master password", "err", unlockErr)
		}
	}
	return svc, nil
}

// migrateJWTSecretToStdBase64 ensures the JWT secret uses standard base64 encoding.
// Old launcher versions used RawURLEncoding (no padding, URL-safe alphabet). If detected,
// the secret is re-encoded to StdEncoding. The caller persists the corrected value.
func migrateJWTSecretToStdBase64(stored string) string {
	if _, err := base64.StdEncoding.DecodeString(stored); err == nil {
		return stored // already standard base64
	}
	raw, err := base64.RawURLEncoding.DecodeString(stored)
	if err != nil {
		slog.Warn("JWT secret is not valid base64, keeping as-is", "err", err)
		return stored
	}
	slog.Info("Migrated JWT secret from RawURLEncoding to StdEncoding")
	return base64.StdEncoding.EncodeToString(raw)
}
