package setup

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerformUndoOnlineSaveAndRefusal(t *testing.T) {
	for _, mode := range []string{"saved", "refused", "different input"} {
		t.Run(mode, func(t *testing.T) {
			cfgPath := writeTestNamespaceYml(t, t.TempDir(), "new.com")
			original, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			var puts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == api.DaemonStatus:
					_, _ = w.Write([]byte(`{"running":true}`))
				case r.URL.Path == api.Config && r.Method == http.MethodGet:
					if mode == "different input" {
						_, _ = w.Write([]byte("id: different\n"))
					} else {
						_, _ = w.Write(original)
					}
				case r.URL.Path == api.Config && r.Method == http.MethodPut:
					puts.Add(1)
					if mode == "refused" {
						w.WriteHeader(http.StatusConflict)
						_, _ = w.Write([]byte(`{"message":"blocked by daemon"}`))
						return
					}
					data, readErr := io.ReadAll(r.Body)
					if !assert.NoError(t, readErr) {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					// A fake daemon persists the request; CLI must not overwrite it afterwards.
					cfg, parseErr := namespace.ParseNamespaceConfig(data)
					if !assert.NoError(t, parseErr) {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					assert.Equal(t, "old.com", cfg.Proxy.Host)
					if !assert.NoError(t, os.WriteFile(cfgPath, []byte("id: default\nname: daemon saved\n"), 0o600)) {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(`{"success":true}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			t.Setenv("CITECK_HOST", strings.TrimPrefix(srv.URL, "http://"))
			_, err = performUndo(NamespaceFile, cfgPath, historyDir(cfgPath), "entry", hostnamePatchRecord("old.com", "new.com"))
			after, readErr := os.ReadFile(cfgPath)
			require.NoError(t, readErr)
			if mode == "saved" {
				require.NoError(t, err)
				assert.Contains(t, string(after), "daemon saved")
				assert.Equal(t, int32(1), puts.Load())
			} else {
				require.Error(t, err)
				assert.Equal(t, original, after)
				if mode == "refused" {
					assert.Equal(t, int32(1), puts.Load())
				} else {
					assert.Zero(t, puts.Load())
				}
				entries, listErr := listPatchEntries(historyDir(cfgPath))
				require.NoError(t, listErr)
				assert.Empty(t, entries)
			}
		})
	}
}
