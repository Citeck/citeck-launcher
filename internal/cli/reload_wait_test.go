package cli

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
)

// getsAfterTheReload counts the /namespace polls the CLI made once the reload
// had been accepted — i.e. how much of a wait it performed.
func getsAfterTheReload(t *testing.T, fd *fakeDaemon) int {
	t.Helper()
	seen, after := false, 0
	for _, req := range fd.recorded() {
		switch {
		case req == http.MethodPost+" "+api.NamespaceReload:
			seen = true
		case seen && req == http.MethodGet+" "+api.Namespace:
			after++
		}
	}
	require.True(t, seen, "the reload was never sent")
	return after
}

// The defect: a reload on a STOPPED namespace never starts anything, so the
// wait had nothing to wait for and hung until Ctrl+C. There is nothing to
// watch — say so and return.
func TestReload_OnAStoppedNamespaceDoesNotWaitAtAll(t *testing.T) {
	fd := &fakeDaemon{
		ns: func(int) (api.NamespaceDto, bool) {
			return nsWith(api.NsStatusStopped, api.AppStatusStopped, api.AppStatusStopped), true
		},
		reload: api.ActionResultDto{Success: true, Message: "Configuration reloaded"},
	}
	c := newFakeDaemon(t, fd)

	require.NoError(t, runReload(c, false, fastWait(liveStatusOpts{})))

	assert.Equal(t, []string{
		http.MethodGet + " " + api.Namespace, // the pre-command sample
		http.MethodPost + " " + api.NamespaceReload,
	}, fd.recorded(), "a stopped namespace is sampled once, reloaded, and not watched")
}

// STOPPING is the same domain: the namespace is on its way down, and a reload
// does not bring it back up.
func TestReload_OnAStoppingNamespaceDoesNotWaitEither(t *testing.T) {
	fd := &fakeDaemon{
		ns: func(int) (api.NamespaceDto, bool) {
			return nsWith(api.NsStatusStopping, api.AppStatusStopping), true
		},
		reload: api.ActionResultDto{Success: true, Message: "Configuration reloaded"},
	}
	c := newFakeDaemon(t, fd)

	require.NoError(t, runReload(c, false, fastWait(liveStatusOpts{})))
	assert.Zero(t, getsAfterTheReload(t, fd))
}

// A running namespace really does restart services, and watching them is the
// whole point of a foreground reload.
func TestReload_OnARunningNamespaceStillStreams(t *testing.T) {
	fd := &fakeDaemon{
		ns: func(n int) (api.NamespaceDto, bool) {
			if n <= 3 {
				return nsWith(api.NsStatusRunning, api.AppStatusStarting, api.AppStatusRunning), true
			}
			return nsWith(api.NsStatusRunning, api.AppStatusRunning, api.AppStatusRunning), true
		},
		reload: api.ActionResultDto{Success: true, Message: "Configuration reloaded"},
	}
	c := newFakeDaemon(t, fd)

	require.NoError(t, runReload(c, false, fastWait(liveStatusOpts{})))
	assert.GreaterOrEqual(t, getsAfterTheReload(t, fd), 3,
		"the reload of a running namespace is watched until its apps settle")
}

// The skip is an optimization of a KNOWN-stopped namespace. A pre-sample we
// could not take says nothing, so the wait happens as before.
func TestReload_APreSampleThatFailedStillStreams(t *testing.T) {
	fd := &fakeDaemon{
		ns: func(n int) (api.NamespaceDto, bool) {
			if n == 1 {
				return api.NamespaceDto{}, false // the sample the skip would be decided on
			}
			return nsWith(api.NsStatusRunning, api.AppStatusRunning, api.AppStatusRunning), true
		},
		reload: api.ActionResultDto{Success: true, Message: "Configuration reloaded"},
	}
	c := newFakeDaemon(t, fd)

	require.NoError(t, runReload(c, false, fastWait(liveStatusOpts{})))
	assert.GreaterOrEqual(t, getsAfterTheReload(t, fd), 1)
}

// --detach never waits, so it has no reason to sample either.
func TestReload_DetachSamplesNothing(t *testing.T) {
	fd := &fakeDaemon{
		ns: func(int) (api.NamespaceDto, bool) {
			return nsWith(api.NsStatusStopped, api.AppStatusStopped), true
		},
		reload: api.ActionResultDto{Success: true, Message: "Configuration reloaded"},
	}
	c := newFakeDaemon(t, fd)

	require.NoError(t, runReload(c, true, fastWait(liveStatusOpts{})))
	assert.Equal(t, []string{http.MethodPost + " " + api.NamespaceReload}, fd.recorded())
}
