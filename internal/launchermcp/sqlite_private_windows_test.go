package launchermcp

import (
	"os"
	"path/filepath"
	"testing"

	legacyreader "github.com/citeck/citeck-launcher/internal/launchermcp/testdata/legacyreader"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsSQLitePrivateManualAuthorityAndOwnerLifetime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private # ü", "state.json")
	s, err := OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	token, raw, err := s.Create("permanent", Developer, nil)
	require.NoError(t, err)
	_, ok := s.Authenticate(raw)
	require.True(t, ok)
	_, err = OpenStore(path)
	require.ErrorContains(t, err, "already owned")
	// Check the actual DACL, not Unix permission bits (Chmod is not an ACL).
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	for _, private := range []string{s.dir, s.path, s.path + "-wal", s.path + "-shm", filepath.Join(s.dir, "state.json")} {
		sd, e := windows.GetNamedSecurityInfo(private, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		require.NoError(t, e)
		require.Contains(t, sd.String(), user.User.Sid.String())
		require.NotContains(t, sd.String(), ";;;WD)")
		require.NotContains(t, sd.String(), ";;;BU)")
	}
	require.NoError(t, s.Close())
	s, err = OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	got, ok := s.Authenticate(raw)
	require.True(t, ok)
	require.Equal(t, token.ID, got.ID)
	require.Nil(t, got.ExpiresAt)
	require.Equal(t, Developer, got.Role)
	require.NoError(t, s.Revoke(token.ID))
	require.NoError(t, s.Close())
	s, err = OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	_, ok = s.Authenticate(raw)
	require.False(t, ok)
}

func TestWindowsSQLiteLegacyManualImportAndFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.json")
	old, err := legacyreader.OpenStore(path)
	require.NoError(t, err)
	token, raw, err := old.Create("legacy", legacyreader.Observer, nil)
	require.NoError(t, err)
	s, err := OpenStore(path)
	require.NoError(t, err)
	key := append([]byte(nil), s.key...)
	require.NoError(t, s.Close())
	_, err = legacyreader.OpenStore(path)
	require.Error(t, err, "the replaced legacy file must fence old readers")
	s, err = OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	require.Equal(t, key, s.key)
	got, ok := s.Authenticate(raw)
	require.True(t, ok)
	require.Equal(t, token.ID, got.ID)
	require.Nil(t, got.ExpiresAt)
	require.FileExists(t, filepath.Join(s.dir, "legacy.backup"))
}

func TestWindowsSQLiteDSNUsesCanonicalNativePaths(t *testing.T) {
	require.Contains(t, sqliteDSN(`C:\Users\owner\private ?# ü\mcp.db`), "file:///C:/Users/owner/private%20%3F%23%20%C3%BC/mcp.db?")
	require.Contains(t, sqliteDSN(`\\server\share\mcp.db`), "file:////server/share/mcp.db?")
}

func TestWindowsSQLiteRejectsPrivateReparseLeaf(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("creating symlinks requires Windows developer mode or privilege")
	}
	_, err := OpenStore(filepath.Join(link, "state.json"))
	require.Error(t, err)
}
