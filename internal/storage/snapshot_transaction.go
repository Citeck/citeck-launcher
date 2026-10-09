package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// SnapshotState and encrypted secret rows form one metadata transaction.
// Volume extraction is deliberately outside this transaction.
type SnapshotState struct {
	WorkspaceID string
	NamespaceID string
	Status      string
	JSON        string
}

type snapshotCommitter interface {
	CommitSnapshotState(SnapshotState, []Secret) error
}

// SnapshotTransactionsAvailable checks support without reading a locked vault.
func (ss *SecretService) SnapshotTransactionsAvailable() bool {
	_, ok := ss.store.(snapshotCommitter)
	return ok
}

// CommitNamespaceSnapshot encrypts with the CURRENT destination key while holding
// the vault lock through the atomic store commit, so a password change cannot
// leave rows encrypted with an obsolete key.
func (ss *SecretService) CommitNamespaceSnapshot(state SnapshotState, values map[string]string) error {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	committer, ok := ss.store.(snapshotCommitter)
	if !ok {
		return errors.New("snapshot metadata transactions unavailable")
	}
	if len(values) > 0 && ss.encrypted && ss.derivedKey == nil {
		return ErrSecretsLocked
	}
	existing := make(map[string]SecretMeta)
	if len(values) > 0 {
		metas, err := ss.store.ListSecrets()
		if err != nil {
			return fmt.Errorf("read destination credential metadata: %w", err)
		}
		for _, meta := range metas {
			if meta.Type != SecretNamespace || meta.Scope != NamespaceSecretScope(state.WorkspaceID, state.NamespaceID) {
				continue
			}
			if _, duplicate := existing[meta.Name]; duplicate {
				return errors.New("duplicate destination namespace credential")
			}
			existing[meta.Name] = meta
		}
	}
	rows := make([]Secret, 0, len(values))
	for name, value := range values {
		row := Secret{SecretMeta: SecretMeta{ID: NamespaceSecretID(state.WorkspaceID, state.NamespaceID, name), Name: name, Type: SecretNamespace, Scope: NamespaceSecretScope(state.WorkspaceID, state.NamespaceID)}, Value: value}
		if meta, have := existing[name]; have {
			row.SecretMeta = meta
		}
		if ss.encrypted {
			ciphertext, err := encryptValue(ss.derivedKey, []byte(value))
			if err != nil {
				return fmt.Errorf("encrypt snapshot credential: %w", err)
			}
			row.Value = ciphertext
		}
		rows = append(rows, row)
	}
	if err := committer.CommitSnapshotState(state, rows); err != nil {
		return fmt.Errorf("commit snapshot metadata: %w", err)
	}
	return nil
}

func validateSnapshotCommit(state SnapshotState, rows []Secret) error {
	if state.NamespaceID == "" || filepath.Base(state.NamespaceID) != state.NamespaceID || state.NamespaceID == "." || state.NamespaceID == ".." || !json.Valid([]byte(state.JSON)) {
		return errors.New("invalid snapshot namespace state")
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.Name == "" || row.Type != SecretNamespace || row.Scope != NamespaceSecretScope(state.WorkspaceID, state.NamespaceID) || row.ID == "" || filepath.Base(row.ID) != row.ID || row.ID == "." || row.ID == ".." || seen[row.ID] {
			return errors.New("invalid snapshot namespace credential")
		}
		seen[row.ID] = true
	}
	return nil
}

// All FileStore handles for one conf directory share their lock, including the
// install path's separately opened vault/state handles.
var fileStoreLocks sync.Map

func sharedFileStoreLock(base string) *sync.RWMutex {
	absolute, err := filepath.Abs(base)
	if err != nil {
		absolute = filepath.Clean(base)
	}
	if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = canonical
	}
	lock, _ := fileStoreLocks.LoadOrStore(absolute, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

type snapshotFileChange struct {
	SecretID string `json:"secretId,omitempty"`
	Before   []byte `json:"before,omitempty"`
	Existed  bool   `json:"existed"`
	After    []byte `json:"after"`
}

type snapshotFileJournal struct {
	State     SnapshotState        `json:"state"`
	Changes   []snapshotFileChange `json:"changes"`
	Committed bool                 `json:"committed"`
}

func (s *FileStore) snapshotJournalPath() string {
	return filepath.Join(s.baseDir, "snapshot-state-transaction.json")
}

func (s *FileStore) snapshotReadyUnderLock() error {
	_, err := os.Stat(s.snapshotJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return errors.New("snapshot metadata recovery required; reopen the store before use")
}

func syncSnapshotDir(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open snapshot metadata directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync snapshot metadata directory: %w", err)
	}
	return nil
}

func snapshotMkdirAll(path string, mode os.FileMode) error {
	var created []string
	for parent := path; ; parent = filepath.Dir(parent) {
		_, err := os.Stat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect snapshot metadata directory: %w", err)
		}
		created = append(created, parent)
		if filepath.Dir(parent) == parent {
			break
		}
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("create snapshot metadata directory: %w", err)
	}
	for index := len(created) - 1; index >= 0; index-- {
		if err := syncSnapshotDir(created[index]); err != nil {
			return err
		}
	}
	return nil
}

func writeSnapshotFile(path string, data []byte, mode os.FileMode) error {
	if err := snapshotMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := fsutil.AtomicWriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write snapshot metadata: %w", err)
	}
	return syncSnapshotDir(path)
}

const maxSnapshotJournalSize = 64 << 20

