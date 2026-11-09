package launchermcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartupCleanupContinuesAndConfigureRecovers(t *testing.T) {
	store, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	dir := filepath.Join(filepath.Dir(store.path), "artifacts")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// More than one ReadDir batch, with anomalies on both sides.
	for _, name := range []string{"system-a.zip", "system-z.zip"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o700))
	}
	for i := range 120 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "system-"+strconv.Itoa(i)+".zip"), []byte("private"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not-ours"), []byte("keep"), 0o600))
	s := New(store, &fakeBackend{})
	defer s.Close()
	require.ErrorContains(t, s.startupError, "system-a.zip")
	require.ErrorContains(t, s.startupError, "system-z.zip")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 3, "other ZIPs must be cleaned despite anomalous entries")
	require.NoError(t, s.Configure(Settings{Port: DefaultPort}))
	require.NotEmpty(t, s.Status().Error, "disable must not hide the pending cleanup failure")
	require.Error(t, s.Configure(Settings{Enabled: true, Port: DefaultPort}))
	require.NoError(t, os.Remove(filepath.Join(dir, "system-a.zip")))
	require.NoError(t, os.Remove(filepath.Join(dir, "system-z.zip")))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	require.NoError(t, s.Configure(Settings{Enabled: true, Port: port}))
	require.Empty(t, s.Status().Error)
	require.True(t, s.Status().Listening)
	// A normal Configure must not rerun the startup sweep on live artifacts.
	a, err := s.CreateSystemArtifact(context.WithValue(context.Background(), issuerKey{}, "issuer"), func(w io.Writer) error {
		if _, writeErr := io.WriteString(w, "private"); writeErr != nil {
			return fmt.Errorf("write test artifact: %w", writeErr)
		}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, s.Configure(Settings{Enabled: true, Port: port}))
	require.Equal(t, 1, artifactCount(s))
	require.NotEmpty(t, a.ID)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

type expiryDuringWrite struct {
	*httptest.ResponseRecorder
	expire func()
}

func (w *expiryDuringWrite) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if w.expire != nil {
		f := w.expire
		w.expire = nil
		f()
	}
	if err != nil {
		return n, fmt.Errorf("write test response: %w", err)
	}
	return n, nil
}
func TestAbsoluteArtifactTTLAbortsActiveDownload(t *testing.T) {
	store, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	s := New(store, &fakeBackend{})
	defer s.Close()
	f, err := os.CreateTemp(t.TempDir(), "heap")
	require.NoError(t, err)
	_, err = f.Write(bytes.Repeat([]byte("x"), 1<<20))
	require.NoError(t, err)
	a, err := s.RegisterArtifact("issuer", "heap.hprof", f, nil, false)
	require.NoError(t, err)
	w := &expiryDuringWrite{ResponseRecorder: httptest.NewRecorder(), expire: func() { s.expireArtifact(a.ID) }}
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		s.downloadArtifact(w, httptest.NewRequest("GET", a.URL, http.NoBody), authPrincipal{ID: "issuer", Role: Developer})
	})
	require.Less(t, int64(w.Body.Len()), a.Size)
	require.Equal(t, strconv.FormatInt(a.Size, 10), w.Header().Get("Content-Length"))
	require.Zero(t, artifactCount(s))
	require.FileExists(t, f.Name(), "expiry must not delete source heaps")
}
