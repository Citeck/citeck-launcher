package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// bootHarness drives the REAL daemon.Start with the slow boot phase replaced by
// a fake that blocks. That is what makes these ordering tests, not handler
// tests: everything before the fake (the single-instance guard, the socket bind,
// the boot handler) is production code running in production order, and moving
// the bind back behind the slow phase makes every one of them fail.
type bootHarness struct {
	socketPath string
	entered    chan struct{}     // closed when the fake slow phase is entered
	release    chan *bootOutcome // the test hands the fake its verdict
	startErr   chan error        // Start's return value
}

// bootOutcome is what the fake slow phase returns: either a daemon (boot
// succeeds and the real routes are installed) or an error (boot aborts).
type bootOutcome struct {
	daemon *Daemon
	err    error
}

func newBootHarness(t *testing.T) *bootHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	// RunDir is /run/citeck in server mode; keep the socket inside the test's
	// own tree (and short enough for the ~108-byte sun_path limit).
	runDir, err := os.MkdirTemp("", "citeck-run")
	require.NoError(t, err)
	t.Setenv("CITECK_RUN", runDir)
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })

	h := &bootHarness{
		socketPath: config.SocketPath(),
		entered:    make(chan struct{}),
		release:    make(chan *bootOutcome, 1),
		startErr:   make(chan error, 1),
	}

	prev := constructDaemonFn
	t.Cleanup(func() { constructDaemonFn = prev })
	constructDaemonFn = func(opts StartOptions, daemonCfg config.DaemonConfig, socketPath string) (*Daemon, error) {
		close(h.entered)
		out := <-h.release
		if out.err != nil {
			return nil, out.err
		}
		out.daemon.socketPath = socketPath
		out.daemon.daemonCfg = daemonCfg
		out.daemon.version = opts.Version
		return out.daemon, nil
	}

	go func() { h.startErr <- Start(StartOptions{Foreground: true, NoUI: true, Version: "test"}) }()

	select {
	case <-h.entered:
	case err := <-h.startErr:
		t.Fatalf("Start returned before the slow boot phase: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the slow boot phase was never entered")
	}
	return h
}

// get performs one request over the daemon's unix socket.
func (h *bootHarness) get(t *testing.T, path string) (resp *http.Response, body []byte) {
	t.Helper()
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", h.socketPath)
			},
		},
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://daemon"+path, http.NoBody)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err, "the socket must answer %s", path)
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, body
}

// finishBoot releases the fake slow phase with a minimal daemon, so Start
// installs the real routes and serves them on the SAME socket.
func (h *bootHarness) finishBoot(t *testing.T) *Daemon {
	t.Helper()
	d := &Daemon{
		activeNs:  &activeNamespace{},
		startTime: time.Now(),
		eventRing: newEventRing(eventReplayBufferSize),
	}
	h.release <- &bootOutcome{daemon: d}
	// The swap happens right after the fake returns; wait for it to be visible.
	require.Eventually(t, func() bool {
		resp, _ := h.get(t, api.DaemonStatus)
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond, "the real routes never replaced the boot handler")
	return d
}

// abortBoot releases the fake slow phase with an error and returns Start's error.
func (h *bootHarness) abortBoot(t *testing.T, err error) error {
	t.Helper()
	h.release <- &bootOutcome{err: err}
	select {
	case startErr := <-h.startErr:
		return startErr
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the boot aborted")
		return nil
	}
}

// THE ordering test. The health gate that judges an auto-updated daemon polls
// this endpoint, and it used to be bound only AFTER the whole boot — the git
// pull, the orphan sweep, the namespace start — so a 90 s boot behind a dead
// Docker socket failed a 60 s gate and rolled a perfectly good 2.11.7 back.
// The fake slow phase is still blocked here: if the bind moves back behind it,
// the dial fails and this test fails.
func TestHealthAnswersWhileTheSlowBootPhaseIsStillRunning(t *testing.T) {
	h := newBootHarness(t)
	resp, body := h.get(t, api.Health)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a booting daemon is alive and must say so")

	var dto api.HealthDto
	require.NoError(t, json.Unmarshal(body, &dto))
	assert.Equal(t, api.HealthStatusStarting, dto.Status, "the answer must name the boot, not claim health")
	assert.False(t, dto.Healthy)

	_ = h.abortBoot(t, errors.New("test over"))
}

// Everything that is not the liveness probe is refused with a machine-readable
// code and a Retry-After, because a client told "running" that then gets a 503
// on its next call is worse off than one told plainly to wait.
func TestEveryOtherRouteIsRefusedWhileBooting(t *testing.T) {
	h := newBootHarness(t)

	// API paths only. A DOCUMENT request is deliberately NOT refused — see
	// TestADocumentRequestWhileBootingGetsTheLoadingPage for why that
	// distinction is the whole fix.
	for _, path := range []string{api.DaemonStatus, api.Namespace, "/api/v1/volumes"} {
		resp, body := h.get(t, path)
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "path %s", path)
		assert.Equal(t, "1", resp.Header.Get("Retry-After"), "path %s must tell the client when to retry", path)
		var errDto api.ErrorDto
		require.NoError(t, json.Unmarshal(body, &errDto), "path %s: %s", path, body)
		assert.Equal(t, api.ErrCodeDaemonStarting, errDto.Code, "path %s", path)
		assert.NotEmpty(t, errDto.Message, "path %s", path)
		assert.NotContains(t, string(body), `"running":true`, "path %s must not fake a running daemon", path)
	}

	_ = h.abortBoot(t, errors.New("test over"))
}

