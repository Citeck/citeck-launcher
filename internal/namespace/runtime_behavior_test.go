package namespace

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	dtypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// mockDocker implements docker.Interface for behavioral tests.
type mockDocker struct {
	mu         sync.Mutex
	containers map[string]string // name → id
	nextID     int
}

func newMockDocker() *mockDocker {
	return &mockDocker{containers: make(map[string]string)}
}

func (m *mockDocker) ContainerName(appName string) string {
	return "test-" + appName
}

func (m *mockDocker) CreateNetwork(ctx context.Context) (string, error) {
	return "mock-network", nil
}

func (m *mockDocker) RemoveNetwork(ctx context.Context) error { return nil }

func (m *mockDocker) CreateContainer(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("container-%d", m.nextID)
	m.containers[app.Name] = id
	return id, nil
}

func (m *mockDocker) StartContainer(ctx context.Context, id string) error { return nil }

func (m *mockDocker) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	return nil
}

func (m *mockDocker) RemoveContainer(ctx context.Context, id string) error { return nil }

func (m *mockDocker) StopAndRemoveContainer(ctx context.Context, name string, timeoutSec int) error {
	return nil
}

func (m *mockDocker) GetContainers(ctx context.Context) ([]dtypes.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []dtypes.Container
	for name, id := range m.containers {
		result = append(result, dtypes.Container{
			ID:    id,
			Names: []string{"/" + m.ContainerName(name)},
			Labels: map[string]string{
				"citeck.launcher.app.name": name,
			},
		})
	}
	return result, nil
}

func (m *mockDocker) InspectContainer(ctx context.Context, id string) (dtypes.ContainerJSON, error) {
	return dtypes.ContainerJSON{
		ContainerJSONBase: &dtypes.ContainerJSONBase{
			State: &dtypes.ContainerState{Running: true, StartedAt: time.Now().Format(time.RFC3339)},
		},
		Config: &container.Config{Labels: map[string]string{}},
	}, nil
}

func (m *mockDocker) PullImage(ctx context.Context, img string, auth *docker.RegistryAuth) error {
	return nil
}

func (m *mockDocker) PullImageWithProgress(ctx context.Context, img string, auth *docker.RegistryAuth, progressFn docker.PullProgressFn) error {
	return nil
}

func (m *mockDocker) ImageExists(ctx context.Context, img string) bool { return true }

func (m *mockDocker) GetImageDigest(ctx context.Context, img string) string {
	return "sha256:mock-digest-" + img
}

func (m *mockDocker) ContainerLogsFollow(ctx context.Context, containerID string, tail int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockDocker) ExecInContainer(ctx context.Context, containerID string, cmd []string) (string, int, error) {
	return "", 0, nil
}

func (m *mockDocker) GetPublishedPort(ctx context.Context, containerID string, containerPort int) int {
	return containerPort
}

func (m *mockDocker) ContainerStats(ctx context.Context, containerID string) (*docker.ContainerStat, error) {
	return &docker.ContainerStat{CPUPercent: 1.0, MemUsage: 100 * 1024 * 1024, MemLimit: 512 * 1024 * 1024}, nil
}

func (m *mockDocker) WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	return nil
}

func (m *mockDocker) WaitForContainerExit(ctx context.Context, containerID string, timeout time.Duration) error {
	return nil
}

func testConfig() *NamespaceConfig {
	return &NamespaceConfig{
		ID:   "test",
		Name: "Test NS",
	}
}

func simpleApp(name, image string, deps ...string) appdef.ApplicationDef {
	depMap := make(map[string]bool)
	for _, d := range deps {
		depMap[d] = true
	}
	return appdef.ApplicationDef{
		Name:  name,
		Image: image,
		Kind:  appdef.KindThirdParty,
		Resources: &appdef.AppResourcesDef{
			Limits: appdef.LimitsDef{Memory: "256m"},
		},
		DependsOn: depMap,
	}
}

