package daemon

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The daemon selects ecos-content's built-in S3 storage by mutating only
// default-content-storage. Generation supplies native endpoint/secret env.
//
// Runs are single-flight, gated by RUNNING platform/provider apps, canceled
// on namespace switches, and serialized with destructive data operations.
// Readiness is polled for up to 15 minutes; failures retry automatically after
// 5 minutes, with the route allowing an earlier attempt. Every run re-reads ECOS.
// A previously confirmed switch followed by LOCAL is an administrator choice.
// An unfinished switch is persisted before writing and confirmed atomically
// with its history after reading the actual resulting default.
//
// No code takes configMu while holding p.mu: active snapshots are read first.

const (
	// contentStorageEventType is the SSE event broadcast whenever the state
	// changes, so the banner follows without polling. Before and After carry
	// the previous and the new state, NamespaceID the namespace.
	contentStorageEventType = "namespace_content_storage"

	// csConfigRef is ecos-content's default storage setting (an eapps config
	// of emodel) and csLocalStorageRef the built-in database storage it names
	// out of the box. Every ref carries its app: a ref without one defaults to
	// `alfresco`.
	csConfigRef       = "eapps/config@app/emodel$default-content-storage"
	csLocalStorageRef = "emodel/content-storage@LOCAL"

	csPollInterval = 15 * time.Second
	csReadyTimeout = 15 * time.Minute
	csRetryAfter   = 5 * time.Minute
)

// csPlatformApps are the ECOS apps the connection talks to. The provider is
// added per namespace (bundle.ContentStorageS3Config.App).
var csPlatformApps = []string{appdef.AppGateway, appdef.AppEmodel, appdef.AppEapps, appdef.AppContent}

// ContentStorageStatus is the last known state of one namespace's connection.
type ContentStorageStatus struct {
	// State is one of the api.ContentStorageState* values.
	State string
	// ErrorMsg is why the last attempt failed (State "error"), as a sentence
	// the HTTP boundary renders in the reader's language; empty otherwise.
	ErrorMsg msg.Message
	// At is when State last changed; zero before any change.
	At time.Time
}

// contentStorageProvisioner owns the connection job of the ACTIVE namespace.
// One per daemon (Daemon.contentStorageProv), created on first use so a
// zero-value test Daemon has one too.
type contentStorageProvisioner struct {
	d *Daemon

	// Seams. Production values are set by newContentStorageProvisioner;
	// tests replace them before the first event.
	records      func(addr string) ecosRecords
	gatewayAddr  func(ctx context.Context, rt *namespace.Runtime) (string, error)
	now          func() time.Time
	pollInterval time.Duration
	readyTimeout time.Duration
	retryAfter   time.Duration

	// kickQueued coalesces a burst of runtime events into one evaluation.
	kickQueued atomic.Bool
	// epoch invalidates evaluations that captured the previous active namespace.
	epoch atomic.Uint64

	mu sync.Mutex
	// wsID/nsID is the namespace status belongs to; "" before anything
	// happened. Status for any other namespace answers pending, so a namespace
	// switch can never show the previous namespace's verdict even before
	// Reset ran.
	wsID   string
	nsID   string
	status ContentStorageStatus
	// job is the run in flight, nil when none: the single-flight guard. A job
	// only ever writes state while it is still THIS pointer, which is what
	// makes a canceled or superseded run harmless.
	job *csJob
	// ranSinceUp is the latch: true once a run has started since the platform
	// last came up, false again as soon as it is seen down.
	ranSinceUp bool
	// lastRunAt is when the last run started, for the error-retry throttle.
	lastRunAt  time.Time
	retryTimer *time.Timer
}

// csJob is one run.
type csJob struct {
	in     csJobInput
	cancel context.CancelFunc
	done   chan struct{}
	// prior is the state when the run started. A run that re-verifies a
	// connected or admin-managed namespace keeps showing that verdict instead
	// of flashing "waiting" on every restart of the platform.
	prior string
}

// csJobInput is the namespace a run works for, snapshotted when it starts.
type csJobInput struct {
	wsID, nsID string
	runtime    *namespace.Runtime
	s3         bundle.ContentStorageS3Config
}

// csGate is whether a run may start for the active namespace right now.
type csGate int

const (
	// csGateNotApplicable: nothing to connect — a DB namespace, no provider
	// configured, or a generation that left the provider out.
	csGateNotApplicable csGate = iota
	// csGateBusy: a snapshot or a dependency migration/rollback owns the
	// namespace's data right now.
	csGateBusy
	// csGatePlatformDown: an app the connection needs is not RUNNING.
	csGatePlatformDown
	csGateOpen
)

