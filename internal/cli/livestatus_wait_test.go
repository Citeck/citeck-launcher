package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
)

// fakeDaemon is a stand-in daemon for the wait loop. It answers
// GET /namespace from a script keyed on the call number and records every
// request it received, which is how these tests tell "waited" from "did not
// wait" without measuring wall clock.
type fakeDaemon struct {
	mu       sync.Mutex
	srv      *httptest.Server
	requests []string
	gets     int

	// ns answers the n-th (1-based) GET /namespace. A nil second return means
	// "refuse this call" — the daemon is not reachable right now.
	ns func(n int) (api.NamespaceDto, bool)

	reload api.ActionResultDto
}

func newFakeDaemon(t *testing.T, fd *fakeDaemon) *client.DaemonClient {
	t.Helper()
	fd.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fd.mu.Lock()
		fd.requests = append(fd.requests, r.Method+" "+r.URL.Path)
		isGet := r.Method == http.MethodGet && r.URL.Path == api.Namespace
		if isGet {
			fd.gets++
		}
		n := fd.gets
		fd.mu.Unlock()

		switch {
		case isGet:
			ns, ok := fd.ns(n)
			if !ok {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"message":"namespace unavailable"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(ns)
		case r.Method == http.MethodPost && r.URL.Path == api.NamespaceReload:
			_ = json.NewEncoder(w).Encode(fd.reload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fd.srv.Close)

	c, err := client.New(client.Options{Host: fd.srv.Listener.Addr().String()})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func (f *fakeDaemon) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

func (f *fakeDaemon) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// close makes the daemon unreachable (not merely unhealthy), the way a killed
// or restarting daemon is.
func (f *fakeDaemon) close() { f.srv.Close() }

func nsWith(status string, appStatuses ...string) api.NamespaceDto {
	apps := make([]api.AppDto, 0, len(appStatuses))
	for i, s := range appStatuses {
		apps = append(apps, api.AppDto{Name: string(rune('a' + i)), Status: s, Kind: "CITECK_CORE"})
	}
	return api.NamespaceDto{ID: "ns", Name: "ns", Status: status, Apps: apps}
}

// fastWait is the same wait every caller performs, driven in milliseconds.
func fastWait(opts liveStatusOpts) liveStatusOpts {
	opts.pollInterval = 10 * time.Millisecond
	if opts.unproductiveGrace == 0 {
		opts.unproductiveGrace = 100 * time.Millisecond
	}
	if opts.daemonFailureBudget == 0 {
		opts.daemonFailureBudget = 10 * time.Second
	}
	return opts
}

// waitTestTimeout is far longer than any of these scripts needs: a wait that
// is still running at this point is one that never ends.
const waitTestTimeout = 5 * time.Second

// runWait runs streamLiveStatus off the test goroutine so a loop that never
// ends fails the test instead of hanging the package.
func runWait(t *testing.T, c *client.DaemonClient, opts liveStatusOpts) (error, bool) { //nolint:revive // (err, returned) reads better here than a named type
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- streamLiveStatus(c, opts) }()
	select {
	case err := <-done:
		return err, true
	case <-time.After(waitTestTimeout):
		return nil, false
	}
}

// A namespace that cannot move is the `citeck reload` hang: the daemon
// accepted the command, nothing drains it while the namespace is stopped, and
// the wait had no bound at all. The wait ends; the command did not fail.
func TestStreamLiveStatus_GivesUpOnANamespaceParkedInStopped(t *testing.T) {
	fd := &fakeDaemon{ns: func(int) (api.NamespaceDto, bool) {
		return nsWith(api.NsStatusStopped, api.AppStatusStopped, api.AppStatusStopped), true
	}}
	c := newFakeDaemon(t, fd)

	start := time.Now()
	err, returned := runWait(t, c, fastWait(liveStatusOpts{unproductiveGrace: 150 * time.Millisecond}))
	require.True(t, returned, "the wait must end on a namespace that is not going anywhere")
	require.NoError(t, err, "the command succeeded; only the wait ended")
	assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond,
		"it must wait out the grace first — a namespace mid-command reads STOPPED for a moment")
}

// `ns.Updating` is the daemon saying it is acting on the click: the reloadMu
// wait, the git pull, the bundle resolve. The namespace is legitimately still
// STOPPED throughout, and on a big bundle that stretch runs for minutes.
func TestStreamLiveStatus_KeepsWaitingWhileTheDaemonReportsUpdating(t *testing.T) {
	const updatingCalls = 40 // 400ms at the test cadence — far past the grace
	fd := &fakeDaemon{ns: func(n int) (api.NamespaceDto, bool) {
		if n <= updatingCalls {
			ns := nsWith(api.NsStatusStopped, api.AppStatusStopped, api.AppStatusStopped)
			ns.Updating = true
			return ns, true
		}
		return nsWith(api.NsStatusRunning, api.AppStatusRunning, api.AppStatusRunning), true
	}}
	c := newFakeDaemon(t, fd)

	err, returned := runWait(t, c, fastWait(liveStatusOpts{unproductiveGrace: 100 * time.Millisecond}))
	require.True(t, returned)
	require.NoError(t, err)
	assert.Greater(t, fd.getCount(), updatingCalls,
		"giving up during Updating would have ended the wait before the namespace ever started")
}

// --follow is the user asking to keep watching. It stays unbounded.
func TestStreamLiveStatus_FollowNeverGivesUp(t *testing.T) {
	fd := &fakeDaemon{ns: func(int) (api.NamespaceDto, bool) {
		return nsWith(api.NsStatusStopped, api.AppStatusStopped), true
	}}
	c := newFakeDaemon(t, fd)

	done := make(chan error, 1)
	go func() {
		done <- streamLiveStatus(c, fastWait(liveStatusOpts{
			follow:              true,
			unproductiveGrace:   50 * time.Millisecond,
			daemonFailureBudget: 100 * time.Millisecond,
		}))
	}()

	select {
	case err := <-done:
		t.Fatalf("--follow must keep watching a stopped namespace, returned %v", err)
	case <-time.After(600 * time.Millisecond): // 12x the grace
	}

	// The only way out of a follow is the daemon going away (or Ctrl+C).
	fd.close()
	select {
	case err := <-done:
		require.Error(t, err, "a daemon that stopped answering is a failure, not a clean end")
	case <-time.After(5 * time.Second):
		t.Fatal("the follow did not end after the daemon went away")
	}
}

// A daemon we can no longer reach is a failure: we do not know anything about
// the namespace any more, so the wait must not report success.
func TestStreamLiveStatus_LostDaemonEndsInAnError(t *testing.T) {
	fd := &fakeDaemon{ns: func(int) (api.NamespaceDto, bool) { return api.NamespaceDto{}, false }}
	c := newFakeDaemon(t, fd)

	err, returned := runWait(t, c, fastWait(liveStatusOpts{daemonFailureBudget: 100 * time.Millisecond}))
	require.True(t, returned, "an unreachable daemon must not be retried forever")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "daemon")
	assert.Greater(t, fd.getCount(), 1, "one refusal is not a lost daemon")
}

