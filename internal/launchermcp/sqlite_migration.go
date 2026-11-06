package launchermcp

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Only the original settings/manual-token JSON is imported. Experimental OAuth
// JSON is unsupported: leave it untouched rather than guessing at its authority.
func parseLegacy(b []byte) (state, error) {
	if len(b) == 0 {
		return state{Settings: Settings{Port: DefaultPort}}, nil
	}
	var v state
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, mcpStorageError(err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return v, errors.New("trailing MCP legacy data")
	}
	if v.Version != 0 || len(v.OAuth) > 0 {
		return v, errors.New("experimental OAuth JSON is unsupported; original file was not changed")
	}
	if v.Settings.Port < 1024 || v.Settings.Port > 65535 {
		return v, errors.New("invalid MCP port in legacy settings")
	}
	ids, hashes := map[string]bool{}, map[string]bool{}
	for _, t := range v.Tokens {
		h, e := hex.DecodeString(t.Hash)
		if t.ID == "" || len(t.ID) > 128 || len(t.Name) < 1 || len(t.Name) > 100 || t.RevokedAt != nil && t.RevokedAt.IsZero() || ids[t.ID] || hashes[t.Hash] || !t.Role.Valid() || e != nil || len(h) != 32 || t.CreatedAt.IsZero() || t.LastUsedAt != nil && t.LastUsedAt.IsZero() || t.ExpiresAt != nil && t.ExpiresAt.IsZero() {
			return v, errors.New("invalid legacy manual authority")
		}
		ids[t.ID] = true
		hashes[t.Hash] = true
	}
	return v, nil
}
func stamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}
func importRows(tx *sql.Tx, v state) error {
	for _, r := range v.Tokens {
		h, _ := hex.DecodeString(r.Hash)
		if _, err := tx.Exec("INSERT INTO manual_tokens(id,name,hash,role,created,expiry,last_used,revoked_at) VALUES(?,?,?,?,?,?,?,?)", r.ID, r.Name, h, r.Role, r.CreatedAt.UnixNano(), stamp(r.ExpiresAt), stamp(r.LastUsedAt), stamp(r.RevokedAt)); err != nil {
			return mcpStorageError(err)
		}
	}
	return nil
}
func readOptional(path string) ([]byte, error) {
	b, err := readPrivateState(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}
func writeSynced(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // Fixed legacy backup beneath the private checked parent.
	if err != nil {
		return mcpStorageError(err)
	}
	if _, err = f.Write(b); err != nil {
		return errors.Join(err, f.Close())
	}
	return errors.Join(f.Sync(), f.Close())
}
func (s *Store) initialize(raw []byte) (err error) {
	v, err := parseLegacy(raw)
	if err != nil {
		return mcpStorageError(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return mcpStorageError(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return mcpStorageError(err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(schema); err != nil {
		return mcpStorageError(err)
	}
	if _, err = tx.Exec("INSERT INTO mcp_settings VALUES(?,?,?,1)", 1, v.Settings.Enabled, v.Settings.Port); err != nil {
		return mcpStorageError(err)
	}
	if _, err = tx.Exec("INSERT INTO mcp_root VALUES(1,?)", key); err != nil {
		return mcpStorageError(err)
	}
	if importErr := importRows(tx, v); importErr != nil {
		return mcpStorageError(importErr)
	}
	if err = tx.Commit(); err != nil {
		return mcpStorageError(err)
	}
	return syncDBDirectory(s.dir)
}

// A small marker prevents a missing database from resurrecting stale JSON or
// generating a new signing root. Existing SQL is always authoritative. No DDL
// fingerprints, provisional activation or checkpoint reconciliation are needed.
func (s *Store) markSQLAuthority() error {
	path := filepath.Join(s.dir, "state.json")
	raw, err := readOptional(path)
	if err != nil {
		return mcpStorageError(err)
	}
	marked, err := s.preserveLegacy(raw)
	if err != nil {
		return err
	}
	if marked {
		return nil
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return mcpStorageError(err)
	}
	defer root.Close()
	id, err := randomOAuthID()
	if err != nil {
		return mcpStorageError(err)
	}
	name := "authority-" + id + ".tmp"
	defer root.Remove(name)
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return mcpStorageError(err)
	}
	if _, err = f.WriteString(`{"settings":{"enabled":false,"port":0},"mcpSqlite":true}`); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return mcpStorageError(err)
	}
	if err := replaceSQLMarker(root, s.dir, name); err != nil {
		return mcpStorageError(err)
	}
	return syncDBDirectory(s.dir)
}

func (s *Store) preserveLegacy(raw []byte) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	var marker map[string]json.RawMessage
	if err := json.Unmarshal(raw, &marker); err != nil {
		return false, mcpStorageError(err)
	}
	if _, ok := marker["mcpSqlite"]; ok {
		return true, nil
	}
	if _, ok := marker["mcpSqliteFence"]; ok {
		return false, nil
	}
	if _, err := parseLegacy(raw); err != nil {
		return false, err
	}
	backup := filepath.Join(s.dir, "legacy.backup")
	prior, err := readOptional(backup)
	if err != nil {
		return false, err
	}
	if len(prior) > 0 {
		if !bytes.Equal(prior, raw) {
			return false, errors.New("legacy backup already contains different data")
		}
		return false, nil
	}
	return false, writeSynced(backup, raw)
}