func (s *FileStore) writeSnapshotJournal(j snapshotFileJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("encode snapshot metadata journal: %w", err)
	}
	if len(data) > maxSnapshotJournalSize {
		return errors.New("snapshot metadata journal exceeds size limit")
	}
	return writeSnapshotFile(s.snapshotJournalPath(), data, 0o600)
}

func (s *FileStore) snapshotChangePath(j snapshotFileJournal, c snapshotFileChange) string {
	if c.SecretID != "" {
		return s.secretPath(c.SecretID)
	}
	return s.nsStatePath(j.State.NamespaceID)
}

func (s *FileStore) applySnapshotJournal(j snapshotFileJournal, after bool) error {
	for _, c := range j.Changes {
		path := s.snapshotChangePath(j, c)
		data := c.Before
		if after {
			data = c.After
		}
		if !after && !c.Existed {
			if err := os.Remove(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return fmt.Errorf("remove uncommitted snapshot metadata: %w", err)
			}
			if err := syncSnapshotDir(path); err != nil {
				return err
			}
			continue
		}
		if err := writeSnapshotFile(path, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileStore) removeSnapshotJournal() error {
	if err := os.Remove(s.snapshotJournalPath()); err != nil {
		return fmt.Errorf("remove snapshot metadata journal: %w", err)
	}
	return syncSnapshotDir(s.snapshotJournalPath())
}

// recoverSnapshotTransaction runs before NewFileStore exposes any reads.
// Prepared transactions undo; committed transactions redo. Payloads contain
// ciphertext whenever the vault is encrypted, and the journal is owner-only.
func (s *FileStore) recoverSnapshotTransaction() error {

	file, err := os.Open(s.snapshotJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open snapshot metadata journal: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotJournalSize+1))
	if err != nil {
		return fmt.Errorf("read snapshot metadata journal: %w", err)
	}
	var j snapshotFileJournal
	if len(data) > maxSnapshotJournalSize || json.Unmarshal(data, &j) != nil || !validSnapshotJournal(j) {
		return errors.New("invalid snapshot metadata journal")
	}

	if err := s.applySnapshotJournal(j, j.Committed); err != nil {
		return fmt.Errorf("recover snapshot metadata: %w", err)
	}
	return s.removeSnapshotJournal()
}

// CommitSnapshotState atomically publishes keys and namespace state to readers.
// Before-image journalling also makes a process crash recoverable on next open.
func (s *FileStore) CommitSnapshotState(state SnapshotState, rows []Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateSnapshotCommit(state, rows); err != nil {
		return err
	}
	if err := s.recoverSnapshotTransaction(); err != nil {
		return err
	}
	j := snapshotFileJournal{State: state}
	for _, row := range rows {
		if existing, err := s.readSecret(s.secretPath(row.ID)); err == nil {
			row.CreatedAt = existing.CreatedAt
		}
		if row.CreatedAt.IsZero() {
			row.CreatedAt = time.Now()
		}
		data, err := json.Marshal(storedSecretFile(row))
		if err != nil {
			return fmt.Errorf("encode snapshot credential: %w", err)
		}
		j.Changes = append(j.Changes, snapshotFileChange{SecretID: row.ID, After: data})
	}
	j.Changes = append(j.Changes, snapshotFileChange{After: []byte(state.JSON)})
	for i := range j.Changes {
		before, err := os.ReadFile(s.snapshotChangePath(j, j.Changes[i]))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read snapshot metadata before-image: %w", err)
		}
		j.Changes[i].Before, j.Changes[i].Existed = before, err == nil
	}
	if err := s.writeSnapshotJournal(j); err != nil {
		return err
	}
	if err := s.applySnapshotJournal(j, true); err != nil {
		rollbackErr := s.applySnapshotJournal(j, false)
		if rollbackErr == nil {
			rollbackErr = s.removeSnapshotJournal()
		}
		return errors.Join(err, rollbackErr)
	}
	j.Committed = true
	if err := s.writeSnapshotJournal(j); err != nil {
		// Leave the journal: reopening resolves the durable commit marker.
		return err
	}
	return s.removeSnapshotJournal()
}

// RecoverSnapshotMetadata resolves a file transaction before restore/load reads.
// SQL transactions are already recovered by opening the SQLite database.
func RecoverSnapshotMetadata(store Store) error {
	if recoverer, ok := store.(interface{ RecoverSnapshotMetadata() error }); ok {
		if err := recoverer.RecoverSnapshotMetadata(); err != nil {
			return fmt.Errorf("recover snapshot metadata: %w", err)
		}
	}
	return nil
}

// RecoverSnapshotMetadata completes journal recovery while excluding all readers.
func (s *FileStore) RecoverSnapshotMetadata() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recoverSnapshotTransaction()
}

func validSnapshotJournal(j snapshotFileJournal) bool {
	if validateSnapshotCommit(j.State, nil) != nil {
		return false
	}
	stateSeen := false
	seen := make(map[string]bool)
	for _, change := range j.Changes {
		if change.SecretID == "" {
			if stateSeen || string(change.After) != j.State.JSON {
				return false
			}
			stateSeen = true
			continue
		}
		if seen[change.SecretID] {
			return false
		}
		seen[change.SecretID] = true
		var file secretFile
		if json.Unmarshal(change.After, &file) != nil || file.ID != change.SecretID {
			return false
		}
		row := Secret{SecretMeta: SecretMeta{ID: file.ID, Name: file.Name, Type: file.Type, Scope: file.Scope}, Value: file.Value}
		if validateSnapshotCommit(j.State, []Secret{row}) != nil {
			return false
		}
	}
	return stateSeen
}
