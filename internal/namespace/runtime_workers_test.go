package namespace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/nsactions"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// workerMockDocker is a programmable docker.RuntimeClient stub used by the
// worker-factory tests. It tracks call counts, lets each method's behavior be
// overridden via a function field, and synthesizes a minimal set of "happy
// path" returns otherwise.
type workerMockDocker struct {
	mu sync.Mutex

	// Call counters.
	pullCalls          int
	imageExistsCalls   int
	getDigestCalls     int
	createCalls        int
	startCalls         int
	stopAndRemoveCalls int
	removeCalls        int
	waitForExitCalls   int
	logsFollowCalls    int
	execCalls          int

	// Programmable fields.
	imageExists        bool
	digest             string
	containerID        string
	pullErr            error
	pullErrTimes       int           // first N pull calls error, then succeed
	pullBlock          chan struct{} // if non-nil, PullImageWithProgress waits on it or ctx
	createErr          error
	createErrTimes     int // first N create calls fail, then succeed
	startErr           error
	stopErr            error
	waitExitErr        error
	stopBlock          chan struct{}
	logsContent        string
	customProgressData []struct {
		cur, tot float64
		pct      int
	}
}

func newWorkerMockDocker() *workerMockDocker {
	return &workerMockDocker{
		imageExists: false,
		digest:      "sha256:mock-digest",
		containerID: "container-xyz",
	}
}

func (m *workerMockDocker) ContainerName(appName string) string { return "test-" + appName }

func (m *workerMockDocker) CreateNetwork(_ context.Context) (string, error) { return "net", nil }
func (m *workerMockDocker) RemoveNetwork(_ context.Context) error           { return nil }

func (m *workerMockDocker) CreateContainer(ctx context.Context, _ appdef.ApplicationDef, _ string) (string, error) {
	m.mu.Lock()
	m.createCalls++
	call := m.createCalls
	wantErr := m.createErr
	errTimes := m.createErrTimes
	id := m.containerID
	m.mu.Unlock()
	if wantErr != nil && call <= errTimes {
		return "", wantErr
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("mock create: %w", err)
	}
	return id, nil
}

func (m *workerMockDocker) StartContainer(_ context.Context, _ string) error {
	m.mu.Lock()
	m.startCalls++
	err := m.startErr
	m.mu.Unlock()
	return err
}

func (m *workerMockDocker) StopContainer(_ context.Context, _ string, _ int) error { return nil }

func (m *workerMockDocker) RemoveContainer(_ context.Context, _ string) error {
	m.mu.Lock()
	m.removeCalls++
	m.mu.Unlock()
	return nil
}

func (m *workerMockDocker) StopAndRemoveContainer(ctx context.Context, _ string, _ int) error {
	m.mu.Lock()
	m.stopAndRemoveCalls++
	block := m.stopBlock
	wantErr := m.stopErr
	m.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return fmt.Errorf("mock stop: %w", ctx.Err())
		}
	}
	return wantErr
}

func (m *workerMockDocker) GetContainers(_ context.Context) ([]container.Summary, error) {
	return nil, nil
}

func (m *workerMockDocker) InspectContainer(_ context.Context, _ string) (container.InspectResponse, error) {
	return container.InspectResponse{}, nil
}

func (m *workerMockDocker) PullImage(_ context.Context, _ string, _ *docker.RegistryAuth) error {
	return nil
}

func (m *workerMockDocker) PullImageWithProgress(
	ctx context.Context,
	_ string,
	_ *docker.RegistryAuth,
	progressFn docker.PullProgressFn,
) error {
	m.mu.Lock()
	m.pullCalls++
	call := m.pullCalls
	errTimes := m.pullErrTimes
	wantErr := m.pullErr
	block := m.pullBlock
	progress := m.customProgressData
	m.mu.Unlock()

	if progressFn != nil {
		for _, p := range progress {
			progressFn(p.cur, p.tot, p.pct)
		}
	}

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return fmt.Errorf("mock pull: %w", ctx.Err())
		}
	}

	if wantErr != nil && call <= errTimes {
		return wantErr
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mock pull: %w", err)
	}
	return nil
}

