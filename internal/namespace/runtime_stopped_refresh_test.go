package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/require"
)

func TestRefreshStoppedAppsPreservesStopAndHistory(t *testing.T) {
	rt := NewRuntime(&Config{ID: "stopped"}, nil, t.TempDir())
	t.Cleanup(rt.Shutdown)
	require.True(t, rt.RefreshStoppedApps([]appdef.ApplicationDef{{Name: "content", Image: "content:1"}, {Name: "removed"}}))
	rt.mu.Lock()
	rt.apps["content"].RestartCount = 3
	rt.mu.Unlock()
	require.True(t, rt.RefreshStoppedApps([]appdef.ApplicationDef{{Name: "content", Image: "content:2"}, {Name: "seaweedfs"}}))
	require.Equal(t, NsStatusStopped, rt.Status())
	require.False(t, rt.running.Load())
	require.Nil(t, rt.FindApp("removed"))
	require.Equal(t, 3, rt.FindApp("content").RestartCount)
	require.Equal(t, "content:2", rt.FindApp("content").Def.Image)
	require.Equal(t, AppStatusStopped, rt.FindApp("seaweedfs").Status)
	rt.mu.Lock()
	rt.status = NsStatusRunning
	rt.mu.Unlock()
	refreshed := rt.RefreshStoppedApps([]appdef.ApplicationDef{{Name: "unexpected"}})
	rt.mu.Lock()
	rt.status = NsStatusStopped
	rt.mu.Unlock()
	require.False(t, refreshed, "a running namespace must only be changed through its loop")
	require.Nil(t, rt.FindApp("unexpected"))
	require.NotNil(t, rt.FindApp("seaweedfs"))
}
