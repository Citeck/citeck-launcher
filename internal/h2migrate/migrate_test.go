package h2migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sha256File returns the hex-encoded SHA256 of path. Used to lock in the
// "MVStore file is never modified" contract that the real-data migration test
// also relies on.
func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: test path under t.TempDir()
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestBackupKotlinStorage_FirstRunCreatesBackup verifies the parachute is
// materialized byte-for-byte on the first migration attempt and that the
// original storage.db is untouched.
func TestBackupKotlinStorage_FirstRunCreatesBackup(t *testing.T) {
	homeDir := t.TempDir()
	h2Path := filepath.Join(homeDir, "storage.db")
	payload := []byte("this is a fake H2 MVStore payload\x00\x01\x02")
	require.NoError(t, os.WriteFile(h2Path, payload, 0o640))

	originalSum := sha256File(t, h2Path)

	require.NoError(t, backupKotlinStorage(h2Path))

	backupPath := h2Path + kotlinBackupSuffix
	_, err := os.Stat(backupPath)
	require.NoError(t, err, "backup file must exist")

	backupSum := sha256File(t, backupPath)
	assert.Equal(t, originalSum, backupSum, "backup must equal source byte-for-byte")
	assert.Equal(t, originalSum, sha256File(t, h2Path), "source storage.db must be unchanged")
}

// TestBackupKotlinStorage_SecondRunSkips locks in idempotency: once a
// pre-migration snapshot exists, subsequent runs must not overwrite it. This
// matters because the user may have run a degraded migration once, then re-run
// after a launcher upgrade — the FIRST backup is the immutable parachute.
func TestBackupKotlinStorage_SecondRunSkips(t *testing.T) {
	homeDir := t.TempDir()
	h2Path := filepath.Join(homeDir, "storage.db")
	require.NoError(t, os.WriteFile(h2Path, []byte("original"), 0o640))

	// First run materializes the snapshot.
	require.NoError(t, backupKotlinStorage(h2Path))

	backupPath := h2Path + kotlinBackupSuffix
	info1, err := os.Stat(backupPath)
	require.NoError(t, err)
	firstMtime := info1.ModTime()
	firstSum := sha256File(t, backupPath)

	// Mutate storage.db so a regression that overwrote the backup would be
	// caught by the hash + size check below.
	require.NoError(t, os.WriteFile(h2Path, []byte("MUTATED storage.db contents (longer)"), 0o640))

	// Make the FS modtime granularity-gap observable.
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, backupKotlinStorage(h2Path))

	info2, err := os.Stat(backupPath)
	require.NoError(t, err)
	assert.Equal(t, firstMtime, info2.ModTime(), "backup mtime must not change on second run")
	assert.Equal(t, firstSum, sha256File(t, backupPath), "backup contents must match first snapshot")
}

// TestBackupKotlinStorage_SourceUnchangedAcrossRuns is the explicit SHA256
// invariant: both first and second runs treat storage.db as read-only.
func TestBackupKotlinStorage_SourceUnchangedAcrossRuns(t *testing.T) {
	homeDir := t.TempDir()
	h2Path := filepath.Join(homeDir, "storage.db")
	require.NoError(t, os.WriteFile(h2Path, []byte("simulated MVStore"), 0o640))

	initialSum := sha256File(t, h2Path)

	require.NoError(t, backupKotlinStorage(h2Path))
	assert.Equal(t, initialSum, sha256File(t, h2Path), "first run must not touch storage.db")

	require.NoError(t, backupKotlinStorage(h2Path))
	assert.Equal(t, initialSum, sha256File(t, h2Path), "second run must not touch storage.db")
}

// TestBackupKotlinStorage_NoLeftoverTmp guarantees that a successful run
// removes its sibling .tmp file. The temp residue would otherwise confuse a
// human inspecting ~/.citeck/launcher/ after migration.
func TestBackupKotlinStorage_NoLeftoverTmp(t *testing.T) {
	homeDir := t.TempDir()
	h2Path := filepath.Join(homeDir, "storage.db")
	require.NoError(t, os.WriteFile(h2Path, []byte("payload"), 0o640))

	require.NoError(t, backupKotlinStorage(h2Path))

	tmpPath := h2Path + kotlinBackupSuffix + ".tmp"
	_, err := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "no .tmp file should remain after successful backup; got err=%v", err)
}