func (m *workerMockDocker) ImageExists(_ context.Context, _ string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.imageExistsCalls++
	return m.imageExists
}

func (m *workerMockDocker) GetImageDigest(_ context.Context, _ string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getDigestCalls++
	return m.digest
}

func (m *workerMockDocker) ContainerLogs(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}

func (m *workerMockDocker) ContainerLogsFollow(_ context.Context, _ string, _ int) (io.ReadCloser, error) {
	m.mu.Lock()
	m.logsFollowCalls++
	content := m.logsContent
	m.mu.Unlock()
	// Return a single-frame stdcopy-wrapped stream so waitForLogPattern's
	// demuxer yields the content on the first read. Easiest path: bypass
	// stdcopy by forging a valid stdout frame header (8 bytes: stream=1,
	// 3 zero bytes, then 4-byte big-endian length).
	return io.NopCloser(strings.NewReader(stdcopyFrame(content))), nil
}

// stdcopyFrame wraps payload in a single Docker multiplex stdout frame so that
// stdcopy.StdCopy unwraps it cleanly. Used by the startup-probe tests to feed
// synthetic log lines through r.waitForLogPattern.
func stdcopyFrame(payload string) string {
	n := len(payload)
	// 8-byte header: [stream(1=stdout), 0, 0, 0, len3, len2, len1, len0]
	hdr := []byte{1, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	return string(hdr) + payload
}

func (m *workerMockDocker) ExecInContainer(_ context.Context, _ string, _ []string) (output string, exitCode int, err error) {
	m.mu.Lock()
	m.execCalls++
	m.mu.Unlock()
	return "", 0, nil
}

func (m *workerMockDocker) GetPublishedPort(_ context.Context, _ string, p int) int { return p }
func (m *workerMockDocker) GetContainerIP(_ context.Context, _ string) string       { return "1.2.3.4" }

func (m *workerMockDocker) ContainerStats(_ context.Context, _ string) (*docker.ContainerStat, error) {
	return &docker.ContainerStat{}, nil
}

func (m *workerMockDocker) WaitForContainer(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *workerMockDocker) WaitForContainerExit(_ context.Context, _ string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waitForExitCalls++
	return m.waitExitErr
}

// newWorkerTestRuntime spins up a Runtime that is never Start()'d — we call
// factories' TaskFuncs directly. Worker factories don't touch r.apps /
// r.status / the runtimeLoop, so a bare-minimum runtime is enough.
//
// The returned Runtime still owns a Dispatcher + dispatchLoop goroutine
// (created by NewRuntimeWithActions). Tests that don't need Dispatcher +
// the dispatch-loop should close r.eventCh themselves to let dispatchLoop
// exit; see t.Cleanup below.
func newWorkerTestRuntime(t *testing.T, md *workerMockDocker) *Runtime {
	t.Helper()
	r := NewRuntime(&Config{ID: "tt"}, md, t.TempDir())
	t.Cleanup(func() {
		// Ensure dispatchLoop exits: close eventCh (it iterates until the
		// channel closes). Avoid double-close by guarding with a nil check
		// is not needed — Shutdown has not been called so eventCh is still open.
		close(r.eventCh)
	})
	return r
}

// --- Pull TaskFunc tests ---

func TestPullTaskSkipsPullWhenImageExists(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = true
	md.digest = "sha256:cached"

	r := newWorkerTestRuntime(t, md)
	plan := r.makePullPlan("app1", "nexus.citeck.ru/img:1", false, nil)

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)
	p, ok := res.Payload.(workers.PullPayload)
	require.True(t, ok)
	assert.Equal(t, "sha256:cached", p.Digest)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 0, md.pullCalls, "no pull when ImageExists=true AND pullAlways=false")
}

func TestPullTaskPullsImageWhenMissing(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = false
	md.digest = "sha256:fresh"

	r := newWorkerTestRuntime(t, md)
	plan := r.makePullPlan("app1", "nexus.citeck.ru/img:1", false, nil)

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)
	p, ok := res.Payload.(workers.PullPayload)
	require.True(t, ok)
	assert.Equal(t, "sha256:fresh", p.Digest)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 1, md.pullCalls)
}

