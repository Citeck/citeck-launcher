package launchermcp

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTokenStoreHashOnlyRestartExpiryRevocationAndDiskFailure(t *testing.T) {
	path := t.TempDir() + "/private/state.json"
	s, err := OpenStore(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	expiry := time.Now().UTC().Add(time.Hour)
	token, value, err := s.Create("Pi", Developer, &expiry)
	require.NoError(t, err)
	hash := sha256.Sum256([]byte(value))
	var stored []byte
	require.NoError(t, s.db.QueryRow("SELECT hash FROM manual_tokens WHERE id=?", token.ID).Scan(&stored))
	require.Equal(t, hash[:], stored)
	b, err := os.ReadFile(s.path + "-wal")
	require.NoError(t, err)
	require.NotContains(t, string(b), value)
	info, err := os.Stat(s.path)
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0o077)
	metadata, err := json.Marshal(s.List())
	require.NoError(t, err)
	require.NotContains(t, string(metadata), "hash")
	require.NotContains(t, string(metadata), value)
	require.NoError(t, s.Close())
	s, err = OpenStore(path)
	require.NoError(t, err)
	got, ok := s.Authenticate(value)
	require.True(t, ok)
	require.Equal(t, token.ID, got.ID)
	require.NotNil(t, s.List()[0].LastUsedAt)
	for _, role := range []Role{"", "admin", "developer,observer", "DEVELOPER"} {
		_, _, err = s.Create("test", role, nil)
		require.Error(t, err)
	}
	past := time.Now().Add(-time.Hour)
	_, _, err = s.Create("expired", Observer, &past)
	require.Error(t, err)
	_, ok = s.Authenticate(strings.Repeat("bad", 200))
	require.False(t, ok)
	before := s.List()
	s.beforeCommit = func() error { return errors.New("disk full") }
	_, raw, err := s.Create("failed", Observer, nil)
	require.Error(t, err)
	require.Empty(t, raw)
	require.Equal(t, before, s.List())
	require.Error(t, s.Revoke(token.ID))
	require.Nil(t, s.List()[0].RevokedAt)
	require.Error(t, s.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
	require.False(t, s.Settings().Enabled)
	s.beforeCommit = nil
	require.NoError(t, s.Revoke(token.ID))
	_, ok = s.Authenticate(value)
	require.False(t, ok)
}
func TestTokenStatePermissionsAndRolesFailClosed(t *testing.T) {
	path := testStorePath(t)
	s, err := OpenStore(path)
	require.NoError(t, err)
	_, _, err = s.Create("x", Observer, nil)
	require.NoError(t, err)
	dbpath := s.path
	require.NoError(t, s.Close())
	require.NoError(t, os.Chmod(dbpath, 0o644))
	_, err = OpenStore(path)
	require.Error(t, err)
	require.NoError(t, os.Chmod(dbpath, 0o600))
	s, err = OpenStore(path)
	require.NoError(t, err)
	_, err = s.db.Exec("PRAGMA ignore_check_constraints=ON; UPDATE manual_tokens SET role='root'")
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, err = OpenStore(path)
	require.Error(t, err)
}
func TestManualTokenExpiryBoundBeforePersistence(t *testing.T) {
	s, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	defer s.Close()
	for _, expiry := range []time.Time{
		time.Now().Add(-time.Hour),
		time.Now().Add(maxCredentialLifetime + time.Hour),
		time.Date(2300, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(3000, time.January, 1, 0, 0, 0, 0, time.UTC),
	} {
		token, raw, createErr := s.Create("invalid expiry", Observer, &expiry)
		require.Error(t, createErr)
		require.Empty(t, raw)
		require.Empty(t, token.ID)
		require.Empty(t, s.List(), "invalid expiry must not create a token row")
	}
	expiry := time.Now().UTC().Add(maxCredentialLifetime)
	token, raw, err := s.Create("maximum validity", Observer, &expiry)
	require.NoError(t, err)
	got, ok := s.Authenticate(raw)
	require.True(t, ok)
	require.Equal(t, token.ID, got.ID)
	require.True(t, expiry.Equal(*got.ExpiresAt), "expiry must round-trip without overflow")
	_, raw, err = s.Create("permanent", Observer, nil)
	require.NoError(t, err)
	_, ok = s.Authenticate(raw)
	require.True(t, ok)
}

func TestLastUsedFailureIsStillRateLimited(t *testing.T) {
	s, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	defer s.Close()
	_, value, err := s.Create("agent", Observer, nil)
	require.NoError(t, err)
	attempts := 0
	s.beforeCommit = func() error { attempts++; return errors.New("disk full") }
	for range 10 {
		_, ok := s.Authenticate(value)
		require.True(t, ok)
	}
	require.Equal(t, 1, attempts)
}
