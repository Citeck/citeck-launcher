package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
)

const secretsFileName = "secrets.json"
const maxSecretsSize = 8 << 20

// SecretStore transfers portable name/value pairs, never vault ciphertext or
// source namespace IDs. Preparing restoration must not change the destination.
type SecretStore interface {
	Read() (map[string]string, error)
	PrepareRestore(map[string]string) (func() error, error)
}

// RestorePlan marks incomplete data before volume mutation and atomically commits
// all restored metadata afterwards. Preparation is read-only.
type RestorePlan struct {
	Begin  func() error
	Commit func() error
}

// StateStore coordinates the volume boundary with a destination metadata transaction.
type StateStore interface {
	PrepareSnapshotRestore(*NamespaceSnapshotMeta, map[string]string) (*RestorePlan, error)
}

type secretService interface {
	ListSecrets() ([]storage.SecretMeta, error)
	GetSecret(string) (*storage.Secret, error)
	SaveSecret(storage.Secret) error
	DeleteSecret(string) error
	IsLocked() bool
}

type namespaceSecrets struct {
	svc                      secretService
	workspaceID, namespaceID string
}

// NamespaceSecrets scopes snapshot secrets to the captured source/destination.
// Global registry/git credentials are not transferred. Imported system overrides
// are namespace-owned rows; WithSystemSecrets adds profile defaults on export.
func NamespaceSecrets(svc *storage.SecretService, workspaceID, namespaceID string) SecretStore {
	var service secretService
	if svc != nil {
		service = svc
	}
	return &namespaceSecrets{svc: service, workspaceID: workspaceID, namespaceID: namespaceID}
}

func (s *namespaceSecrets) rows() (map[string]storage.Secret, error) {
	if s.svc == nil {
		return nil, errors.New("namespace secret service unavailable")
	}
	if s.svc.IsLocked() {
		return nil, storage.ErrSecretsLocked
	}
	metas, err := s.svc.ListSecrets()
	if err != nil {
		return nil, fmt.Errorf("list snapshot namespace secrets: %w", err)
	}
	rows := make(map[string]storage.Secret)
	scope := storage.NamespaceSecretScope(s.workspaceID, s.namespaceID)
	for _, meta := range metas {
		if !storage.IsNamespaceSecret(meta) || meta.Scope != scope {
			continue
		}
		if meta.Name == "" {
			return nil, errors.New("namespace secret has no name")
		}
		if _, exists := rows[meta.Name]; exists {
			return nil, errors.New("duplicate namespace secret name")
		}
		row, readErr := s.svc.GetSecret(meta.ID)
		if readErr != nil {
			return nil, fmt.Errorf("read snapshot namespace secret: %w", readErr)
		}
		if row == nil {
			return nil, errors.New("namespace secret row missing")
		}
		if !storage.IsNamespaceSecret(row.SecretMeta) || row.Scope != scope || row.Name != meta.Name {
			return nil, errors.New("namespace secret changed while reading")
		}
		rows[meta.Name] = *row
	}
	return rows, nil
}

func (s *namespaceSecrets) Read() (map[string]string, error) {
	rows, err := s.rows()
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(rows))
	for name, row := range rows {
		values[name] = row.Value
	}
	return values, nil
}