func TestPullTaskPullsWhenPullAlwaysEvenIfLocal(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = true // local copy exists
	md.digest = "sha256:new"

	r := newWorkerTestRuntime(t, md)
	plan := r.makePullPlan("app1", "nexus.citeck.ru/img:1", true, nil)

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 1, md.pullCalls, "pullAlways must force a pull even with local image")
}

func TestPullTaskAuthErrorWraps(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = false
	md.pullErr = errors.New("error response from daemon: pull access denied")
	md.pullErrTimes = 10 // always fail

	r := newWorkerTestRuntime(t, md)
	plan := r.makePullPlan("app1", "nexus.citeck.ru/ecos-model:1.0", false, nil)

	res := plan.fn(context.Background())
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "authentication failed")
	assert.Contains(t, res.Err.Error(), "docker login nexus.citeck.ru")
}

func TestPullTaskFallsBackToLocalAfterNRetries(t *testing.T) {
	md := newWorkerMockDocker()
	// All pulls fail (non-auth). Image exists locally → fall back after the
	// 4th attempt, matching legacy nsactions.PullExecutor + actions.Service
	// (Execute called for actx.Attempt = 0, 1, 2, 3; the `attempt >= 3` check
	// in PullExecutor.Execute becomes true on attempt 3, AFTER its pull has
	// already run and failed).
	md.imageExists = true
	md.pullErr = errors.New("network timeout")
	md.pullErrTimes = 10 // would always fail, but we expect early fallback

	r := newWorkerTestRuntime(t, md)
	// pullAlways=true so the short-circuit at top doesn't skip the pull.
	plan := r.makePullPlan("app1", "nexus.citeck.ru/img:1", true, nil)

	res := plan.fn(context.Background())
	require.NoError(t, res.Err, "after N retries with local image available, should succeed")

	md.mu.Lock()
	defer md.mu.Unlock()
	// PullRetriesForExistingImage = 3 → 4 actual pull attempts (0, 1, 2, 3)
	// before fallback. Matches nsactions.PullExecutor under actions.Service.
	assert.Equal(t, nsactions.PullRetriesForExistingImage+1, md.pullCalls,
		"PullRetriesForExistingImage+1 (=4) attempts before local fallback, matching legacy")
}

func TestPullTaskExhaustsRetriesWithoutLocalFallback(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = false // no local image → no fallback path
	md.pullErr = errors.New("network timeout")
	md.pullErrTimes = 100

	r := newWorkerTestRuntime(t, md)
	plan := r.makePullPlan("app1", "nexus.citeck.ru/img:1", true, nil)

	// Shorten the retry sleeps so the test runs fast. We use a short-lived
	// context to force exit after a reasonable number of attempts. The retry
	// budget is len(PullRetryDelays) == 5, so with 1s+1s+1s+5s+10s = 18s of
	// backoff we'd exceed the default test timeout. Bound via ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	res := plan.fn(ctx)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "pull nexus.citeck.ru/img:1")
}

func TestPullTaskCancelMidRetry(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = false
	md.pullErr = errors.New("network timeout")
	md.pullErrTimes = 100

	r := newWorkerTestRuntime(t, md)
	plan := r.makePullPlan("app1", "nexus.citeck.ru/img:1", true, nil)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel quickly so we abort during the first retry-sleep (1s delay).
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := plan.fn(ctx)
	elapsed := time.Since(start)

	require.Error(t, res.Err)
	require.ErrorIs(t, res.Err, context.Canceled)
	assert.Less(t, elapsed, 600*time.Millisecond, "ctx cancel should abort retry-sleep promptly")
}

// --- Start TaskFunc tests ---

func TestStartTaskCreatesAndStarts(t *testing.T) {
	md := newWorkerMockDocker()
	md.containerID = "container-abc"

	r := newWorkerTestRuntime(t, md)
	def := appdef.ApplicationDef{Name: "web", Image: "img:1"}
	plan := r.makeStartPlan("web", def, "/vols")

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)
	p, ok := res.Payload.(workers.StartPayload)
	require.True(t, ok)
	assert.Equal(t, "container-abc", p.ContainerID)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 1, md.stopAndRemoveCalls, "stale container stop+remove should run once")
	assert.Equal(t, 1, md.createCalls)
	assert.Equal(t, 1, md.startCalls)
}