// `citeck upgrade` swaps the binary and waits right after, so the daemon
// drops out and comes back — twice, if the swap restarts it again. The budget
// bounds a CONTINUOUS outage, so it has to be re-armed by every success.
func TestStreamLiveStatus_SurvivesOutagesShorterThanTheBudget(t *testing.T) {
	fd := &fakeDaemon{ns: func(n int) (api.NamespaceDto, bool) {
		switch {
		case n <= 5: // first outage: 50ms
			return api.NamespaceDto{}, false
		case n <= 15: // back, still coming up: 100ms
			return nsWith(api.NsStatusStarting, api.AppStatusStarting, api.AppStatusStarting), true
		case n <= 20: // second outage: 50ms, at ~200ms into the wait
			return api.NamespaceDto{}, false
		}
		return nsWith(api.NsStatusRunning, api.AppStatusRunning, api.AppStatusRunning), true
	}}
	c := newFakeDaemon(t, fd)

	err, returned := runWait(t, c, fastWait(liveStatusOpts{
		daemonFailureBudget: 120 * time.Millisecond,
		unproductiveGrace:   10 * time.Second,
	}))
	require.True(t, returned)
	require.NoError(t, err, "no single outage came near the budget")
	assert.Greater(t, fd.getCount(), 20)
}

