package launchermcp

// Add owner-selected metadata without changing any existing grant, root or token.
func (s *Store) upgradeApprovalSchema() error {
	tx, err := s.db.Begin()
	if err != nil {
		return mcpStorageError(err)
	}
	for _, statement := range []string{
		"ALTER TABLE oauth_grants ADD COLUMN name TEXT NOT NULL DEFAULT '' CHECK(length(CAST(name AS BLOB))<=100)",
		"ALTER TABLE oauth_pending ADD COLUMN name TEXT NOT NULL DEFAULT '' CHECK(length(CAST(name AS BLOB))<=100)",
		"ALTER TABLE oauth_pending ADD COLUMN grant_deadline INTEGER",
		"PRAGMA user_version=4",
	} {
		if _, err = tx.Exec(statement); err != nil {
			return s.rollback(tx, err)
		}
	}
	return mcpStorageError(tx.Commit())
}