func newContentStorageProvisioner(d *Daemon) *contentStorageProvisioner {
	return &contentStorageProvisioner{
		d:       d,
		records: newHTTPEcosRecords,
		gatewayAddr: func(ctx context.Context, rt *namespace.Runtime) (string, error) {
			return rt.AppHTTPAddress(ctx, appdef.AppGateway, 0)
		},
		now:          time.Now,
		pollInterval: csPollInterval,
		readyTimeout: csReadyTimeout,
		retryAfter:   csRetryAfter,
	}
}

// contentStorageProv returns the daemon's provisioner, creating it on first use.
func (d *Daemon) contentStorageProv() *contentStorageProvisioner {
	d.contentStorageOnce.Do(func() {
		d.contentStorage = newContentStorageProvisioner(d)
	})
	return d.contentStorage
}

// OnRuntimeEvent is the hook handleRuntimeEvent calls for every runtime event
// of the active namespace. App and namespace status changes are the ones that
// can open or close the gate; everything else (stats every few seconds, pull
// progress) is ignored. It never blocks: the evaluation runs on its own
// goroutine, and a burst of events folds into one.
func (p *contentStorageProvisioner) OnRuntimeEvent(evt api.EventDto) {
	if evt.Type != "app_status" && evt.Type != "namespace_status" {
		return
	}
	if !p.kickQueued.CompareAndSwap(false, true) {
		return // an evaluation is already queued and will see this state
	}
	go func() {
		p.kickQueued.Store(false)
		p.evaluate(false, "")
	}()
}

// Retry runs the job now for the given namespace (the Retry button): it skips
// the latch and the error throttle, but not the gate — a namespace whose
// platform is down, or whose data a snapshot owns, runs once that is over.
func (p *contentStorageProvisioner) Retry(nsID string) {
	p.evaluate(true, nsID)
}

// Status answers the connection state of one namespace: pending for anything
// the provisioner has not worked on.
func (p *contentStorageProvisioner) Status(nsID string) ContentStorageStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	if nsID == "" || nsID != p.nsID || p.status.State == "" {
		return ContentStorageStatus{State: api.ContentStorageStatePending}
	}
	return p.status
}

// Reset forgets the previous namespace's state and cancels its job: called
// whenever the active namespace is replaced or cleared.
func (p *contentStorageProvisioner) Reset() {
	p.mu.Lock()
	if p.retryTimer != nil {
		p.retryTimer.Stop()
		p.retryTimer = nil
	}
	defer p.mu.Unlock()
	p.epoch.Add(1)
	p.adoptLocked("", "")
}

// adoptLocked makes the provisioner belong to another namespace: the job in
// flight is canceled and every verdict forgotten.
func (p *contentStorageProvisioner) adoptLocked(wsID, nsID string) {
	if p.retryTimer != nil {
		p.retryTimer.Stop()
		p.retryTimer = nil
	}
	if p.job != nil {
		p.job.cancel()
		p.job = nil
	}
	p.wsID, p.nsID = wsID, nsID
	p.status = ContentStorageStatus{}
	p.ranSinceUp = false
	p.lastRunAt = time.Time{}
}

// evaluate decides whether a run starts now, and starts it. force is the
// Retry route; nsID, when set, is the namespace the caller meant, and nothing
// happens if another one is active by now.
func (p *contentStorageProvisioner) evaluate(force bool, nsID string) {
	// Capture the generation before active(): Reset invalidates both the
	// active snapshot and its gate, including snapshots delayed before p.mu.
	epoch := p.epoch.Load()
	act := p.d.active()
	p.evaluateSnapshot(force, nsID, act, epoch)
}

// evaluateSnapshot evaluates one captured active namespace. Reading its gate
// stays outside p.mu; the generation check under p.mu prevents a delayed
// evaluation from adopting an old namespace and canceling its successor.
func (p *contentStorageProvisioner) evaluateSnapshot(force bool, nsID string, act activeNamespace, epoch uint64) {
	if p.d.bgCtx != nil && p.d.bgCtx.Err() != nil {
		return
	}
	if act.nsConfig == nil {
		return
	}
	in, gate := p.gateFor(act)

	p.mu.Lock()
	// Reset publishes its epoch while holding this mutex. Reject the stale
	// snapshot before adoption can cancel the successor's live job.
	if p.epoch.Load() != epoch {
		p.mu.Unlock()
		return
	}
	if p.wsID != act.workspaceID || p.nsID != act.nsConfig.ID {
		p.adoptLocked(act.workspaceID, act.nsConfig.ID)
	}
	if nsID != "" && nsID != p.nsID {
		p.mu.Unlock()
		return
	}
	var (
		evt *api.EventDto
		job *csJob
		ctx context.Context
	)
	switch gate {
	case csGateNotApplicable:
		p.ranSinceUp = false
	case csGateBusy:
		// Left exactly as it is; the next event after the operation asks again.
	case csGatePlatformDown:
		p.ranSinceUp = false
		// A Retry while the platform is down cannot run, but it must not leave
		// the old error up either: the run will happen once the apps are back.
		if force && p.job == nil && p.status.State == api.ContentStorageStateError {
			evt = p.setStatusLocked(api.ContentStorageStatePending, msg.Message{})
		}
	case csGateOpen:
		if p.job == nil && (force || p.dueLocked()) {
			job, ctx, evt = p.startLocked(in)
		}
	}
	p.mu.Unlock()

	p.broadcast(evt)
	if job != nil {
		go p.run(ctx, job)
	}
}

