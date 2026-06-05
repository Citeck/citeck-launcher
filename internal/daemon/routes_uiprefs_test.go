package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func newUIPrefsDaemon(t *testing.T) *Daemon {
	t.Helper()
	config.SetDesktopMode(true)
	t.Cleanup(func() { config.SetDesktopMode(false) })
	s, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return &Daemon{store: s, workspaceID: "ws1", daemonCfg: config.DaemonConfig{Locale: "de"}}
}

func putPrefs(t *testing.T, d *Daemon, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, api.UIPrefs, strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handlePutUIPrefs(rec, req)
	return rec
}

func getStatusTheme(t *testing.T, d *Daemon) api.DaemonStatusDto {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, api.DaemonStatus, http.NoBody)
	rec := httptest.NewRecorder()
	d.handleDaemonStatus(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var st api.DaemonStatusDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	return st
}

func TestUIPrefs_PersistAndReflectInStatus(t *testing.T) {
	d := newUIPrefsDaemon(t)

	// Before any PUT: theme empty, locale falls back to daemon.yml.
	st := getStatusTheme(t, d)
	require.Empty(t, st.Theme)
	require.Equal(t, "de", st.Locale, "locale should fall back to daemonCfg when no ui.locale stored")

	// PUT theme + locale → 200, persisted, reflected in status (locale now wins).
	require.Equal(t, http.StatusOK, putPrefs(t, d, `{"theme":"dark","locale":"ru"}`).Code)
	st = getStatusTheme(t, d)
	require.Equal(t, "dark", st.Theme)
	require.Equal(t, "ru", st.Locale)

	// Survives a fresh status read (i.e. read from the store, not request state).
	require.Equal(t, "light", func() string {
		require.Equal(t, http.StatusOK, putPrefs(t, d, `{"theme":"light"}`).Code)
		return getStatusTheme(t, d).Theme
	}())
}

func TestUIPrefs_Validation(t *testing.T) {
	d := newUIPrefsDaemon(t)
	require.Equal(t, http.StatusBadRequest, putPrefs(t, d, `{"theme":"blue"}`).Code, "invalid theme rejected")
	require.Equal(t, http.StatusBadRequest, putPrefs(t, d, `{"locale":"xx"}`).Code, "unknown locale rejected")
	require.Equal(t, http.StatusBadRequest, putPrefs(t, d, `not json`).Code, "malformed body rejected")

	// A rejected value must not have been persisted.
	require.Empty(t, getStatusTheme(t, d).Theme)
}
