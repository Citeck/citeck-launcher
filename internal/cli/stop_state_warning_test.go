package cli

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// appDaemonSocket is a fake daemon on the socket the CLI's own client resolves,
// answering the three calls `citeck stop <app>` makes: the running probe, the
// per-app stop, and the namespace fetch the "applied but not saved" check reads.
type appDaemonSocket struct {
	stateWriteError string
	stopped         []string
}

func (f *appDaemonSocket) serve(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	ln, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc(api.DaemonStatus, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DaemonStatusDto{Running: true})
	})
	mux.HandleFunc("POST "+api.AppStop("{name}"), func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		f.stopped = append(f.stopped, name)
		_ = json.NewEncoder(w).Encode(api.ActionResultDto{Success: true, Message: "app " + name + " stopped"})
	})
	mux.HandleFunc(api.Namespace, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.NamespaceDto{
			ID: "ns", Name: "ns", Status: "RUNNING", StateWriteError: f.stateWriteError,
		})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// captureStderr swaps os.Stderr for a pipe and returns what was written to it
// while fn ran. output.Errf writes there, which is where the warning belongs:
// `--format json` puts the action result on stdout, and a stray text line would
// corrupt it for a scripted caller.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	os.Stderr = orig
	require.NoError(t, w.Close())
	out := <-done
	require.NoError(t, r.Close())
	return out
}

// The reported case, end to end: `citeck stop onlyoffice` stops the container
// and answers success — because it DID stop it. What was refused is the record
// of the detach intent, so without a word here the operator finds the app
// running again at the next daemon start with nothing to explain it.
func TestStopAppWarnsWhenTheDetachIntentWasNotSaved(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	f := &appDaemonSocket{stateWriteError: "persist namespace state: disk quota exceeded"}
	f.serve(t)

	cmd := newStopCmd()
	cmd.SetArgs([]string{"onlyoffice"})
	var runErr error
	stderr := captureStderr(t, func() { runErr = cmd.Execute() })

	require.NoError(t, runErr, "the action succeeded — the exit code must stay 0")
	assert.Equal(t, []string{"onlyoffice"}, f.stopped)
	assert.Contains(t, stderr, "disk quota exceeded")
	assert.Contains(t, stderr, "could not save")
}

// A healthy store says nothing at all.
func TestStopAppIsQuietWhenTheStateWasSaved(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	f := &appDaemonSocket{}
	f.serve(t)

	cmd := newStopCmd()
	cmd.SetArgs([]string{"onlyoffice"})
	var runErr error
	stderr := captureStderr(t, func() { runErr = cmd.Execute() })

	require.NoError(t, runErr)
	assert.Empty(t, stderr)
}