func TestStartTaskRetryOnCreateConflict(t *testing.T) {
	md := newWorkerMockDocker()
	md.createErr = errors.New("container name conflict")
	md.createErrTimes = 2 // first 2 create calls fail, 3rd succeeds
	md.containerID = "container-ok"

	r := newWorkerTestRuntime(t, md)
	def := appdef.ApplicationDef{Name: "web", Image: "img:1"}
	plan := r.makeStartPlan("web", def, "/vols")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res := plan.fn(ctx)
	require.NoError(t, res.Err)
	p, _ := res.Payload.(workers.StartPayload)
	assert.Equal(t, "container-ok", p.ContainerID)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 3, md.createCalls, "expected 2 failed + 1 success")
	assert.Equal(t, 1, md.startCalls)
}

func TestStartTaskFailsWhenStartContainerErrors(t *testing.T) {
	md := newWorkerMockDocker()
	md.startErr = errors.New("no such image")

	r := newWorkerTestRuntime(t, md)
	def := appdef.ApplicationDef{Name: "web", Image: "img:1"}
	plan := r.makeStartPlan("web", def, "/vols")

	res := plan.fn(context.Background())
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "start container web")
}

// --- Stop TaskFunc tests ---

func TestStopTaskSucceedsFirstTry(t *testing.T) {
	md := newWorkerMockDocker()

	r := newWorkerTestRuntime(t, md)
	plan := r.makeStopPlan("web", "test-web", 0)

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)
	_, ok := res.Payload.(workers.StopPayload)
	assert.True(t, ok)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 1, md.stopAndRemoveCalls)
}

func TestStopTaskRetriesOnError(t *testing.T) {
	md := newWorkerMockDocker()
	md.stopErr = errors.New("transient docker hiccup")

	r := newWorkerTestRuntime(t, md)
	plan := r.makeStopPlan("web", "test-web", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := plan.fn(ctx)
	require.Error(t, res.Err)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 3, md.stopAndRemoveCalls, "should retry exactly 3 times (attempt 0 + 2 retries)")
}

func TestStopTaskCancelAbortsRetrySleep(t *testing.T) {
	md := newWorkerMockDocker()
	md.stopErr = errors.New("docker unavailable")

	r := newWorkerTestRuntime(t, md)
	plan := r.makeStopPlan("web", "test-web", 0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := plan.fn(ctx)
	elapsed := time.Since(start)

	require.Error(t, res.Err)
	require.ErrorIs(t, res.Err, context.Canceled)
	assert.Less(t, elapsed, 600*time.Millisecond)
}

// --- Init container TaskFunc tests ---

func TestInitContainerTaskHappyPath(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = false // force a pull of the init image
	md.containerID = "init-abc"

	r := newWorkerTestRuntime(t, md)
	initDef := appdef.ApplicationDef{Name: "web-init", Image: "init:1", IsInit: true}
	plan := r.makeInitContainerPlan("web", "init:1", 0, initDef, "/vols")

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)
	_, ok := res.Payload.(workers.InitPayload)
	assert.True(t, ok)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.GreaterOrEqual(t, md.pullCalls, 1, "init image must be pulled")
	assert.Equal(t, 1, md.stopAndRemoveCalls, "stale init container stop+remove")
	assert.Equal(t, 1, md.createCalls)
	assert.Equal(t, 1, md.startCalls)
	assert.Equal(t, 1, md.waitForExitCalls)
	assert.Equal(t, 1, md.removeCalls, "cleanup removes init container after exit")
}

func TestInitContainerTaskCleanupOnWaitFailure(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = true // skip pull
	md.containerID = "init-bad"
	md.waitExitErr = errors.New("init container exited non-zero")

	r := newWorkerTestRuntime(t, md)
	initDef := appdef.ApplicationDef{Name: "web-init", Image: "init:1", IsInit: true}
	plan := r.makeInitContainerPlan("web", "init:1", 0, initDef, "/vols")

	res := plan.fn(context.Background())
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "init container")

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 1, md.removeCalls, "cleanup must still run on WaitForExit failure")
}

