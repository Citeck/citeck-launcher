// Behavioral tests for AUTO-DETACHED apps: a companion (qdrant, stt-sidecar)
// the generator emits while its owner (rag, ai) is detached. The spec must be
// in the namespace so it can be started deliberately — that is what running the
// owner from an IDE needs — and it must never start on its own.
package namespace

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autoDetachApps() []appdef.ApplicationDef {
	return []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
		simpleApp("qdrant", "qdrant/qdrant:v1.19.1"),
	}
}

// The whole point of the feature: everyone who simply has RAG switched off must
// not find a vector store running on their machine after an update.
func TestAutoDetachedAppIsNeverStartedByItself(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	r.Start(autoDetachApps(), false)

	require.True(t, waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second),
		"the neighboring app must come up as usual")

	qdrant := r.FindApp("qdrant")
	require.NotNil(t, qdrant, "the spec stays in the namespace, or there is nothing to start")
	assert.Equal(t, AppStatusStopped, qdrant.Status)
	assert.Empty(t, qdrant.ContainerID, "there must be no container")

	md.mu.Lock()
	_, created := md.containers["qdrant"]
	md.mu.Unlock()
	assert.False(t, created, "an auto-detached app creates no container by itself")
}

// The UI needs to tell "detached, press play" from "stopped and about to be
// started", and the only signal it has is AppDto.Detached.
func TestAutoDetachedAppReportsItselfDetached(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second))

	for _, app := range r.ToNamespaceDto().Apps {
		if app.Name == "qdrant" {
			assert.True(t, app.Detached)
			return
		}
	}
	t.Fatal("qdrant is missing from the DTO")
}

// The debug case itself: the operator starts the companion on purpose, and a
// later regeneration — which re-states the same auto-detach, because the owner
// is still detached — must not take it away again. Nothing remembers the
// operator's click: the app is simply no longer stopped, and the verdict only
// ever reaches apps that are.
func TestExplicitStartSurvivesTheNextGeneration(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second))

	require.NoError(t, r.StartApp("qdrant"))
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusRunning, 10*time.Second),
		"an explicit start must override auto-detach")

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	assert.Equal(t, AppStatusRunning, r.FindApp("qdrant").Status,
		"a regeneration must not take down what the operator started by hand")
}

// Detaching the owner does NOT reach back for a companion that is already
// RUNNING. Stopping it is the operator's move — the rule only decides what
// starts by itself, and a running container is something somebody started.
func TestAutoDetachDoesNotTouchARunningCompanion(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusRunning, 10*time.Second))

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})

	assert.Equal(t, AppStatusRunning, r.FindApp("qdrant").Status,
		"the verdict must not take down what is already running")
	md.mu.Lock()
	_, alive := md.containers["qdrant"]
	md.mu.Unlock()
	assert.True(t, alive, "the container must stay")
	assert.False(t, r.ToNamespaceDto().Apps[0].Detached || r.IsAppDetached("qdrant"),
		"a running app stays under the loop rather than counting as detached")
}

// ...and nothing about it is recorded as an operator stop either: that map is
// persisted, and the owner's state has no business in it.
func TestAutoDetachNeverWritesTheOperatorsDetachSet(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusRunning, 10*time.Second))

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})

	assert.NotContains(t, r.ManualStoppedApps(), "qdrant",
		"this is derived state, not the operator's intent")
}
