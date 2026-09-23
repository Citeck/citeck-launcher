package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// A namespace's own secrets — what `${secret:<id>}` resolves to.
//
// The workspace `secrets:` section only carries DEFAULTS. The first time a
// namespace needs a value it stores its own copy, and from then on that copy is
// the value: a later edit of the default, or its removal from the workspace,
// does not reach the namespace. That is the only safe reading for a password —
// it is written into a database's data volume when the database is first
// initialized, and a service handed a changed one would simply fail to connect.
//
// The copies are SecretService rows of type NAMESPACE, so they are encrypted
// like every other secret once a master password exists, and stored as is
// before one does (see SecretService.SaveSecret) — a desktop stand must come
// up on first start without asking for a master password.

// namespaceSecretStore is the slice of *storage.SecretService this needs.
type namespaceSecretStore interface {
	ListSecrets() ([]storage.SecretMeta, error)
	GetSecret(id string) (*storage.Secret, error)
	SaveSecret(secret storage.Secret) error
	DeleteSecret(id string) error
}

// namespaceSecretScope names the namespace a row belongs to. A namespace id is
// unique only within its workspace, so both are part of it.
func namespaceSecretScope(wsID, nsID string) string {
	return "ns:" + wsID + "/" + nsID
}

// namespaceSecretRowID is the row id. It is a digest rather than the readable
// scope because the file store uses the id as a FILE NAME, and neither the
// scope's separators nor a workspace-chosen secret id are safe there. The
// readable parts live in Scope and Name.
func namespaceSecretRowID(wsID, nsID, secretID string) string {
	sum := sha256.Sum256([]byte(wsID + "\x00" + nsID + "\x00" + secretID))
	return "ns-" + hex.EncodeToString(sum[:12])
}

// loadNamespaceSecrets answers the namespace's secret values, id -> value.
//
// With seed, every workspace default the namespace has no value for yet is
// stored first; a value it already has is never touched. Without seed (a
// reload PREVIEW, which must not write) a missing value is answered with the
// default, which is exactly what seeding would store.
//
// An error means the values could not be read — in practice a desktop vault
// with a master password that has not been entered yet (storage.ErrSecretsLocked).
// The caller then generates without them: every app that references a secret
// is left out, and the start gate defers the namespace until the vault opens.
func loadNamespaceSecrets(svc namespaceSecretStore, wsID, nsID string, ws *bundle.WorkspaceConfig, seed bool) (map[string]string, error) {
	if svc == nil {
		return nil, errors.New("no secret store")
	}
	metas, err := svc.ListSecrets()
	if err != nil {
		return nil, fmt.Errorf("list namespace secrets: %w", err)
	}
	scope := namespaceSecretScope(wsID, nsID)
	out := make(map[string]string)
	for _, m := range metas {
		if !storage.IsNamespaceSecret(m) || m.Scope != scope {
			continue
		}
		sec, getErr := svc.GetSecret(m.ID)
		if getErr != nil {
			return nil, fmt.Errorf("read namespace secret %q: %w", m.Name, getErr)
		}
		out[m.Name] = sec.Value
	}
	if ws == nil {
		return out, nil
	}
	for _, def := range ws.Secrets {
		if def.ID == "" {
			continue
		}
		if _, have := out[def.ID]; have {
			continue // the namespace's own value wins, always
		}
		if seed {
			row := storage.Secret{
				SecretMeta: storage.SecretMeta{
					ID:    namespaceSecretRowID(wsID, nsID, def.ID),
					Name:  def.ID,
					Type:  storage.SecretNamespace,
					Scope: scope,
				},
				Value: def.Value,
			}
			if saveErr := svc.SaveSecret(row); saveErr != nil {
				return nil, fmt.Errorf("store namespace secret %q: %w", def.ID, saveErr)
			}
			slog.Info("Namespace secret stored from the workspace default", "ns", nsID, "secret", def.ID)
		}
		out[def.ID] = def.Value
	}
	return out, nil
}

// namespaceSecretsFor is loadNamespaceSecrets for a generation: it logs a read
// failure instead of returning it, because a namespace whose secrets cannot be
// read is still generated — without the apps that need them.
func namespaceSecretsFor(svc namespaceSecretStore, wsID, nsID string, ws *bundle.WorkspaceConfig, seed bool) map[string]string {
	values, err := loadNamespaceSecrets(svc, wsID, nsID, ws, seed)
	if err != nil {
		slog.Warn("Namespace secrets are not readable; the apps that reference them will not be generated",
			"ns", nsID, "err", err)
		return nil
	}
	return values
}

// deleteNamespaceSecrets removes every value a namespace owns. Called when the
// namespace itself is deleted; a failure is logged, never fatal — an orphan row
// is invisible and harmless, and a later namespace with the same id would find
// the old value only if the workspace default had not changed anyway.
func deleteNamespaceSecrets(svc namespaceSecretStore, wsID, nsID string) {
	if svc == nil {
		return
	}
	metas, err := svc.ListSecrets()
	if err != nil {
		slog.Warn("Failed to list namespace secrets for deletion", "ns", nsID, "err", err) //nolint:gosec // G706: nsID is a validated namespace id
		return
	}
	scope := namespaceSecretScope(wsID, nsID)
	for _, m := range metas {
		if !storage.IsNamespaceSecret(m) || m.Scope != scope {
			continue
		}
		if delErr := svc.DeleteSecret(m.ID); delErr != nil {
			slog.Warn("Failed to delete namespace secret", "ns", nsID, "secret", m.Name, "err", delErr) //nolint:gosec // G706: validated namespace id and a stored secret name
		}
	}
}
