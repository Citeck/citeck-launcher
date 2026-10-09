package daemon

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// A namespace's own secrets — what `${secret:<id>}` resolves to.
//
// The workspace `secrets:` section only says how a namespace gets its FIRST
// value: a constant default to copy, or a value to generate. The first time a
// namespace needs a value it stores its own, and from then on that copy is the
// value: a later edit of the default, its removal from the workspace, or a
// constant turned into a generated entry (or back) does not reach the
// namespace, and a generated value is never generated again. That is the only
// safe reading for a password — it is written into a database's data volume
// when the database is first initialized, and a service handed a changed one
// would simply fail to connect. A generated value is therefore one per
// NAMESPACE: only an explicit snapshot restore carries it to another one.
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
	return storage.NamespaceSecretScope(wsID, nsID)
}

// namespaceSecretRowID is the row id. It is a digest rather than the readable
// scope because the file store uses the id as a FILE NAME, and neither the
// scope's separators nor a workspace-chosen secret id are safe there. The
// readable parts live in Scope and Name.
func namespaceSecretRowID(wsID, nsID, secretID string) string {
	return storage.NamespaceSecretID(wsID, nsID, secretID)
}

// namespaceSecretMode says what loading a namespace's secrets may do. Three
// callers want three different things, and a boolean "seed or not" could not
// say the difference between the last two once a value can be GENERATED:
// what seeding would store is no longer something a reader can know.
type namespaceSecretMode int

const (
	// nsSecretsRead stores nothing and invents nothing. A constant with no
	// stored value answers its workspace default (exactly what seeding would
	// store, whoever asks); a generated one answers nothing at all, so
	// `${secret:<id>}` stays unresolved and whatever needs it is left out.
	// For callers that act on the values after a generation has already
	// seeded them, such as the dependency migration's temp containers. A key
	// nobody stored is a key no container was started with.
	//
	// The zero value on purpose: a caller that forgets to choose cannot write.
	nsSecretsRead namespaceSecretMode = iota
	// nsSecretsSeed stores every value the namespace does not have yet — a
	// constant's default, or a freshly generated value — and answers them. The
	// load and reload paths, i.e. the generations whose containers are started.
	nsSecretsSeed
	// nsSecretsPreview stores nothing and answers a generated secret the
	// namespace does not have yet with previewGeneratedSecret, so a reload
	// PLAN shows the app that will receive it (as a change) instead of
	// reporting it excluded. The placeholder never reaches a store or a
	// container.
	nsSecretsPreview
)

// previewGeneratedSecret stands in, in a reload plan, for a value the real
// reload will generate. Readable on purpose: the plan's diff prints env lines.
const previewGeneratedSecret = "<generated on reload>"

// nsSecretSeedMu serializes seeding. Generating is not idempotent — two
// concurrent seeds of one namespace would each generate a value and the second
// write would replace the value the first generation already handed to its
// containers — so the read-then-store happens under one lock.
var nsSecretSeedMu sync.Mutex

// loadNamespaceSecrets answers the namespace's secret values, id -> value.
// What it may store and what it answers for a value the namespace does not
// have yet depends on mode; a value the namespace HAS is answered as stored in
// every mode and never touched — with one exception below.
//
// A generated secret whose stored value is EMPTY is treated as missing: an
// empty key is a service with authentication switched off (SeaweedFS reads an
// empty S3 secret key or JWT signing key as "no auth"). Seeding replaces it,
// with a warning; reading answers nothing; a preview answers the placeholder.
// A constant's empty value is a value, as it always was.
//
// An error means the values could not be read — in practice a desktop vault
// with a master password that has not been entered yet (storage.ErrSecretsLocked),
// including a seed that has to WRITE a value into it. The caller then
// generates without them: every app that references a secret is left out, and
// the start gate defers the namespace until the vault opens.
func loadNamespaceSecrets(svc namespaceSecretStore, wsID, nsID string, ws *bundle.WorkspaceConfig, mode namespaceSecretMode) (map[string]string, error) {
	if svc == nil {
		return nil, errors.New("no secret store")
	}
	if mode == nsSecretsSeed {
		nsSecretSeedMu.Lock()
		defer nsSecretSeedMu.Unlock()
	}
	metas, err := svc.ListSecrets()
	if err != nil {
		return nil, fmt.Errorf("list namespace secrets: %w", err)
	}
	scope := namespaceSecretScope(wsID, nsID)
	out := make(map[string]string)
	rowIDs := make(map[string]string)
	for _, m := range metas {
		if !storage.IsNamespaceSecret(m) || m.Scope != scope {
			continue
		}
		sec, getErr := svc.GetSecret(m.ID)
		if getErr != nil {
			return nil, fmt.Errorf("read namespace secret %q: %w", m.Name, getErr)
		}
		out[m.Name] = sec.Value
		rowIDs[m.Name] = m.ID
	}
	if ws == nil {
		return out, nil
	}
	decided := make(map[string]bool, len(ws.Secrets))
	for _, def := range ws.Secrets {
		if def.ID == "" || decided[def.ID] {
			continue // the first entry for an id is the one that counts
		}
		decided[def.ID] = true
		stored, have := out[def.ID]
		if def.Generated {
			if have && stored != "" {
				continue // the namespace's own value wins, always
			}
			value, gerr := generatedNamespaceSecret(svc, mode, scope, rowIDs[def.ID], wsID, nsID, def, have)
			if gerr != nil {
				return nil, gerr
			}
			if value == "" {
				delete(out, def.ID) // reading: missing stays missing
				continue
			}
			out[def.ID] = value
			continue
		}
		if have {
			continue // the namespace's own value wins, always
		}
		if mode == nsSecretsSeed {
			row := namespaceSecretRow(wsID, nsID, scope, "", def.ID, def.Value)
			if saveErr := svc.SaveSecret(row); saveErr != nil {
				return nil, fmt.Errorf("store namespace secret %q: %w", def.ID, saveErr)
			}
			slog.Info("Namespace secret stored from the workspace default", "ns", nsID, "secret", def.ID)
		}
		out[def.ID] = def.Value
	}
	return out, nil
}

