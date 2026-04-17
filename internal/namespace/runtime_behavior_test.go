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
	"github.com/docker/docker/api/types/container"
)

// mockContainer tracks a mock container's ID and labels.
type mockContainer struct {
	id     string
	labels map[string]string
}

// mockDocker implements docker.RuntimeClient for behavioral tests.
type mockDocker struct {
	mu              sync.Mutex
	containers      map[string]mockContainer // app name → container
	nextID          int
	stopRemoveCalls int // number of StopAndRemoveContainer invocations
	removeNetCalls  int // number of RemoveNetwork invocations

	// Test knobs (nil-safe defaults preserve normal behavior).
	pullCalls      int               // incremented on each PullImageWithProgress call
	pullBlock      chan struct{}     // if non-nil, PullImageWithProgress blocks until close or ctx.Done
	stopBlock      chan struct{}     // if non-nil, StopAndRemoveContainer blocks until close or ctx.Done
	stopDelay      time.Duration     // if >0, StopAndRemoveContainer sleeps (honoring ctx) before returning
	removeNetBlock chan struct{}     // if non-nil, RemoveNetwork blocks until close or ctx.Done
	imageExists    map[string]bool   // optional override; nil keeps "always true" default
	imageDigests   map[string]string // optional override; nil keeps "sha256:mock-digest-{img}" default

	// Test knobs for ExecInContainer.
	execCalls int           // incremented on each ExecInContainer call
	execBlock chan struct{} // if non-nil, ExecInContainer blocks until close or ctx.Done

	// Test knob for init-container worker (T19c / T20c coverage).
	// If non-nil, WaitForContainerExit blocks until close or ctx.Done — lets
	// tests pin an app in STARTING(init-phase) while dispatching cmdStopApp /
	// cmdStop to exercise the init-phase stop routing.
	initContainerWaitBlock chan struct{}
}

func newMockDocker() *mockDocker {
	return &mockDocker{containers: make(map[string]mockContainer)}
}

func (m *mockDocker) ContainerName(appName string) string {
	return "test-" + appName
}

func (m *mockDocker) CreateNetwork(ctx context.Context) (string, error) {
	return "mock-network", nil
}

func (m *mockDocker) RemoveNetwork(ctx context.Context) error {
	m.mu.Lock()
	m.removeNetCalls++
	block := m.removeNetBlock
	m.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return fmt.Errorf("mock remove-network: %w", ctx.Err())
		}
	}
	return nil
}

func (m *mockDocker) CreateContainer(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("container-%d", m.nextID)
	m.containers[app.Name] = mockContainer{
		id: id,
		labels: map[string]string{
			"citeck.launcher.app.name": app.Name,
			"citeck.launcher.app.hash": app.GetHash(),
			"citeck.launcher":          "true",
		},
	}
	return id, nil
}

func (m *mockDocker) StartContainer(ctx context.Context, id string) error { return nil }

func (m *mockDocker) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	return nil
}

func (m *mockDocker) RemoveContainer(ctx context.Context, id string) error { return nil }

func (m *mockDocker) StopAndRemoveContainer(ctx context.Context, name string, timeoutSec int) error {
	m.mu.Lock()
	m.stopRemoveCalls++
	block := m.stopBlock
	delay := m.stopDelay
	// Strip the "test-" prefix added by ContainerName so we delete the right key.
	appName := strings.TrimPrefix(name, "test-")
	delete(m.containers, appName)
	m.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return fmt.Errorf("mock stop: %w", ctx.Err())
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("mock stop: %w", ctx.Err())
		}
	}
	return nil
}

func (m *mockDocker) GetContainers(ctx context.Context) ([]container.Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]container.Summary, 0, len(m.containers))
	for name, c := range m.containers {
		result = append(result, container.Summary{
			ID:     c.id,
			Names:  []string{"/" + m.ContainerName(name)},
			Labels: c.labels,
			State:  "running",
		})
	}
	return result, nil
}

func (m *mockDocker) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			State: &container.State{Status: "running", Running: true, StartedAt: time.Now().Format(time.RFC3339)},
		},
		Config: &container.Config{Labels: map[string]string{}},
	}, nil
}

func (m *mockDocker) PullImage(ctx context.Context, img string, auth *docker.RegistryAuth) error {
	return nil
}

func (m *mockDocker) PullImageWithProgress(ctx context.Context, img string, auth *docker.RegistryAuth, progressFn docker.PullProgressFn) error {
	m.mu.Lock()
	m.pullCalls++
	block := m.pullBlock
	m.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return fmt.Errorf("mock pull: %w", ctx.Err())
		}
	}
	return nil
}

func (m *mockDocker) ImageExists(ctx context.Context, img string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.imageExists != nil {
		return m.imageExists[img]
	}
	return true
}

func (m *mockDocker) GetImageDigest(ctx context.Context, img string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.imageDigests != nil {
		if d, ok := m.imageDigests[img]; ok {
			return d
		}
	}
	return "sha256:mock-digest-" + img
}

func (m *mockDocker) ContainerLogs(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}

