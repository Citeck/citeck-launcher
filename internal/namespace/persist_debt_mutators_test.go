package namespace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// Every mutator that persists inline records durable user intent — a detach, a
// re-attach, an edited ApplicationDef, an edited mounted file, a cleared
// restart badge. Each of them ran persistState() and then cleared r.dirty
// whatever the store answered, so a refused write was dropped on the spot:
// nothing retried it, and the intent lived in memory until the process
// restarted. They all hold the same lock at the same point in the same shape,
// so they all get the same treatment — persistUnderLock, which leaves the debt
// marked for the loop tail.
//
// Each case asserts the pair that makes "still owed" mean something: the
// runtime holds the change in memory, and it knows the write has not landed.
func TestAFailedInlineWriteIsStillOwed(t *testing.T) {
	const app = "gateway"
	baseDef := appdef.ApplicationDef{Name: app, Image: "gw:1"}

	// newRuntime hands back a runtime whose store always refuses, with one
	// live app in the requested status and a generated baseline behind it.
	newRuntime := func(t *testing.T, status AppRuntimeStatus) *Runtime {
		t.Helper()
		r := NewRuntime(testConfig(), newMockDocker(), t.TempDir())
		r.SetGeneratedDefs([]appdef.ApplicationDef{baseDef})
		r.InjectAppsForTest(&AppRuntime{Name: app, Status: status, Def: baseDef, ContainerID: "c1"})
		r.SetStatePersister(failingPersister{})
		// The call under test must be the only thing that can raise the flag.
		r.dirty.Store(false)
		return r
	}

	editedFile := func(t *testing.T, r *Runtime) (relPath, absPath string, content, template []byte) {
		t.Helper()
		relPath = "app/gateway/props/application-launcher.yml"
		absPath = filepath.Join(r.volumesBase, filepath.FromSlash(relPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(absPath), 0o755))
		return relPath, absPath, []byte("server:\n  port: 8080\n"), []byte("server:\n  port: 80\n")
	}

	t.Run("UpdateAppDef", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		require.NoError(t, r.UpdateAppDef(app, appdef.ApplicationDef{Name: app, Image: "gw:2"}, true))
		assert.NotNil(t, r.AppPatch(app), "the edit is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("ResetAppDef", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		r.mu.Lock()
		r.editedAppPatches[app] = json.RawMessage(`{"image":"gw:2"}`)
		r.mu.Unlock()
		require.NoError(t, r.ResetAppDef(app))
		assert.Nil(t, r.AppPatch(app), "the reset is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("WriteEditedFile", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		relPath, absPath, content, template := editedFile(t, r)
		require.NoError(t, r.WriteEditedFile(relPath, absPath, content, template))
		assert.True(t, r.IsFileEdited(relPath), "the file edit is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("ResetEditedFile", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		relPath, absPath, content, template := editedFile(t, r)
		require.NoError(t, r.WriteEditedFile(relPath, absPath, content, template))
		r.dirty.Store(false)
		require.NoError(t, r.ResetEditedFile(app, relPath))
		assert.False(t, r.IsFileEdited(relPath), "the reset is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("StopApp", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		require.NoError(t, r.StopApp(app))
		assert.True(t, r.ManualStoppedApps()[app], "the detach intent is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("StartApp", func(t *testing.T) {
		r := newRuntime(t, AppStatusStopped)
		r.mu.Lock()
		r.manualStoppedApps[app] = true
		r.mu.Unlock()
		require.NoError(t, r.StartApp(app))
		assert.False(t, r.ManualStoppedApps()[app], "the re-attach is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	// RestartApp persists on two branches: the one that stops a live container
	// first, and the direct re-entry for an app with nothing in flight. Both
	// refuse to run on a runtime that was never started, which it only reads
	// off r.runCtx — so that is what the two cases stand up.
	t.Run("RestartApp/running", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		r.mu.Lock()
		r.runCtx = context.Background()
		r.manualStoppedApps[app] = true
		r.mu.Unlock()
		require.NoError(t, r.RestartApp(app))
		assert.False(t, r.ManualStoppedApps()[app], "the re-attach is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("RestartApp/stopped", func(t *testing.T) {
		r := newRuntime(t, AppStatusStopped)
		r.mu.Lock()
		r.runCtx = context.Background()
		r.manualStoppedApps[app] = true
		r.mu.Unlock()
		require.NoError(t, r.RestartApp(app))
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})

	t.Run("ClearRestartEvents", func(t *testing.T) {
		r := newRuntime(t, AppStatusRunning)
		r.mu.Lock()
		r.restartEvents = []RestartEvent{{App: app, Reason: "oom"}}
		r.restartCounts = map[string]int{app: 3}
		r.mu.Unlock()
		r.ClearRestartEvents("")
		assert.Empty(t, r.RestartEvents(), "the clear is held in memory whatever the store did")
		assert.True(t, r.dirty.Load(), "the write is still owed")
	})
}