// gateFor answers whether the active namespace can be connected now, and the
// input a run needs when it can.
func (p *contentStorageProvisioner) gateFor(act activeNamespace) (csJobInput, csGate) {
	if act.runtime == nil || !act.nsConfig.UsesS3ContentStorage() ||
		act.contentStorageProblem != nil {
		return csJobInput{}, csGateNotApplicable
	}
	var s3 *bundle.ContentStorageS3Config
	if act.nsConfig.S3 != nil {
		// A locked or missing secret must never select an unusable default.
		content, ok := runtimeAppDef(act.runtime, appdef.AppContent)
		if !ok {
			return csJobInput{}, csGateNotApplicable
		}
		secret, _ := content.Environments.Get("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD")
		if secret == "" {
			return csJobInput{}, csGateNotApplicable
		}
		// External S3 has no Launcher-managed app or lifecycle dependency.
		s3 = &bundle.ContentStorageS3Config{}
	} else {
		s3 = act.workspaceConfig.ContentStorageS3()
		if s3 == nil {
			return csJobInput{}, csGateNotApplicable
		}
	}
	// Down before busy: a snapshot import stops the platform while it holds
	// the lock, and the latch has to see that, or the restored platform would
	// never be re-derived.
	if !csPlatformRunning(act.runtime, s3.App) {
		return csJobInput{}, csGatePlatformDown
	}
	if longOpOwnsNamespaceData(p.d.longOp.Holder()) {
		return csJobInput{}, csGateBusy
	}
	return csJobInput{
		wsID: act.workspaceID, nsID: act.nsConfig.ID,
		runtime: act.runtime, s3: *s3,
	}, csGateOpen
}

// csPlatformRunning reports whether every app the connection needs is RUNNING.
func csPlatformRunning(rt *namespace.Runtime, provider string) bool {
	apps := slices.Clone(csPlatformApps)
	if provider != "" {
		apps = append(apps, provider)
	}
	for _, name := range apps {
		app := rt.FindApp(name)
		if app == nil || app.Status != namespace.AppStatusRunning {
			return false
		}
	}
	return true
}

// dueLocked is the latch plus the error throttle.
func (p *contentStorageProvisioner) dueLocked() bool {
	if !p.ranSinceUp {
		return true
	}
	return p.status.State == api.ContentStorageStateError && !p.now().Before(p.lastRunAt.Add(p.retryAfter))
}

// startLocked registers a run. The caller starts its goroutine after
// unlocking. A daemon that is shutting down starts nothing.
func (p *contentStorageProvisioner) startLocked(in csJobInput) (job *csJob, ctx context.Context, evt *api.EventDto) {
	if p.retryTimer != nil {
		p.retryTimer.Stop()
		p.retryTimer = nil
	}
	base := p.d.bgCtx
	if base == nil {
		base = context.Background()
	}
	if base.Err() != nil {
		return nil, nil, nil
	}
	ctx, cancel := context.WithCancel(base) //nolint:gosec // G118: the job owns cancel; finish and adoptLocked call it
	job = &csJob{in: in, cancel: cancel, done: make(chan struct{}), prior: p.status.State}
	p.job = job
	p.ranSinceUp = true
	p.lastRunAt = p.now()
	if !csVerdict(job.prior) {
		evt = p.setStatusLocked(api.ContentStorageStateWaiting, msg.Message{})
	}
	return job, ctx, evt
}

// csVerdict reports whether a state is a settled answer from ECOS.
func csVerdict(state string) bool {
	return state == api.ContentStorageStateConnected || state == api.ContentStorageStateAdminManaged
}