func (m *mockDocker) ContainerLogsFollow(ctx context.Context, containerID string, tail int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockDocker) ExecInContainer(ctx context.Context, _ string, _ []string) (output string, exitCode int, err error) {
	m.mu.Lock()
	m.execCalls++
	block := m.execBlock
	m.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", 1, fmt.Errorf("mock exec: %w", ctx.Err())
		}
	}
	return "", 0, nil
}

func (m *mockDocker) GetPublishedPort(ctx context.Context, containerID string, containerPort int) int {
	return containerPort
}

func (m *mockDocker) GetContainerIP(ctx context.Context, containerID string) string {
	return "172.18.0.2"
}

func (m *mockDocker) ContainerStats(ctx context.Context, containerID string) (*docker.ContainerStat, error) {
	return &docker.ContainerStat{CPUPercent: 1.0, MemUsage: 100 * 1024 * 1024, MemLimit: 512 * 1024 * 1024}, nil
}

func (m *mockDocker) WaitForContainerExit(ctx context.Context, containerID string, timeout time.Duration) error {
	m.mu.Lock()
	block := m.initContainerWaitBlock
	m.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return fmt.Errorf("mock wait-for-exit: %w", ctx.Err())
		}
	}
	return nil
}

func testConfig() *Config {
	return &Config{
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

// waitForStatus blocks until the runtime reaches the target namespace status.
// 10ms cadence is well under the 1s runtimeLoop tickerPeriod and below the
// signalCh debounce — tests observe transitions within a single tick without
// meaningfully loading the scheduler.
func waitForStatus(r *Runtime, target NsRuntimeStatus, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if r.Status() == target {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ticker.C:
		case <-time.After(time.Until(deadline)):
			return r.Status() == target
		}
	}
}

// waitForAppStatus blocks until the named app reaches the target status.
// See waitForStatus for the rationale behind the poll-based implementation.
func waitForAppStatus(r *Runtime, appName string, target AppRuntimeStatus, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if app := r.FindApp(appName); app != nil && app.Status == target {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ticker.C:
		case <-time.After(time.Until(deadline)):
			app := r.FindApp(appName)
			return app != nil && app.Status == target
		}
	}
}

func TestStartAndStop(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
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
	r := NewRuntime(testConfig(), md, t.TempDir())
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

func TestStopAppMarksDetachedAndPersists(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")})
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING")
	}

	if err := r.StopApp("foo"); err != nil {
		t.Fatalf("StopApp: %v", err)
	}

	// Status should transition to STOPPED
	if !waitForAppStatus(r, "foo", AppStatusStopped, 5*time.Second) {
		t.Fatalf("app should be STOPPED after StopApp")
	}

	// Marked as manually stopped (public snapshot API, not direct field access).
	if _, detached := r.ManualStoppedApps()["foo"]; !detached {
		t.Fatalf("StopApp should mark app as manualStoppedApps")
	}

	// StartApp should clear the detach flag
	if err := r.StartApp("foo"); err != nil {
		t.Fatalf("StartApp: %v", err)
	}
	if _, stillDetached := r.ManualStoppedApps()["foo"]; stillDetached {
		t.Fatalf("StartApp should remove app from manualStoppedApps")
	}
}

func TestWaitForDepsSkipsDetached(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Pre-mark app-a as detached before start — like a template default
	r.SetManualStoppedApps(map[string]bool{"app-a": true})

	// B depends on A (detached)
	apps := []appdef.ApplicationDef{
		simpleApp("app-a", "image-a:1"),
		simpleApp("app-b", "image-b:1", "app-a"),
	}
	r.Start(apps)

	// B should reach RUNNING because detached A is treated as satisfied
	if !waitForAppStatus(r, "app-b", AppStatusRunning, 10*time.Second) {
		app := r.FindApp("app-b")
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app-b should reach RUNNING when dependency is detached, got %s", status)
	}

	// A should be STOPPED (detached)
	a := r.FindApp("app-a")
	if a == nil || a.Status != AppStatusStopped {
		status := "nil"
		if a != nil {
			status = string(a.Status)
		}
		t.Fatalf("app-a should be STOPPED (detached), got %s", status)
	}
}

func TestRegeneratePreservesRunning(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
	}

	r.Start(apps)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// Track create count before regenerate
	md.mu.Lock()
	createCountBefore := md.nextID
	md.mu.Unlock()

	// Regenerate with same app definition — container should be preserved (hash match)
	r.Regenerate(apps, nil, nil)

	// Wait for regeneration to start and complete
	waitForStatus(r, NsStatusStarting, 5*time.Second)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING after regenerate")
	}

	// Container should NOT have been recreated (hash match → preserve running container)
	md.mu.Lock()
	createCountAfterPreserve := md.nextID
	md.mu.Unlock()

	if createCountAfterPreserve != createCountBefore {
		t.Fatalf("container should NOT have been recreated for unchanged app, create count before=%d after=%d", createCountBefore, createCountAfterPreserve)
	}
}

