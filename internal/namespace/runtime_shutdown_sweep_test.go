package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSweepLeftoverContainersRemovesNamespaceContainers: the terminal shutdown
// sweep force-removes every container still present for the namespace — the
// safety net for a per-app stop+remove that left a container Exited (its app
// landed in STOPPING_FAILED, which the shutdown chain treats as terminal).
func TestSweepLeftoverContainersRemovesNamespaceContainers(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	md.mu.Lock()
	md.containers["eapps"] = mockContainer{id: "c-eapps"}
	md.containers["emodel"] = mockContainer{id: "c-emodel"}
	md.mu.Unlock()

	r.sweepLeftoverContainers()

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.ElementsMatch(t, []string{"c-eapps", "c-emodel"}, md.removedContainerIDs)
	assert.Empty(t, md.containers, "all namespace containers must be force-removed by the sweep")
}

// TestSweepLeftoverContainersRetriesForceRemove: a transient RemoveContainer
// failure is retried (force-remove) until it succeeds, so a flaky remove during
// shutdown doesn't strand the container.
func TestSweepLeftoverContainersRetriesForceRemove(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	md.mu.Lock()
	md.containers["eapps"] = mockContainer{id: "c-eapps"}
	md.removeFailFirst = 1 // first remove errors, retry succeeds
	md.mu.Unlock()

	r.sweepLeftoverContainers()

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, []string{"c-eapps", "c-eapps"}, md.removedContainerIDs, "remove is retried after a transient failure")
	assert.Empty(t, md.containers, "container is removed once the retry succeeds")
}

// TestRemoveNetworkPlanSweepsLeftoversFirst: the terminal RemoveNetwork plan
// sweeps leftover containers BEFORE removing the network, so a STOPPING_FAILED
// leftover (e.g. eapps) can't survive shutdown or block RemoveNetwork with a
// dangling endpoint.
func TestRemoveNetworkPlanSweepsLeftoversFirst(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	md.mu.Lock()
	md.containers["eapps"] = mockContainer{id: "c-eapps"} // leftover from a failed stop+remove
	md.mu.Unlock()

	res := r.makeRemoveNetworkPlan().fn(context.Background())
	require.NoError(t, res.Err)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Contains(t, md.removedContainerIDs, "c-eapps", "RemoveNetwork plan must sweep leftover containers first")
	assert.Equal(t, 1, md.removeNetCalls)
}