// The grace bounds a CONTINUOUS unproductive run, not the wait as a whole: a
// namespace on its way back up passes through the stop domain more than once
// (`citeck restart` reads STOPPED, then STARTING, then STOPPED again while the
// apps cycle), and none of those stretches means nothing is happening.
func TestStreamLiveStatus_UnproductiveRunsAreReArmedByProgress(t *testing.T) {
	fd := &fakeDaemon{ns: func(n int) (api.NamespaceDto, bool) {
		switch {
		case n <= 5: // in the stop domain: 50ms
			return nsWith(api.NsStatusStopped, api.AppStatusStopped, api.AppStatusStopped), true
		case n <= 15: // coming up: 100ms
			return nsWith(api.NsStatusStarting, api.AppStatusStarting, api.AppStatusStarting), true
		case n <= 20: // back in the stop domain: 50ms, at ~200ms into the wait
			return nsWith(api.NsStatusStopped, api.AppStatusStopped, api.AppStatusStopped), true
		}
		return nsWith(api.NsStatusRunning, api.AppStatusRunning, api.AppStatusRunning), true
	}}
	c := newFakeDaemon(t, fd)

	err, returned := runWait(t, c, fastWait(liveStatusOpts{
		unproductiveGrace:   120 * time.Millisecond,
		daemonFailureBudget: 10 * time.Second,
	}))
	require.True(t, returned)
	require.NoError(t, err)
	assert.Greater(t, fd.getCount(), 20,
		"no single unproductive stretch came near the grace, so the wait must have run to the end")
}

// nsHeld is a namespace where `rag` is parked by a detached `zookeeper`: the
// shape `citeck start rag` hits when qdrant (or any hard dependency) is
// stopped.
func nsHeld(held bool) api.NamespaceDto {
	return api.NamespaceDto{ID: "ns", Name: "ns", Status: "STARTING", Apps: []api.AppDto{
		{Name: "zookeeper", Status: "STOPPED", Kind: "THIRD_PARTY"},
		{Name: "rag", Status: "DEPS_WAITING", Held: held, Kind: "CITECK_CORE",
			WaitingFor: []api.WaitingDepDto{{App: "zookeeper", Status: "STOPPED"}}},
	}}
}

// A held app never reaches RUNNING on its own — RestartApp is an explicit no-op
// on DEPS_WAITING — and this loop has no deadline at all, only Ctrl+C. So the
// wait has to end on the first poll that sees the hold.
func TestStreamSingleAppStatus_EndsOnAHeldApp(t *testing.T) {
	ensureI18n()
	fd := &fakeDaemon{ns: func(int) (api.NamespaceDto, bool) { return nsHeld(true), true }}
	c := newFakeDaemon(t, fd)

	done := make(chan error, 1)
	go func() { done <- streamSingleAppStatus(c, "rag") }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(waitTestTimeout):
		t.Fatal("the wait on a held app never finished, and it never will")
	}
	assert.Equal(t, 1, fd.getCount(), "the verdict is visible on the very first poll")
}

// The same app WITHOUT the daemon's held verdict is genuinely pending: the wait
// must poll again rather than return, or `citeck start <app>` would finish the
// moment a dependency is momentarily not RUNNING. Asserted by ending the wait
// on the SECOND answer — a loop that treated any DEPS_WAITING as terminal would
// return after the first and never ask for it.
func TestStreamSingleAppStatus_KeepsWaitingWhenTheAppIsNotHeld(t *testing.T) {
	ensureI18n()
	fd := &fakeDaemon{ns: func(n int) (api.NamespaceDto, bool) {
		if n == 1 {
			return nsHeld(false), true
		}
		ns := nsHeld(false)
		ns.Apps[1].Status = "RUNNING"
		ns.Apps[1].WaitingFor = nil
		return ns, true
	}}
	c := newFakeDaemon(t, fd)

	done := make(chan error, 1)
	go func() { done <- streamSingleAppStatus(c, "rag") }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(waitTestTimeout):
		t.Fatal("the wait did not finish even after the app reached RUNNING")
	}
	assert.GreaterOrEqual(t, fd.getCount(), 2,
		"without the daemon's verdict DEPS_WAITING is not a terminal state")
}
