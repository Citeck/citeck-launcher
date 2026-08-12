package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
)

// exportServer is a stand-in daemon: it serves one export file and records the
// DELETEs it received, which is how the tests tell "downloaded and left alone"
// from "downloaded and cleaned up".
type exportServer struct {
	mu           sync.Mutex
	body         []byte
	truncate     bool // send only half the body
	honestLength bool // …and set Content-Length to what is actually sent
	deletes      []string
}

func newExportServer(t *testing.T, body []byte) (*exportServer, *client.DaemonClient) {
	t.Helper()
	es := &exportServer{body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			es.mu.Lock()
			es.deletes = append(es.deletes, r.URL.Path)
			es.mu.Unlock()
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		out := es.body
		if es.truncate {
			out = out[:len(out)/2]
		}
		declared := len(es.body)
		if es.honestLength {
			declared = len(out)
		}
		w.Header().Set("Content-Length", strconv.Itoa(declared))
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)

	c, err := client.New(client.Options{Host: srv.Listener.Addr().String()})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return es, c
}

func (e *exportServer) deleteCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.deletes)
}

// The plain remote case: the daemon is on another host, so the bytes come over
// the wire and land with mode 0600 — a heap dump carries every password the
// process had in memory.
func TestFetchExport_DownloadsAndWritesRestrictedMode(t *testing.T) {
	payload := []byte("JAVA PROFILE 1.0.2\x00heap bytes")
	_, c := newExportServer(t, payload)
	dest := filepath.Join(t.TempDir(), "heap.hprof.gz")

	how, err := fetchExport(c, "emodel", api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload))}, dest, false)
	require.NoError(t, err)
	assert.Equal(t, "downloaded", how)

	got, err := os.ReadFile(dest) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, payload, got)

	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, exportFileMode, info.Mode().Perm())
}

// A cut-off transfer produces a file that opens fine and is missing half the
// heap. Handing that to someone to analyze is worse than failing.
//
// Two ways it can arrive, and the second is the one that needs our own check:
// a body cut short of its Content-Length is caught by the HTTP stack, but a
// body that is short AND honest about it (a daemon that read a file being
// rewritten, a proxy that re-framed the response) arrives as a clean, complete,
// wrong-sized download.
func TestFetchExport_TruncatedTransferFailsAndLeavesNoFile(t *testing.T) {
	payload := []byte("0123456789abcdef")

	for _, tc := range []struct {
		name  string
		short bool // Content-Length matches the short body
	}{
		{"cut off mid-body", false},
		{"short but well-formed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			es, c := newExportServer(t, payload)
			es.truncate = true
			es.honestLength = tc.short
			dest := filepath.Join(t.TempDir(), "heap.hprof.gz")

			_, err := fetchExport(c, "emodel",
				api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload))}, dest, false)
			require.Error(t, err)

			_, statErr := os.Stat(dest)
			assert.True(t, os.IsNotExist(statErr), "a truncated download must not be left behind")
			assert.Zero(t, es.deleteCount(), "a failed download must never delete the original")
		})
	}
}

// Same host: the bytes are already here. With --rm the file is MOVED — free,
// and it leaves nothing behind, which is the whole "dump, take it, clean up"
// flow. The mode still has to be tightened: the container wrote this file.
func TestFetchExport_LocalMoveWhenRemoving(t *testing.T) {
	payload := []byte("local heap bytes")
	es, c := newExportServer(t, payload)

	dir := t.TempDir()
	src := filepath.Join(dir, "heap.hprof.gz")
	require.NoError(t, os.WriteFile(src, payload, 0o644)) //nolint:gosec // deliberately world-readable, as a container would write it
	dest := filepath.Join(dir, "downloaded.hprof.gz")

	how, err := fetchExport(c, "emodel",
		api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload)), HostPath: src}, dest, true)
	require.NoError(t, err)
	assert.Equal(t, "moved", how)

	_, statErr := os.Stat(src)
	assert.True(t, os.IsNotExist(statErr), "a move leaves nothing behind")
	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, exportFileMode, info.Mode().Perm(), "the container's mode must not survive the move")
	assert.Zero(t, es.deleteCount(), "a move needs no daemon-side delete")
}