func (s *namespaceSecrets) PrepareRestore(values map[string]string) (func() error, error) {
	if len(values) == 0 {
		return func() error { return nil }, nil
	}
	rows, err := s.rows()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if name == "" {
			return nil, errors.New("snapshot secret has no name")
		}

		if strings.HasPrefix(name, "@citeck/system/") {
			known := false
			for _, id := range systemsecrets.KnownIDs() {
				if name == systemsecrets.NamespaceName(id) {
					known = true
				}
			}
			if !known || values[name] == "" {
				return nil, errors.New("invalid snapshot system credential")
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return func() error {
		for index, name := range names {
			row, exists := rows[name]
			if !exists {
				row = storage.Secret{SecretMeta: storage.SecretMeta{
					ID:   storage.NamespaceSecretID(s.workspaceID, s.namespaceID, name),
					Name: name, Type: storage.SecretNamespace,
					Scope: storage.NamespaceSecretScope(s.workspaceID, s.namespaceID),
				}}
			}
			row.Value = values[name]
			if saveErr := s.svc.SaveSecret(row); saveErr != nil {
				// Include the failed write: a persistence error can occur after it landed.
				var rollbackErr error
				for _, changed := range names[:index+1] {
					if old, had := rows[changed]; had {
						rollbackErr = errors.Join(rollbackErr, s.svc.SaveSecret(old))
					} else {
						rollbackErr = errors.Join(rollbackErr, s.svc.DeleteSecret(storage.NamespaceSecretID(s.workspaceID, s.namespaceID, changed)))
					}
				}
				return errors.Join(fmt.Errorf("restore namespace secrets: %w", saveErr), rollbackErr)
			}
		}
		return nil
	}, nil
}

// readSnapshotSecrets validates the whole file before touching volumes or vault.
// Parsing errors never include the payload (which contains plaintext secrets).
func readSnapshotSecrets(dir string) (values map[string]string, exists bool, err error) {
	file, err := os.Open(filepath.Join(dir, secretsFileName)) //nolint:gosec // internal extracted snapshot directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open secrets.json: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxSecretsSize+1))
	if err != nil {
		return nil, true, fmt.Errorf("read secrets.json: %w", err)
	}
	if len(raw) > maxSecretsSize {
		return nil, true, errors.New("secrets.json exceeds size limit")
	}
	values, err = decodeSnapshotSecrets(raw)
	return values, true, err
}

func decodeSnapshotSecrets(raw []byte) (map[string]string, error) {
	invalid := errors.New("invalid secrets.json: expected an object with unique nonempty names and string values")
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	values := make(map[string]string)
	for decoder.More() {
		key, keyErr := decoder.Token()
		name, ok := key.(string)
		if keyErr != nil || !ok || name == "" {
			return nil, invalid
		}
		if _, exists := values[name]; exists {
			return nil, invalid
		}
		value, valueErr := decoder.Token()
		text, ok := value.(string)
		if valueErr != nil || !ok {
			return nil, invalid
		}
		values[name] = text
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, invalid
	}
	return values, nil
}

// WithSystemSecrets includes effective system credentials in an explicitly
// requested export. Names are reserved; namespace overrides remain authoritative.
func WithSystemSecrets(base SecretStore, values map[string]string) SecretStore {
	return &systemSnapshotSecrets{SecretStore: base, values: values}
}

type systemSnapshotSecrets struct {
	SecretStore
	values map[string]string
}

func (s *systemSnapshotSecrets) Read() (map[string]string, error) {
	values, err := s.SecretStore.Read()
	if err != nil {
		return nil, fmt.Errorf("read snapshot credentials: %w", err)
	}
	for name, value := range s.values {
		if imported, exists := values[name]; exists {
			if imported == "" {
				return nil, errors.New("system snapshot credential is missing")
			}
			continue
		}
		if value == "" {
			return nil, errors.New("system snapshot credential is missing")
		}
		values[name] = value
	}
	return values, nil
}

func prepareSnapshotRestore(store SecretStore, meta *NamespaceSnapshotMeta, values map[string]string, haveSecrets bool) (*RestorePlan, error) {
	if destination, ok := store.(StateStore); ok {
		plan, err := destination.PrepareSnapshotRestore(meta, values)
		if err != nil {
			return nil, fmt.Errorf("prepare snapshot metadata transaction: %w", err)
		}
		if plan == nil || plan.Begin == nil || plan.Commit == nil {
			return nil, errors.New("invalid snapshot restore plan")
		}
		return plan, nil
	}
	plan := &RestorePlan{Begin: func() error { return nil }, Commit: func() error { return nil }}
	if haveSecrets && len(values) > 0 {
		if store == nil {
			return nil, errors.New("snapshot contains namespace secrets but no destination secret store")
		}
		restore, err := store.PrepareRestore(values)
		if err != nil {
			return nil, fmt.Errorf("prepare snapshot secrets: %w", err)
		}
		plan.Commit = restore
	}
	return plan, nil
}
