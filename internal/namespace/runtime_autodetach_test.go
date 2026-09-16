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
		"соседнее приложение должно подняться как обычно")

	qdrant := r.FindApp("qdrant")
	require.NotNil(t, qdrant, "спека остаётся в неймспейсе — иначе её нечем запустить")
	assert.Equal(t, AppStatusStopped, qdrant.Status)
	assert.Empty(t, qdrant.ContainerID, "контейнера быть не должно")

	md.mu.Lock()
	_, created := md.containers["qdrant"]
	md.mu.Unlock()
	assert.False(t, created, "auto-detached приложение не создаёт контейнер само")
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
	t.Fatal("qdrant отсутствует в DTO")
}

// The debug case itself: the operator starts the companion on purpose, and a
// later regeneration — which re-states the same auto-detach, because the owner
// is still detached — must not take it away again.
func TestExplicitStartSurvivesTheNextGeneration(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second))

	require.NoError(t, r.StartApp("qdrant"))
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusRunning, 10*time.Second),
		"явный старт должен перебить auto-detach")

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	assert.Equal(t, AppStatusRunning, r.FindApp("qdrant").Status,
		"перегенерация не должна гасить то, что оператор поднял руками")
}

// Detaching the owner while its companion is RUNNING must actually take the
// container down — that is the memory the "switched-off RAG costs nothing"
// promise is about. Marking it STOPPED and leaving the container up would be a
// ghost: 1 GB of qdrant nothing in the UI accounts for.
func TestCompanionContainerIsStoppedWhenItBecomesAutoDetached(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusRunning, 10*time.Second))

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})

	require.True(t, waitForAppStatus(r, "qdrant", AppStatusStopped, 10*time.Second),
		"companion должен остановиться, а не просто числиться остановленным")
	md.mu.Lock()
	_, alive := md.containers["qdrant"]
	md.mu.Unlock()
	assert.False(t, alive, "контейнер должен быть снят")
}

// ...and it must not be recorded as an OPERATOR detach: manualStoppedApps is
// persisted, so a stop that belongs to the owner's state would outlive it and
// park a re-attached rag in DEPS_WAITING on a qdrant nobody remembers stopping.
func TestAutoDetachIsNotRecordedAsAnOperatorStop(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.Start(autoDetachApps(), false)
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusRunning, 10*time.Second))

	r.SetAutoDetachedApps(map[string]bool{"qdrant": true})
	require.True(t, waitForAppStatus(r, "qdrant", AppStatusStopped, 10*time.Second))

	assert.NotContains(t, r.ManualStoppedApps(), "qdrant",
		"это состояние владельца, а не намерение оператора")
}