func TestInitContainerTaskCleanupOnStartFailure(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = true // skip pull
	md.startErr = errors.New("start failed")

	r := newWorkerTestRuntime(t, md)
	initDef := appdef.ApplicationDef{Name: "web-init", Image: "init:1", IsInit: true}
	plan := r.makeInitContainerPlan("web", "init:1", 0, initDef, "/vols")

	res := plan.fn(context.Background())
	require.Error(t, res.Err)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 1, md.removeCalls, "cleanup must run when StartContainer fails")
	assert.Equal(t, 0, md.waitForExitCalls, "wait-for-exit skipped after start failure")
}

func TestInitContainerTaskPullFailure(t *testing.T) {
	md := newWorkerMockDocker()
	md.imageExists = false
	md.pullErr = errors.New("network down")
	md.pullErrTimes = 100

	r := newWorkerTestRuntime(t, md)
	initDef := appdef.ApplicationDef{Name: "web-init", Image: "init:1", IsInit: true}
	plan := r.makeInitContainerPlan("web", "init:1", 0, initDef, "/vols")

	// Bounded ctx so InitPullRetryDelays don't stretch the test runtime.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	res := plan.fn(ctx)

	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "pull init image")

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 0, md.createCalls, "must not create init container when pull fails")
	assert.Equal(t, 0, md.removeCalls, "no cleanup needed — init container never created")
}

// --- Startup probe TaskFunc tests ---

func TestStartupProbeTaskMatchesLogPattern(t *testing.T) {
	md := newWorkerMockDocker()
	md.logsContent = "some noise\nService started on port 8080\nmore logs\n"

	r := newWorkerTestRuntime(t, md)
	conds := []appdef.StartupCondition{
		{Log: &appdef.LogStartupCondition{Pattern: "Service started", TimeoutSeconds: 5}},
	}
	plan := r.makeStartupProbePlan("web", "container-xyz", conds)

	res := plan.fn(context.Background())
	require.NoError(t, res.Err)
	_, ok := res.Payload.(workers.ProbePayload)
	assert.True(t, ok)
}

func TestStartupProbeTaskFailsWhenPatternMissing(t *testing.T) {
	md := newWorkerMockDocker()
	md.logsContent = "nothing matches here\n"

	r := newWorkerTestRuntime(t, md)
	conds := []appdef.StartupCondition{
		{Log: &appdef.LogStartupCondition{Pattern: "will-never-appear", TimeoutSeconds: 1}},
	}
	plan := r.makeStartupProbePlan("web", "container-xyz", conds)

	res := plan.fn(context.Background())
	require.Error(t, res.Err)
}

// --- TaskID shape sanity ---

func TestWorkerPlansEmitCorrectTaskIDs(t *testing.T) {
	md := newWorkerMockDocker()
	r := newWorkerTestRuntime(t, md)

	pull := r.makePullPlan("app", "img:1", false, nil)
	assert.Equal(t, workers.TaskID{App: "app", Op: workers.OpPull}, pull.taskID)

	start := r.makeStartPlan("app", appdef.ApplicationDef{Name: "app", Image: "img:1"}, "/v")
	assert.Equal(t, workers.TaskID{App: "app", Op: workers.OpStart}, start.taskID)

	// Vary stopTimeout (5 vs the default-0 used elsewhere) so the unparam
	// linter sees the parameter taking distinct values across the test suite.
	stop := r.makeStopPlan("app", "test-app", 5)
	assert.Equal(t, workers.TaskID{App: "app", Op: workers.OpStop}, stop.taskID)

	// Vary image string (different from the "init:1" used by the
	// init-container test cases) so unparam sees real diversity.
	init := r.makeInitContainerPlan("app", "init:2", 0,
		appdef.ApplicationDef{Name: "app-init", Image: "init:2", IsInit: true}, "/v")
	assert.Equal(t, workers.TaskID{App: "app", Op: workers.OpInit}, init.taskID)

	probe := r.makeStartupProbePlan("app", "cid", nil)
	assert.Equal(t, workers.TaskID{App: "app", Op: workers.OpProbe}, probe.taskID)
}

