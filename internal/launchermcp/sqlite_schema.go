package launchermcp

import "context"

// upgradeSchema preserves existing roots, credentials, histories and decisions.
// Versions 1/2 were the private experimental SQLite schema, not JSON authority.
func (s *Store) upgradeSchema(version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return mcpStorageError(err)
	}
	if version == 1 {
		if _, err = tx.Exec(oauthPendingSchema); err != nil {
			return s.rollback(tx, err)
		}
	}
	for _, statement := range []string{
		"DROP TRIGGER IF EXISTS family_quota", "DROP TRIGGER IF EXISTS grant_quota",
		"DROP TRIGGER IF EXISTS used_quota", "DROP TRIGGER IF EXISTS access_quota", "DROP TRIGGER IF EXISTS pending_quota",
		"DROP INDEX IF EXISTS grants_revoked", "DROP INDEX IF EXISTS grants_lifecycle",
		"DROP TABLE mcp_meta",
		"UPDATE oauth_grants SET revoked=1 WHERE lifecycle='terminal'",
		"ALTER TABLE oauth_grants DROP COLUMN lifecycle", "ALTER TABLE oauth_grants DROP COLUMN reserved",
		"ALTER TABLE oauth_grants DROP COLUMN pending_expiry", "ALTER TABLE oauth_grants DROP COLUMN metadata",
		"ALTER TABLE oauth_clients DROP COLUMN redirect", "ALTER TABLE oauth_clients DROP COLUMN metadata",
		"CREATE TRIGGER pending_quota BEFORE INSERT ON oauth_pending WHEN (SELECT count(*) FROM oauth_pending)>=256 BEGIN SELECT RAISE(ABORT,'pending quota'); END",
		"PRAGMA user_version=3",
	} {
		if _, err = tx.ExecContext(context.Background(), statement); err != nil {
			return s.rollback(tx, err)
		}
	}
	return mcpStorageError(tx.Commit())
}