// setStatusLocked records a state and answers the SSE event announcing it —
// nil when nothing changed — for the caller to broadcast once p.mu is
// released.
func (p *contentStorageProvisioner) setStatusLocked(state string, m msg.Message) *api.EventDto {
	if p.status.State == state && p.status.ErrorMsg.Key == m.Key && slices.Equal(p.status.ErrorMsg.Args, m.Args) {
		return nil
	}
	before := p.status.State
	if before == "" {
		before = api.ContentStorageStatePending
	}
	p.status = ContentStorageStatus{State: state, ErrorMsg: m, At: p.now()}
	return &api.EventDto{
		Type:        contentStorageEventType,
		Timestamp:   p.status.At.UnixMilli(),
		NamespaceID: p.nsID,
		Before:      before,
		After:       state,
	}
}

// setState records a run's state, unless the run is no longer the current one
// (canceled by a namespace switch, superseded): such a run's word counts for
// nothing. changed reports whether the state really moved.
func (p *contentStorageProvisioner) setState(j *csJob, state string, m msg.Message) (changed bool) {
	p.mu.Lock()
	if j != p.job {
		p.mu.Unlock()
		return false
	}
	evt := p.setStatusLocked(state, m)
	p.mu.Unlock()
	p.broadcast(evt)
	return evt != nil
}

// fail ends a run in the error state.
func (p *contentStorageProvisioner) fail(j *csJob, m msg.Message) {
	if p.setState(j, api.ContentStorageStateError, m) {
		slog.Warn("S3 content storage could not be connected", "ns", j.in.nsID, "provider", j.in.s3.App,
			"reason", englishForLogs.Render(m))
	}
}

// abort ends a run that cannot continue because the platform went away (an
// app stopped, a snapshot took the lock): the latch is re-armed so the run
// happens again once the platform is back, and the namespace shows the
// verdict it had, or pending.
func (p *contentStorageProvisioner) abort(j *csJob) {
	p.mu.Lock()
	if j != p.job {
		p.mu.Unlock()
		return
	}
	p.ranSinceUp = false
	var evt *api.EventDto
	if !csVerdict(j.prior) {
		evt = p.setStatusLocked(api.ContentStorageStatePending, msg.Message{})
	}
	p.mu.Unlock()
	slog.Info("S3 content storage connection postponed: the platform is not available", "ns", j.in.nsID)
	p.broadcast(evt)
}

// finish releases the slot, schedules automatic error retry, and evaluates
// a platform restart observed during the job. Register the successor before
// closing done, so waiters see any job that replaced this one.
func (p *contentStorageProvisioner) finish(j *csJob) {
	p.mu.Lock()
	current := p.job == j
	if current {
		p.job = nil
	}
	if current && p.status.State == api.ContentStorageStateError {
		p.scheduleRetryLocked()
	}
	again := current && !p.ranSinceUp
	p.mu.Unlock()
	j.cancel()
	if again {
		p.evaluate(false, "")
	}
	close(j.done)
}

// scheduleRetryLocked keeps retry alive even if a tick finds the platform
// stopped or data owned by another operation. Namespace Reset cancels it.
func (p *contentStorageProvisioner) scheduleRetryLocked() {
	if p.retryTimer != nil {
		return
	}
	epoch, wsID, nsID := p.epoch.Load(), p.wsID, p.nsID
	var timer *time.Timer
	timer = time.AfterFunc(p.retryAfter, func() {
		p.mu.Lock()
		if p.retryTimer != timer || epoch != p.epoch.Load() || p.wsID != wsID || p.nsID != nsID {
			p.mu.Unlock()
			return
		}
		p.retryTimer = nil
		p.mu.Unlock()
		if p.d.bgCtx != nil && p.d.bgCtx.Err() != nil {
			return
		}
		p.evaluateSnapshot(false, "", p.d.active(), epoch)
		p.mu.Lock()
		defer p.mu.Unlock()
		if epoch == p.epoch.Load() && p.wsID == wsID && p.nsID == nsID &&
			p.job == nil && p.status.State == api.ContentStorageStateError {
			p.scheduleRetryLocked()
		}
	})
	p.retryTimer = timer
}

// broadcast sends a state change to every SSE subscriber; nil is no change.
func (p *contentStorageProvisioner) broadcast(evt *api.EventDto) {
	if evt != nil {
		p.d.broadcastEvent(*evt)
	}
}

// platformStillUp is the gate again, asked during a run: the apps may stop and
// a snapshot may start while the run waits for ECOS.
func (p *contentStorageProvisioner) platformStillUp(j *csJob) bool {
	return !longOpOwnsNamespaceData(p.d.longOp.Holder()) && csPlatformRunning(j.in.runtime, j.in.s3.App)
}
