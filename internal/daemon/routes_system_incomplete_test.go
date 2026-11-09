package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSystemDumpSourceFailureIsExplicitAndKeepsLaterSources(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	require.NoError(t, os.MkdirAll(config.ReportsDir(), 0o700))
	// A dangling symlink deterministically fails safe reads, even as root.
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "private"), filepath.Join(config.ReportsDir(), "bad.log")))
	require.NoError(t, os.WriteFile(filepath.Join(config.ReportsDir(), "good.log"), []byte("report"), 0o600))
	require.NoError(t, os.WriteFile(config.DaemonLogPath(), []byte("daemon marker"), 0o600))
	require.NoError(t, os.Symlink("missing", config.LauncherLogPath()))
	require.NoError(t, os.WriteFile(config.LauncherLogPath()+".1", []byte("rotated marker"), 0o600))
	entries := dumpZipEntries(t, &Daemon{})
	require.Contains(t, entries["INCOMPLETE.txt"], "reports/bad.log")
	require.Contains(t, entries["INCOMPLETE.txt"], "launcher-logs/launcher.log")
	require.NotContains(t, entries["INCOMPLETE.txt"], config.HomeDir())
	require.Equal(t, "report", entries["reports/good.log"])
	require.Equal(t, "daemon marker", entries["daemon-logs/daemon.log"])
	require.Equal(t, "rotated marker", entries["launcher-logs/launcher.log.1"])

	// MCP uses this shared writer: source warnings must not return a fatal
	// error that would make CreateSystemArtifact discard the entire ZIP.
	d := &Daemon{}
	var b bytes.Buffer
	require.NoError(t, d.streamSystemDumpZip(context.Background(), &b, d.active(), map[string]any{}, nil, nil))
	zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	require.NoError(t, err)
	require.NotEmpty(t, zr.File)
}

func TestSystemDumpRejectsSourceParentSymlinksButAllowsSymlinkedHome(t *testing.T) {
	for _, kind := range []string{"reports", "logs", "updates", "home"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CITECK_HOME", home)
			require.NoError(t, os.MkdirAll(config.LogDir(), 0o700))
			outside := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(outside, "private.txt"), []byte("outside-secret"), 0o600))
			switch kind {
			case "reports":
				require.NoError(t, os.Symlink(outside, config.ReportsDir()))
			case "logs":
				require.NoError(t, os.Remove(config.LogDir()))
				require.NoError(t, os.WriteFile(filepath.Join(outside, "daemon.log"), []byte("outside-secret"), 0o600))
				require.NoError(t, os.Symlink(outside, config.LogDir()))
			case "updates":
				require.NoError(t, os.WriteFile(filepath.Join(outside, "manifest.json"), []byte("outside-secret"), 0o600))
				require.NoError(t, os.Symlink(outside, config.UpdatesDir()))
			case "home":
				link := filepath.Join(t.TempDir(), "home")
				require.NoError(t, os.WriteFile(config.DaemonLogPath(), []byte("own-log"), 0o600))
				require.NoError(t, os.Symlink(home, link))
				t.Setenv("CITECK_HOME", link)
			}
			entries := dumpZipEntries(t, &Daemon{})
			for _, content := range entries {
				require.NotContains(t, content, "outside-secret")
			}
			if kind == "home" {
				require.Equal(t, "own-log", entries["daemon-logs/daemon.log"])
			} else {
				require.Contains(t, entries, "INCOMPLETE.txt")
			}
		})
	}
}

func TestHTTPSystemDumpFatalWriterErrorAbortsResponse(t *testing.T) {
	handler := RecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		(&Daemon{}).writeSystemDumpZip(context.Background(), w, map[string]any{}, nil, nil)
	}))
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(&refusedDumpResponse{ResponseRecorder: httptest.NewRecorder()}, httptest.NewRequest("GET", "/", http.NoBody))
	})
}

func TestRecoveryMiddlewarePropagatesWrappedAbort(t *testing.T) {
	for _, panicErr := range []error{http.ErrAbortHandler, fmt.Errorf("stream failed: %w", http.ErrAbortHandler)} {
		handler := RecoveryMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(panicErr) }))
		require.PanicsWithValue(t, http.ErrAbortHandler, func() {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", http.NoBody))
		})
	}
}

func TestCancelledDumpAbortsThroughRealHTTPAndRecoveryMiddleware(t *testing.T) {
	server := httptest.NewServer(RecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		(&Daemon{}).writeSystemDumpZip(ctx, w, map[string]any{}, nil, nil)
	})))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err, "middleware must not swallow ErrAbortHandler into a clean completed download")
}

type refusedDumpResponse struct{ *httptest.ResponseRecorder }

func (*refusedDumpResponse) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