// Without --rm the local file is copied, not moved: `export get` is not
// destructive, `export rm` is.
func TestFetchExport_LocalCopyKeepsTheSource(t *testing.T) {
	payload := []byte("local heap bytes")
	es, c := newExportServer(t, payload)

	dir := t.TempDir()
	src := filepath.Join(dir, "heap.hprof.gz")
	require.NoError(t, os.WriteFile(src, payload, 0o600))
	dest := filepath.Join(dir, "copy.hprof.gz")

	how, err := fetchExport(c, "emodel",
		api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload)), HostPath: src}, dest, false)
	require.NoError(t, err)
	assert.Equal(t, "copied", how)

	kept, err := os.ReadFile(src) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, payload, kept)
	assert.Zero(t, es.deleteCount())
}

// A HostPath that does not describe the file we asked for (wrong size — a
// different host's namespace with the same layout) must not be trusted; the
// download is the correct answer.
func TestFetchExport_IgnoresHostPathThatDoesNotMatch(t *testing.T) {
	payload := []byte("the real bytes")
	_, c := newExportServer(t, payload)

	dir := t.TempDir()
	impostor := filepath.Join(dir, "heap.hprof.gz")
	require.NoError(t, os.WriteFile(impostor, []byte("something else entirely"), 0o600))
	dest := filepath.Join(dir, "out.hprof.gz")

	how, err := fetchExport(c, "emodel",
		api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload)), HostPath: impostor}, dest, false)
	require.NoError(t, err)
	assert.Equal(t, "downloaded", how)

	got, err := os.ReadFile(dest) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

// The daemon-reported host path is only a shortcut, and it is trusted only as
// far as it can be checked: right size, readable, not a directory. Anything
// else and the download is the honest answer.
func TestLocalExportUsable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "heap.hprof.gz")
	require.NoError(t, os.WriteFile(file, []byte("0123456789"), 0o600))

	assert.True(t, localExportUsable(api.ExportFileDto{HostPath: file, Size: 10}))
	assert.False(t, localExportUsable(api.ExportFileDto{HostPath: file, Size: 11}),
		"a size mismatch means this is not the file the daemon described")
	assert.False(t, localExportUsable(api.ExportFileDto{HostPath: dir, Size: 10}), "a directory is not a download")
	assert.False(t, localExportUsable(api.ExportFileDto{HostPath: filepath.Join(dir, "absent"), Size: 10}))
	assert.False(t, localExportUsable(api.ExportFileDto{Size: 10}), "no path reported (remote daemon)")
}

// --rm must not fire when the download failed: the file on the daemon's host is
// the only copy that exists, and a truncated local file is not a copy.
func TestFetchExport_FailedDownloadDoesNotDelete(t *testing.T) {
	payload := []byte("0123456789abcdef")
	es, c := newExportServer(t, payload)
	es.truncate = true
	es.honestLength = true
	dest := filepath.Join(t.TempDir(), "heap.hprof.gz")

	_, err := fetchExport(c, "emodel",
		api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload))}, dest, true)
	require.Error(t, err)
	assert.Zero(t, es.deleteCount(), "the only copy of the file must survive a failed transfer")
}

// --rm on a remote download deletes through the daemon, and only after the
// bytes are safely on this machine.
func TestFetchExport_RemoteRemoveDeletesThroughTheDaemon(t *testing.T) {
	payload := []byte("heap bytes")
	es, c := newExportServer(t, payload)
	dest := filepath.Join(t.TempDir(), "heap.hprof.gz")

	how, err := fetchExport(c, "emodel", api.ExportFileDto{Name: "heap.hprof.gz", Size: int64(len(payload))}, dest, true)
	require.NoError(t, err)
	assert.Equal(t, "downloaded", how)
	require.Equal(t, 1, es.deleteCount())
	assert.Equal(t, "/api/v1/apps/emodel/export/heap.hprof.gz", es.deletes[0])
}

func TestRefuseExistingDest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "heap.hprof.gz")
	require.NoError(t, os.WriteFile(dest, []byte("previous download"), 0o600))

	require.Error(t, refuseExistingDest(dest, false))
	require.NoError(t, refuseExistingDest(dest, true), "--force is the way to say yes")
	require.NoError(t, refuseExistingDest(filepath.Join(t.TempDir(), "absent"), false))
}

func TestFormatExportSize(t *testing.T) {
	for _, c := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"}, {512, "512 B"}, {2048, "2.0 KiB"},
		{3 * 1024 * 1024, "3.0 MiB"}, {int64(2.5 * 1024 * 1024 * 1024), "2.5 GiB"},
	} {
		assert.Equal(t, c.want, formatExportSize(c.in), "%d bytes", c.in)
	}
}
