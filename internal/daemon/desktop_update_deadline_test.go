package daemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
	"github.com/stretchr/testify/require"
)

// The real Unix HTTP transport must allow staging to outlive its ordinary
// response-write budget while bytes keep arriving. No daemon or Docker starts.
func TestUpdateApplyLiftsUnixResponseDeadlineDuringActiveDownload(t *testing.T) {
	t.Setenv("CITECK_WRAPPER_SOCK", "")
	fake := updatetest.Start(t, "Citeck/citeck-launcher", updatetest.Release{Version: "2.6.0", BinaryContent: "progressing-daemon"})
	upstream := &http.Client{Timeout: time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, fake.URL()+r.URL.RequestURI(), http.NoBody)
		if requestErr != nil {
			t.Error(requestErr)
			return
		}
		resp, responseErr := upstream.Do(req)
		if responseErr != nil {
			t.Error(responseErr)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if !strings.HasSuffix(r.URL.Path, ".tar.gz") {
			_, _ = io.Copy(w, resp.Body)
			return
		}
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Error(readErr)
			return
		}
		for _, value := range data {
			if _, writeErr := w.Write([]byte{value}); writeErr != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}))
	t.Cleanup(proxy.Close)
	d := &Daemon{updateSvc: update.NewService("2.4.0", t.TempDir(), update.WithBaseURLs(proxy.URL, proxy.URL), update.WithSigningPublicKeyHex(""))}
	// An owned short path avoids the Unix socket's platform length limit.
	dir, dirErr := os.MkdirTemp("", "upd-")
	require.NoError(t, dirErr)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "socket")
	socket, socketErr := bindBootSocket(path)
	require.NoError(t, socketErr)
	socket.server.WriteTimeout = 20 * time.Millisecond
	socket.routes.set(http.HandlerFunc(d.handleUpdateApply))
	stopped := make(chan struct{})
	go func() { _ = socket.server.Serve(socket.listener); close(stopped) }()
	t.Cleanup(func() { _ = socket.server.Close(); <-stopped })
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	started := time.Now()
	resp, requestErr := client.Post("http://unix/api/v1/desktop/update/apply", "application/json", http.NoBody)
	require.NoError(t, requestErr)
	defer resp.Body.Close()
	require.Greater(t, time.Since(started), 20*time.Millisecond)
	// The missing-wrapper response proves staging finished and the ordinary
	// response could still be delivered, without applying an update in this test.
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	body, bodyErr := io.ReadAll(resp.Body)
	require.NoError(t, bodyErr)
	require.Contains(t, string(body), "no desktop wrapper to apply update")
	require.Equal(t, "ready", d.updateSvc.Status().Progress.Phase)
}
