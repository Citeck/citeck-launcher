package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// Operator-requested JVM diagnostics: `citeck jcmd` / `jstack` / `jmap`.
//
// Both handlers can run for a long time — a heap dump is minutes on a large
// heap — so both lift the server's write deadline, the same way the
// snapshot-backed namespace create does. The runtime suspends the app's
// liveness probe for the duration; nothing here needs to know about that.

// jvmRuntime returns the active namespace's runtime, or writes the error.
func (d *Daemon) jvmRuntime(w http.ResponseWriter) (*namespace.Runtime, bool) {
	rt := d.active().runtime
	if rt == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, api.ErrCodeNotConfigured, "no active namespace")
		return nil, false
	}
	return rt, true
}

// writeJVMError maps the runtime's typed refusals onto status codes so the CLI
// can say "postgres is not a JVM app" rather than relaying a docker failure.
func writeJVMError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, namespace.ErrNotJVMApp):
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, err.Error())
	case errors.Is(err, namespace.ErrAppUnknown):
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, err.Error())
	case errors.Is(err, namespace.ErrAppNotRunning):
		writeErrorCode(w, http.StatusConflict, api.ErrCodeInvalidRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (d *Daemon) handleAppJVMCommand(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	var req api.JVMCommandRequestDto
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, "invalid request body")
		return
	}
	if req.Command == "" {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, "command is required")
		return
	}
	rt, ok := d.jvmRuntime(w)
	if !ok {
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	out, err := rt.JVMCommand(r.Context(), name, req.Command, req.Args...)
	if err != nil {
		writeJVMError(w, err)
		return
	}
	writeJSON(w, api.JVMCommandResponseDto{App: name, Command: req.Command, Output: out})
}

func (d *Daemon) handleAppHeapDump(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	rt, ok := d.jvmRuntime(w)
	if !ok {
		return
	}

	// A multi-GB heap takes minutes to write, and the response is the only way
	// the caller learns the file's name.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	file, size, err := rt.HeapDump(r.Context(), name, time.Now())
	if err != nil {
		writeJVMError(w, err)
		return
	}
	writeJSON(w, api.HeapDumpResponseDto{App: name, File: file, Size: size})
}
