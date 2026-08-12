package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
)

func postJSON(mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	return rec
}

// Without an active namespace there is no runtime to attach to. The answer has
// to say that rather than panic on a nil runtime.
func TestJVMRoutes_NoActiveNamespace(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	rec := postJSON(mux, api.AppJVMCommand("emodel"), `{"command":"Thread.print"}`)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, api.ErrCodeNotConfigured, decodeErr(t, rec).Code)

	rec = postJSON(mux, api.AppHeapDump("emodel"), "")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestJVMCommandRoute_RejectsEmptyCommand(t *testing.T) {
	mux, _ := newExportTestDaemon(t)

	rec := postJSON(mux, api.AppJVMCommand("emodel"), `{"args":["-l"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, api.ErrCodeInvalidRequest, decodeErr(t, rec).Code)

	rec = postJSON(mux, api.AppJVMCommand("emodel"), `not json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestJVMRoutes_RejectInvalidAppName(t *testing.T) {
	mux, _ := newExportTestDaemon(t)

	for _, path := range []string{
		"/api/v1/apps/%2E%2E/jcmd",
		"/api/v1/apps/%2E%2E/heap-dump",
	} {
		rec := postJSON(mux, path, `{"command":"Thread.print"}`)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", path, rec.Body.String())
		assert.Equal(t, api.ErrCodeInvalidRequest, decodeErr(t, rec).Code)
	}
}

// "emodel is not a JVM app" / "is not running" are answers the operator can act
// on; a relayed docker exec failure is not. The generated def in this daemon
// has IsJVM unset, which is exactly the non-JVM case (postgres, the proxy).
func TestJVMCommandRoute_NonJVMAppIs400(t *testing.T) {
	mux, _ := newExportTestDaemon(t)

	rec := postJSON(mux, api.AppJVMCommand("emodel"), `{"command":"Thread.print"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var errDto api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errDto))
	assert.Contains(t, errDto.Message, "not a JVM app")
	assert.Contains(t, errDto.Message, "emodel")
}