// generatedNamespaceSecret answers the value of a generated secret the
// namespace has no usable value for (none stored, or an empty one): a fresh
// one, stored, when seeding; the placeholder in a preview; "" when reading.
func generatedNamespaceSecret(svc namespaceSecretStore, mode namespaceSecretMode, scope, rowID, wsID, nsID string, def bundle.SecretDefault, wasEmpty bool) (string, error) {
	switch mode {
	case nsSecretsPreview:
		return previewGeneratedSecret, nil
	case nsSecretsSeed:
	default:
		return "", nil
	}
	value, err := generateSecretValue(def.GeneratedLength())
	if err != nil {
		return "", fmt.Errorf("generate namespace secret %q: %w", def.ID, err)
	}
	if saveErr := svc.SaveSecret(namespaceSecretRow(wsID, nsID, scope, rowID, def.ID, value)); saveErr != nil {
		return "", fmt.Errorf("store namespace secret %q: %w", def.ID, saveErr)
	}
	if wasEmpty {
		slog.Warn("Namespace secret had an empty value; generated a new one, since an empty key switches the service's authentication off",
			"ns", nsID, "secret", def.ID)
	} else {
		slog.Info("Namespace secret generated", "ns", nsID, "secret", def.ID)
	}
	return value, nil
}

// namespaceSecretRow builds the stored row for one value. rowID is the id of
// the row being replaced, "" for a new one.
func namespaceSecretRow(wsID, nsID, scope, rowID, secretID, value string) storage.Secret {
	if rowID == "" {
		rowID = namespaceSecretRowID(wsID, nsID, secretID)
	}
	return storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:    rowID,
			Name:  secretID,
			Type:  storage.SecretNamespace,
			Scope: scope,
		},
		Value: value,
	}
}

// secretValueAlphabet is what a generated value is drawn from: [A-Za-z0-9],
// safe unquoted in an env var, a URL, a YAML scalar and a shell word.
const secretValueAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// generateSecretValue returns length characters drawn uniformly from
// secretValueAlphabet by crypto/rand.
func generateSecretValue(length int) (string, error) {
	return generateSecretValueFrom(rand.Reader, length)
}

// generateSecretValueFrom draws from r. UNIFORMLY: a random byte is used only
// when it is below the largest multiple of the alphabet size that fits in a
// byte (248 = 4×62) and is discarded otherwise. Taking every byte modulo 62
// would map the 8 bytes 248..255 onto the first 8 characters and make each of
// them 25% likelier than the rest.
func generateSecretValueFrom(r io.Reader, length int) (string, error) {
	n := len(secretValueAlphabet)
	limit := 256 - 256%n
	out := make([]byte, 0, length)
	buf := make([]byte, length+length/4+8)
	for len(out) < length {
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("read random bytes: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, secretValueAlphabet[int(b)%n])
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}

// namespaceSecretsFor is loadNamespaceSecrets for a generation: it logs a read
// failure instead of returning it, because a namespace whose secrets cannot be
// read is still generated — without the apps that need them.
func namespaceSecretsFor(svc namespaceSecretStore, wsID, nsID string, ws *bundle.WorkspaceConfig, mode namespaceSecretMode) map[string]string {
	values, err := loadNamespaceSecrets(svc, wsID, nsID, ws, mode)
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