// The white screen reported after an auto-update from 2.12.0 to 2.12.1, and the
// reason the boot refusal cannot cover document requests.
//
// An auto-update replaces the DAEMON only; the wrapper stays whatever version
// the user installed. A 2.12.0 wrapper proxies the daemon's answer straight into
// the webview, so the moment this daemon started answering a document request
// with `503 DAEMON_STARTING`, that JSON became the window's contents — with no
// way forward, because nothing reloads it. Before the early bind there was no
// socket at all during boot, the proxy failed to connect, and the wrapper showed
// its own auto-refreshing loading page instead.
//
// So a booting daemon serves a loading page of its own for anything that is not
// an API call. Old wrappers show it and refresh into the real UI; new ones never
// reach their own fallback. The API refusal is unchanged — an API caller wants
// the honest 503, not HTML.
func TestADocumentRequestWhileBootingGetsTheLoadingPage(t *testing.T) {
	h := newBootHarness(t)

	for _, path := range []string{"/", "/index.html", "/window/logs"} {
		resp, body := h.get(t, path)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "path %s must not refuse the window", path)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html", "path %s", path)
		assert.Contains(t, string(body), "http-equiv=\"refresh\"",
			"path %s must come back on its own once the boot finishes", path)
		assert.NotContains(t, string(body), api.ErrCodeDaemonStarting,
			"path %s must not put the refusal JSON in front of the user", path)
	}

	_ = h.abortBoot(t, errors.New("test over"))
}

// The swap is on the SAME listener and the same socket file: nothing reconnects,
// and the boot handler stops answering the moment the routes are in.
func TestTheRealRoutesReplaceTheBootHandlerOnTheSameSocket(t *testing.T) {
	h := newBootHarness(t)
	before, err := os.Stat(h.socketPath)
	require.NoError(t, err)

	d := h.finishBoot(t)

	resp, body := h.get(t, api.DaemonStatus)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var status api.DaemonStatusDto
	require.NoError(t, json.Unmarshal(body, &status))
	assert.True(t, status.Running, "after boot the daemon answers for itself")
	assert.Empty(t, resp.Header.Get("Retry-After"))

	after, err := os.Stat(h.socketPath)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "the socket file must not be re-created by the swap")

	d.shutdownServer()
	select {
	case startErr := <-h.startErr:
		require.ErrorIs(t, startErr, ErrShutdownRequested)
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}

// A boot that fails after the early bind must leave nothing behind — the socket
// file included, or the next start's single-instance guard has a stale file to
// reason about and `citeck status` dials a socket nobody serves.
func TestAnAbortedBootLeavesNoStaleSocketFile(t *testing.T) {
	h := newBootHarness(t)
	_, err := os.Stat(h.socketPath)
	require.NoError(t, err, "the socket must exist while booting")

	startErr := h.abortBoot(t, errors.New("boom"))
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "boom")

	_, statErr := os.Stat(h.socketPath)
	assert.True(t, os.IsNotExist(statErr), "aborted boot left a stale socket file: %v", statErr)
}

// The early bind must not weaken the single-instance guard: it runs BEFORE the
// bind, and a daemon that is merely booting is still a daemon.
func TestASecondDaemonIsRefusedWhileTheFirstIsStillBooting(t *testing.T) {
	h := newBootHarness(t)

	err := guardSingleDaemonInstance(h.socketPath)
	require.Error(t, err, "a booting daemon already owns the socket")
	assert.Contains(t, err.Error(), "another daemon is already running")

	_, statErr := os.Stat(h.socketPath)
	require.NoError(t, statErr, "the guard must not remove a LIVE socket")

	_ = h.abortBoot(t, errors.New("test over"))
}

// The socket is the daemon's whole access-control story (AGENTS.md: no token
// auth on it because the 0600 mode is the gate), so binding it earlier must not
// widen it.
func TestTheEarlyBoundSocketKeepsItsPrivateMode(t *testing.T) {
	h := newBootHarness(t)
	info, err := os.Stat(h.socketPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "socket mode is %v", info.Mode().Perm())
	_ = h.abortBoot(t, errors.New("test over"))
}
