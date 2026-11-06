package launchermcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestArtifactDownloadBoundToDeveloperTokenAndExpiry(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	dev, value, err := store.Create("dev", Developer, nil)
	require.NoError(t, err)
	_, other, err := store.Create("other", Developer, nil)
	require.NoError(t, err)
	_, observer, err := store.Create("observer", Observer, nil)
	require.NoError(t, err)
	service := New(store, &fakeBackend{})
	defer service.Close()
	path := filepath.Join(t.TempDir(), "heap.hprof")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("heap", 4096)), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	a, err := service.RegisterArtifact(dev.ID, "heap.hprof", f, nil, false)
	require.NoError(t, err)
	h := service.Handler(DefaultPort)
	for _, c := range []struct {
		auth   string
		status int
	}{{"", 401}, {observer, 403}, {other, 404}, {value, 200}} {
		r := httptest.NewRequest("GET", a.URL, http.NoBody)
		r.Host = "localhost:19380"
		if c.auth != "" {
			r.Header.Set("Authorization", "Bearer "+c.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, c.status, w.Code, w.Body.String())
		if c.status == 200 {
			require.Equal(t, strconv.FormatInt(a.Size, 10), w.Header().Get("Content-Length"))
			require.Equal(t, int(a.Size), w.Body.Len())
		}
	}
	service.expireArtifact(a.ID)
	require.Zero(t, artifactCount(service))
	require.FileExists(t, path, "source heaps must never be deleted")
	f, err = os.Open(path)
	require.NoError(t, err)
	a, err = service.RegisterArtifact(dev.ID, "heap.hprof", f, nil, false)
	require.NoError(t, err)
	require.NoError(t, store.Revoke(dev.ID))
	r := httptest.NewRequest("GET", a.URL, http.NoBody)
	r.Host = "localhost:19380"
	r.Header.Set("Authorization", "Bearer "+value)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
}
func TestArtifactsStreamLargeFilesAndDetectChangedSize(t *testing.T) {
	s, err := OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	token, value, err := s.Create("dev", Developer, nil)
	require.NoError(t, err)
	service := New(s, &fakeBackend{})
	defer service.Close()
	path := filepath.Join(t.TempDir(), "heap.hprof")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(20<<20))
	a, err := service.RegisterArtifact(token.ID, "heap.hprof", f, nil, false)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(nil)
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	server.Config.Handler = service.Handler(n)
	server.Start()
	defer server.Close()
	client := &http.Client{Transport: bearerTransport{value}}
	response, err := client.Get(server.URL + a.URL)
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	copied, err := io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.Equal(t, a.Size, copied)
	require.NoError(t, response.Body.Close())
	require.NoError(t, f.Truncate(1))
	response, err = client.Get(server.URL + a.URL)
	require.NoError(t, err)
	require.Equal(t, 409, response.StatusCode)
	require.NoError(t, response.Body.Close())
}
func TestSystemArtifactQuotaTTLAndPartialCleanup(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	service := New(store, &fakeBackend{})
	defer service.Close()
	ctx := context.WithValue(context.Background(), issuerKey{}, "issuer")
	a, err := service.CreateSystemArtifact(ctx, func(w io.Writer) error {
		_, e := io.WriteString(w, "private diagnostic zip")
		if e != nil {
			return fmt.Errorf("test system writer: %w", e)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(22), a.Size)
	require.NotContains(t, a.URL, "token")
	dir := filepath.Join(filepath.Dir(store.path), "artifacts")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	service.expireArtifact(a.ID)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	_, err = service.CreateSystemArtifact(ctx, func(w io.Writer) error { return context.Canceled })
	require.Error(t, err)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	limited := &artifactWriter{ctx: ctx, w: io.Discard, left: 10}
	_, err = limited.Write(make([]byte, 11))
	require.Error(t, err)
	service.artifacts.reserved = maxSystemStorageBytes
	_, err = service.CreateSystemArtifact(ctx, func(io.Writer) error { t.Fatal("quota must refuse before writer"); return nil })
	require.Error(t, err)
	service.artifacts.reserved = 0
	for range maxArtifacts {
		f, e := os.CreateTemp(t.TempDir(), "heap")
		require.NoError(t, e)
		_, e = f.WriteString("x")
		require.NoError(t, e)
		_, e = service.RegisterArtifact("issuer", "heap.hprof", f, nil, false)
		require.NoError(t, e)
	}
	f, err := os.CreateTemp(t.TempDir(), "over-quota")
	require.NoError(t, err)
	_, err = f.WriteString("x")
	require.NoError(t, err)
	_, err = service.RegisterArtifact("issuer", "heap.hprof", f, nil, false)
	require.Error(t, err)
	_, err = f.Stat()
	require.Error(t, err, "failed registration closes its handle")
	service.Close()
	require.Zero(t, artifactCount(service))
}
func TestDefaultOffPersistsOnlyExplicitEnable(t *testing.T) {
	path := t.TempDir() + "/state.json"
	store, err := OpenStore(path)
	require.NoError(t, err)
	service := New(store, &fakeBackend{})
	defer service.Close()
	require.NoError(t, service.Start())
	require.False(t, service.Status().Listening)
	_, _, err = store.Create("observer", Observer, nil)
	require.NoError(t, err)
	service.Close()
	restarted, err := OpenStore(path)
	require.NoError(t, err)
	require.False(t, restarted.Settings().Enabled)
	service = New(restarted, &fakeBackend{})
	defer service.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	_, p, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(p)
	require.NoError(t, listener.Close())
	require.NoError(t, service.Configure(Settings{Enabled: true, Port: port}))
	service.Close()
	restarted, err = OpenStore(path)
	require.NoError(t, err)
	require.True(t, restarted.Settings().Enabled)
	service = New(restarted, &fakeBackend{})
	defer service.Close()
	require.NoError(t, service.Start())
	require.True(t, service.Status().Listening)
	require.NoError(t, service.Configure(Settings{Enabled: false, Port: port}))
	service.Close()
	restarted, err = OpenStore(path)
	require.NoError(t, err)
	require.False(t, restarted.Settings().Enabled)
	require.NoError(t, restarted.Close())
	// An old/migrated profile with no enabled field is still disabled.
	legacyPath := t.TempDir() + "/state.json"
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{"settings":{"port":19380},"tokens":[]}`), 0o600))
	restarted, err = OpenStore(legacyPath)
	require.NoError(t, err)
	require.False(t, restarted.Settings().Enabled)
	require.NoError(t, restarted.Close())
}

type cancelBackend struct{ entered, done chan struct{} }

func (b cancelBackend) Call(ctx context.Context, _ string, _ Arguments) (any, error) {
	close(b.entered)
	<-ctx.Done()
	close(b.done)
	return nil, fmt.Errorf("test canceled backend: %w", ctx.Err())
}
func TestSDKRequestCancellationReachesBackend(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	_, value, err := store.Create("dev", Developer, nil)
	require.NoError(t, err)
	backend := cancelBackend{make(chan struct{}), make(chan struct{})}
	service := New(store, backend)
	server := httptest.NewUnstartedServer(nil)
	_, p, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	server.Config.Handler = service.Handler(port)
	server.Start()
	defer server.Close()
	session := sdkSession(t, server.URL+"/mcp", value)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "namespace_status", Arguments: map[string]any{"workspaceId": "ws", "namespaceId": "ns"}})
	}()
	<-backend.entered
	cancel()
	select {
	case <-backend.done:
	case <-time.After(2 * time.Second):
		t.Fatal("SDK request cancellation was lost")
	}
	<-finished
}

func artifactCount(s *Service) int {
	s.artifacts.mu.Lock()
	defer s.artifacts.mu.Unlock()
	return len(s.artifacts.entries)
}
