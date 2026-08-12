package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
)

// jmapServer is a stand-in daemon for the whole `citeck jmap` flow: take the
// dump, find it in the export listing, fetch it, remove it. It records the
// request path of every call so the composition itself is what is asserted.
type jmapServer struct {
	mu       sync.Mutex
	dumpFile string
	body     []byte
	calls    []string
	deleted  bool
}

func newJmapServer(t *testing.T) (*jmapServer, *client.DaemonClient) {
	t.Helper()
	js := &jmapServer{dumpFile: "emodel-20260812T094312Z.hprof.gz", body: []byte{0x1f, 0x8b, 0x08, 0x00, 0x42}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.mu.Lock()
		js.calls = append(js.calls, r.Method+" "+r.URL.Path)
		js.mu.Unlock()

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/heap-dump"):
			_ = json.NewEncoder(w).Encode(api.HeapDumpResponseDto{
				App: "emodel", File: js.dumpFile, Size: int64(len(js.body)),
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/export"):
			_ = json.NewEncoder(w).Encode([]api.ExportFileDto{{
				Name: js.dumpFile, Size: int64(len(js.body)), Modified: "2026-08-12T09:43:12Z",
			}})
		case r.Method == http.MethodDelete:
			js.mu.Lock()
			js.deleted = true
			js.mu.Unlock()
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.Header().Set("Content-Length", strconv.Itoa(len(js.body)))
			_, _ = w.Write(js.body)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := client.New(client.Options{Host: srv.Listener.Addr().String()})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return js, c
}

// chdir into a temp dir so the "no -o" default (the dump's own name in the
// current directory) can be exercised without writing into the repo.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

// The whole point of `jmap`: one command takes the dump, brings it here, and
// leaves nothing on the daemon's host — a heap dump is heap-sized and holds
// every secret the process had in memory.
func TestJmap_DumpsFetchesAndCleansUp(t *testing.T) {
	js, c := newJmapServer(t)
	dir := chdirTemp(t)

	dump, err := c.HeapDump("emodel")
	require.NoError(t, err)
	require.Equal(t, js.dumpFile, dump.File)

	files, err := c.ListAppExports("emodel")
	require.NoError(t, err)
	meta, ok := findExportFile(files, dump.File)
	require.True(t, ok)

	how, err := fetchExport(c, "emodel", meta, dump.File, true)
	require.NoError(t, err)
	assert.Equal(t, "downloaded", how)

	got, err := os.ReadFile(filepath.Join(dir, js.dumpFile)) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, js.body, got)

	js.mu.Lock()
	defer js.mu.Unlock()
	assert.True(t, js.deleted, "the dump must not be left on the daemon's host")
	assert.Equal(t, []string{
		"POST /api/v1/apps/emodel/heap-dump",
		"GET /api/v1/apps/emodel/export",
		"GET /api/v1/apps/emodel/export/" + js.dumpFile,
		"DELETE /api/v1/apps/emodel/export/" + js.dumpFile,
	}, js.calls)
}

// A JVM command's output is returned verbatim — `citeck jcmd <app> help` is
// only useful because the JVM's own answer arrives unedited.
func TestJVMCommandClient_ReturnsOutputVerbatim(t *testing.T) {
	const help = "The following commands are available:\nGC.heap_dump\nThread.print\n"
	var (
		bodyMu    sync.Mutex
		gotBody   api.JVMCommandRequestDto
		decodeErr error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyMu.Lock()
		decodeErr = json.NewDecoder(r.Body).Decode(&gotBody)
		cmd := gotBody.Command
		bodyMu.Unlock()
		_ = json.NewEncoder(w).Encode(api.JVMCommandResponseDto{App: "emodel", Command: cmd, Output: help})
	}))
	defer srv.Close()

	c, err := client.New(client.Options{Host: srv.Listener.Addr().String()})
	require.NoError(t, err)
	defer c.Close()

	res, err := c.JVMCommand("emodel", "help", nil)
	require.NoError(t, err)
	require.NoError(t, decodeErr)
	assert.Equal(t, help, res.Output)
	assert.Equal(t, "help", gotBody.Command)

	// Options travel as args and are joined daemon-side into the single attach
	// slot HotSpot expects.
	_, err = c.JVMCommand("emodel", "Thread.print", []string{"-l"})
	require.NoError(t, err)
	require.NoError(t, decodeErr)
	assert.Equal(t, []string{"-l"}, gotBody.Args)
}