// --- Dispatcher-level cancel-of-pull coverage ---

// TestDispatcherCancelAppStopsPulls confirms that CancelApp delivered during
// an in-flight OpPull task promptly cancels the pull's ctx, and that pulls
// on a DIFFERENT app are NOT affected. Uses REAL makePullPlan TaskFuncs (not
// synthetic closures) so the test proves end-to-end that the pull-cancellation
// path through workerMockDocker's PullImageWithProgress.pullBlock +
// select-on-ctx unwinds correctly all the way back to the dispatcher's results
// channel.
func TestDispatcherCancelAppStopsPulls(t *testing.T) {
	md := newWorkerMockDocker()
	// Force pulls to enter the blocking select inside PullImageWithProgress.
	md.pullBlock = make(chan struct{})
	// imageExists=false so the runPullTask short-circuit doesn't skip the
	// pull, and so the per-attempt local-image fallback never activates.
	md.imageExists = false

	r := newWorkerTestRuntime(t, md)
	// Wire a results channel into r.dispatcher (we ignore the runtimeLoop's
	// r.resultCh — runtimeLoop isn't running here; we collect Results directly).
	results := make(chan workers.Result, 4)

	app1, app2 := "alfresco", "ecos-model"
	plan1 := r.makePullPlan(app1, "nexus.citeck.ru/img1:1", true, nil)
	plan2 := r.makePullPlan(app2, "nexus.citeck.ru/img2:1", true, nil)

	r.dispatcher.Dispatch(plan1.taskID, plan1.fn, results, r.signalCh)
	r.dispatcher.Dispatch(plan2.taskID, plan2.fn, results, r.signalCh)

	// Wait for both pulls to be in-flight (PullImageWithProgress increments
	// pullCalls before entering the blocking select).
	require.Eventually(t, func() bool {
		md.mu.Lock()
		defer md.mu.Unlock()
		return md.pullCalls >= 2
	}, 2*time.Second, 10*time.Millisecond, "both pulls must be in-flight before CancelApp")

	// Cancel every non-OpStop task for app1. OpPull is not in the exceptOps
	// list, so the app1 pull must be canceled.
	canceled := r.dispatcher.CancelApp(app1, workers.CancelExternalStop, workers.OpStop)
	require.Equal(t, 1, canceled)

	// App1's pull must return promptly with a ctx error wrapped by
	// runPullTask ("pull <image>: context canceled").
	select {
	case res := <-results:
		assert.Equal(t, app1, res.TaskID.App)
		require.Error(t, res.Err)
		require.ErrorIs(t, res.Err, context.Canceled)
		assert.Contains(t, res.Err.Error(), "pull nexus.citeck.ru/img1:1")
	case <-time.After(2 * time.Second):
		t.Fatal("app1 pull did not abort after CancelApp")
	}

	// App2's pull must still be blocked — its ctx is untouched. Release it
	// by closing pullBlock and assert it returns success.
	close(md.pullBlock)

	select {
	case res := <-results:
		assert.Equal(t, app2, res.TaskID.App)
		require.NoError(t, res.Err)
		_, ok := res.Payload.(workers.PullPayload)
		assert.True(t, ok, "successful pull yields PullPayload")
	case <-time.After(2 * time.Second):
		t.Fatal("app2 pull did not finish after releasing pullBlock")
	}

	// Total pull invocations: 1 for app1 (canceled mid-call), 1 for app2.
	// The cancel happened inside PullImageWithProgress's select-on-ctx, so
	// app1's pull is observed exactly once (no retries — runPullTask sees
	// ctx.Err() in the next loop iteration's top check or in the post-pull
	// select, both of which return the wrapped ctx error immediately).
	md.mu.Lock()
	defer md.mu.Unlock()
	assert.Equal(t, 2, md.pullCalls,
		"each app should have exactly one pull invocation (no retries on cancel)")
}