func TestRegenerateRestartsChanged(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
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
	r.Regenerate(apps2, nil, nil)

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
	r := NewRuntime(testConfig(), md, t.TempDir())
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

// TestDetachLeavesContainersRunning verifies that ShutdownDetached exits the
// runtime without stopping or removing any containers — the binary-upgrade
// path that lets the next daemon adopt the live platform.
func TestDetachLeavesContainersRunning(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
		simpleApp("mongo", "mongo:4"),
	}

	r.Start(apps)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING, got %v", r.Status())
	}

	// Snapshot containers + stop counter so we can assert detach left them alone.
	md.mu.Lock()
	containersBefore := len(md.containers)
	stopCallsBefore := md.stopRemoveCalls
	netCallsBefore := md.removeNetCalls
	md.mu.Unlock()

	if containersBefore != len(apps) {
		t.Fatalf("expected %d containers before detach, got %d", len(apps), containersBefore)
	}

	r.ShutdownDetached()

	// runLoop must have exited (running flag clear).
	if r.running.Load() {
		t.Fatalf("runtime still marked as running after ShutdownDetached")
	}

	md.mu.Lock()
	containersAfter := len(md.containers)
	stopCallsAfter := md.stopRemoveCalls
	netCallsAfter := md.removeNetCalls
	md.mu.Unlock()

	if containersAfter != containersBefore {
		t.Fatalf("detach removed containers: before=%d after=%d", containersBefore, containersAfter)
	}
	if stopCallsAfter != stopCallsBefore {
		t.Fatalf("detach called StopAndRemoveContainer: before=%d after=%d", stopCallsBefore, stopCallsAfter)
	}
	if netCallsAfter != netCallsBefore {
		t.Fatalf("detach called RemoveNetwork: before=%d after=%d", netCallsBefore, netCallsAfter)
	}
}

// TestDetachWhileStopping verifies that asking for detach after a stop is
// already in flight degrades into a regular shutdown wait — the runtime
// must NOT leave containers running when it's already committed to a stop.
//
// Drive the STOPPING state via the public Stop() API. A stopBlock on
// mockDocker holds the graceful-shutdown chain in STOPPING long enough for
// the Detach() probe to observe it.
func TestDetachWhileStopping(t *testing.T) {
	md := newMockDocker()

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer func() {
		// Release any leftover stopBlock so the deferred Shutdown() can
		// complete.
		md.mu.Lock()
		if md.stopBlock != nil {
			close(md.stopBlock)
			md.stopBlock = nil
		}
		md.mu.Unlock()
		r.Shutdown()
	}()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
	}

	r.Start(apps)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// Install stopBlock AFTER RUNNING but BEFORE Stop() — doStop dispatches
	// stopContainer workers that read md.stopBlock under md.mu and block on
	// the channel, holding the runtime in STOPPING long enough for the
	// Detach() probe. Installing it after RUNNING avoids interfering with
	// the best-effort pre-create cleanup during start.
	md.mu.Lock()
	md.stopBlock = make(chan struct{})
	md.mu.Unlock()

	// Initiate stop via public API. doStop transitions NS → STOPPING and
	// dispatches stopContainer workers, which block on md.stopBlock.
	r.Stop()

	// Wait for the NS to enter STOPPING (usually <100ms).
	if !waitForStatus(r, NsStatusStopping, 5*time.Second) {
		t.Fatalf("namespace did not reach STOPPING after Stop()")
	}

	// Detach must refuse — stop is committed.
	if r.Detach() {
		t.Fatalf("Detach() returned true while status was STOPPING")
	}

	// The deferred cleanup releases stopBlock and drives Shutdown() through.
}

// TestDetachThenAdopt verifies the full upgrade flow: detach the runtime,
// create a fresh runtime against the same mock docker, and confirm the
// new runtime adopts the existing containers without recreating them.
func TestDetachThenAdopt(t *testing.T) {
	md := newMockDocker()
	tmpDir := t.TempDir()
	r1 := NewRuntime(testConfig(), md, tmpDir)

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
		simpleApp("mongo", "mongo:4"),
	}

	r1.Start(apps)
	if !waitForStatus(r1, NsStatusRunning, 10*time.Second) {
		t.Fatalf("first runtime did not reach RUNNING")
	}

	md.mu.Lock()
	createsBefore := md.nextID
	md.mu.Unlock()

	r1.ShutdownDetached()

	// Containers must still exist after detach.
	md.mu.Lock()
	if len(md.containers) != len(apps) {
		md.mu.Unlock()
		t.Fatalf("expected %d containers to survive detach, got %d", len(apps), len(md.containers))
	}
	md.mu.Unlock()

	// Spin up a fresh runtime against the same mock — simulates a new
	// daemon process. doStart must reuse running containers (hash match)
	// instead of creating new ones.
	r2 := NewRuntime(testConfig(), md, tmpDir)
	defer r2.Shutdown()

	r2.Start(apps)
	if !waitForStatus(r2, NsStatusRunning, 10*time.Second) {
		t.Fatalf("second runtime did not reach RUNNING after adopt")
	}

	md.mu.Lock()
	createsAfter := md.nextID
	md.mu.Unlock()

	if createsAfter != createsBefore {
		t.Fatalf("new runtime created new containers instead of adopting: createsBefore=%d createsAfter=%d", createsBefore, createsAfter)
	}
}
