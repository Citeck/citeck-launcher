//go:build linux

package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/jvmattach"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Map an inert fixture file so the REAL host /proc discovery recognizes this
// test process, then serve its attach socket ourselves. No JVM, signal, Docker
// engine or production injection hook is needed. The socket exists throughout
// the call, so Command never enters its SIGQUIT listener-start path.
func TestMCPHostAttachLostReplyIsUnknownWithoutContainerReplay(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "libjvm.so")
	require.NoError(t, os.WriteFile(lib, make([]byte, 4096), 0o600))
	f, err := os.Open(lib)
	require.NoError(t, err)
	defer f.Close()
	mapping, err := unix.Mmap(int(f.Fd()), 0, 4096, unix.PROT_READ, unix.MAP_PRIVATE)
	require.NoError(t, err)
	defer unix.Munmap(mapping)
	nsPID, err := jvmattach.New().NSPid(os.Getpid())
	require.NoError(t, err)
	sock := fmt.Sprintf("/proc/%d/root/tmp/.java_pid%d", os.Getpid(), nsPID)
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer ln.Close()
	request := make(chan string, 1)
	go func() {
		conn, e := ln.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, len("1\x00jcmd\x00GC.run\x00\x00\x00"))
		_, _ = io.ReadFull(conn, buf)
		request <- string(buf)
		_, _ = io.WriteString(conn, "0") // command delivered; incomplete completion line
	}()
	var containerAttempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/cid/json") {
			_, _ = fmt.Fprintf(w, `{"Id":"cid","State":{"Pid":%d}}`, os.Getpid())
			return
		}
		containerAttempts.Add(1)
		http.Error(w, "container fallback forbidden after host dispatch", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", strings.Replace(srv.URL, "http://", "tcp://", 1))
	t.Setenv("DOCKER_API_VERSION", "1.54")
	dc, err := docker.NewClient("alpha", "same")
	require.NoError(t, err)
	defer dc.Close()
	d := mcpFixture(t)
	store, err := launchermcp.OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	d.mcp = launchermcp.New(store, mcpBackend{d})
	defer d.mcp.Close()
	rt := namespace.NewRuntime(d.activeNs.nsConfig, dc, t.TempDir())
	defer rt.Shutdown()
	rt.InjectAppsForTest(&namespace.AppRuntime{Name: "emodel", ContainerID: "cid", Status: namespace.AppStatusRunning, Def: appdef.ApplicationDef{Name: "emodel", IsJVM: true}})
	d.activeNs.runtime = rt
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := (mcpBackend{d}).Call(ctx, "jvm_command", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Command: "GC.run"})
	require.NoError(t, err)
	require.NoError(t, ctx.Err(), "unknown is caused by the lost response, not cancellation")
	require.Equal(t, "unknown", value.(map[string]any)["outcome"])
	require.Equal(t, false, value.(map[string]any)["replaySafe"])
	require.Zero(t, containerAttempts.Load(), "neither copy nor exec may replay the host command")
	select {
	case got := <-request:
		require.Equal(t, "1\x00jcmd\x00GC.run\x00\x00\x00", got)
	case <-ctx.Done():
		t.Fatal("no host command recorded")
	}
}