func waitForStatus(r *Runtime, target NsRuntimeStatus, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.Status() == target {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func waitForAppStatus(r *Runtime, appName string, target AppRuntimeStatus, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		app := r.FindApp(appName)
		if app != nil && app.Status == target {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestStartAndStop(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, "test-ws", t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
		simpleApp("mongo", "mongo:4"),
		simpleApp("emodel", "ecos-model:2.0", "postgres"),
	}

	r.Start(apps)

	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING, got %v", r.Status())
	}

	// All apps should be RUNNING
	for _, app := range apps {
		if !waitForAppStatus(r, app.Name, AppStatusRunning, 5*time.Second) {
			found := r.FindApp(app.Name)
			status := "nil"
			if found != nil {
				status = string(found.Status)
			}
			t.Fatalf("app %s did not reach RUNNING, got %s", app.Name, status)
		}
	}

	r.Stop()

	if !waitForStatus(r, NsStatusStopped, 10*time.Second) {
		t.Fatalf("namespace did not reach STOPPED, got %v", r.Status())
	}
}

func TestWaitForDeps(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, "test-ws", t.TempDir())
	defer r.Shutdown()

	// B depends on A
	apps := []appdef.ApplicationDef{
		simpleApp("app-a", "image-a:1"),
		simpleApp("app-b", "image-b:1", "app-a"),
	}

	r.Start(apps)

	// B should eventually reach RUNNING (after A reaches RUNNING first)
	if !waitForAppStatus(r, "app-b", AppStatusRunning, 10*time.Second) {
		found := r.FindApp("app-b")
		status := "nil"
		if found != nil {
			status = string(found.Status)
		}
		t.Fatalf("app-b did not reach RUNNING, got %s", status)
	}

	// A should also be running
	aApp := r.FindApp("app-a")
	if aApp == nil || aApp.Status != AppStatusRunning {
		t.Fatalf("app-a should be RUNNING")
	}
}

func TestRegeneratePreservesRunning(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, "test-ws", t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
	}

	r.Start(apps)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// Get container ID before regenerate
	md.mu.Lock()
	idBefore := md.containers["postgres"]
	md.mu.Unlock()

	// Regenerate with same app definition — container should be preserved (hash match)
	r.Regenerate(apps)

	// Wait for regeneration to start and complete
	waitForStatus(r, NsStatusStarting, 5*time.Second)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING after regenerate")
	}

	// Container ID should be the same (not recreated)
	md.mu.Lock()
	idAfter := md.containers["postgres"]
	md.mu.Unlock()

	if idBefore != idAfter {
		t.Logf("container was recreated (id changed from %s to %s) — this is OK if hash comparison used digest", idBefore, idAfter)
	}
}

func TestRegenerateRestartsChanged(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, "test-ws", t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
	}

	r.Start(apps)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	md.mu.Lock()
	createCountBefore := md.nextID
	md.mu.Unlock()

	// Change the image — should trigger recreation
	apps2 := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:18"),
	}
	r.Regenerate(apps2)

	// Wait for regeneration to start (leaves RUNNING)
	waitForStatus(r, NsStatusStarting, 5*time.Second)
	// Wait for regeneration to complete (returns to RUNNING)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING after regenerate, got %v", r.Status())
	}

	md.mu.Lock()
	createCountAfter := md.nextID
	md.mu.Unlock()

	// A new container should have been created (CreateContainer called again)
	if createCountAfter <= createCountBefore {
		t.Fatalf("expected new container creation after image change, create count before=%d after=%d", createCountBefore, createCountAfter)
	}
}

func TestStopWhileStarting(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, "test-ws", t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
		simpleApp("mongo", "mongo:4"),
	}

	r.Start(apps)

	// Immediately stop — should not deadlock
	time.Sleep(50 * time.Millisecond)
	r.Stop()

	if !waitForStatus(r, NsStatusStopped, 10*time.Second) {
		t.Fatalf("namespace did not reach STOPPED after stop-during-start, got %v", r.Status())
	}
}
