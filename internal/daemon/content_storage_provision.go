package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// The one-time connection of ecos-content to an S3 namespace's provider: the
// daemon creates the secret, endpoint and storage records in ECOS and switches
// `default-content-storage` to them, once, through the gateway's records API
// (content_storage_provision_records.go).
//
// WHEN it runs:
//
//   - from OnRuntimeEvent (app and namespace status events) once gateway,
//     emodel, eapps, content AND the provider are all RUNNING — never waiting
//     for the NAMESPACE to be running, since one unrelated app can keep it
//     STALLED. The provider is part of the gate because switching the default
//     storage to an S3 server that is down makes every upload fail at once;
//   - once per time the platform comes up (the "ran since up" latch): a
//     restart of any of those apps — a namespace stop, a snapshot import, an
//     update — re-arms it, so the next run re-derives the answer;
//   - after an error, again on a qualifying event at most every 5 minutes, and
//     at once on the Retry route;
//   - never for a DB namespace, never while the generation reports a
//     ContentStorageProblem (the provider is not part of the namespace), and
//     not while a snapshot or a dependency migration/rollback holds the
//     long-operation lock — the state is left as it is and the next event
//     asks again;
//   - single-flight, bound to d.bgCtx, canceled by Reset on a namespace
//     switch.
//
// WHAT it does — the decision is re-derived from ECOS's own records on every
// run, with no persisted "done" flag, so it is right after a snapshot restore
// or a wiped database, and it never overwrites what an administrator chose:
//
//  1. waits until ECOS answers (every 15 s, at most 15 min): the default
//     storage config must have a value — a config that is not deployed yet
//     answers none. A gateway that is up is not a platform that is ready:
//     artifacts deploy asynchronously;
//  2. a default storage other than the built-in LOCAL one is a verdict:
//     ours → connected, anything else → admin-managed;
//  3. our storage record already there while the default is LOCAL means an
//     administrator switched back on purpose → admin-managed, the default is
//     left alone — UNLESS this launcher's own switch to it is still pending
//     (the marker below), in which case the switch is finished;
//  4. otherwise creates the records that are MISSING and switches the
//     default, then reads it back → connected. A step that failed in a way
//     the platform can still change (no answer, a record source not
//     registered yet) is retried within the same budget.
//
// The default and the storage record are never rewritten. The secret and the
// endpoint are the launcher's OWN, and on every run that does not end on an
// administrator's own storage as the default, the ones that exist are brought
// up to the namespace's current key and provider URL first, an
// administrator's edit included (content_storage_provision_owned.go).
//
// Two things are persisted, and neither is a verdict. The pending switch
// (NsPersistedState.ContentStorageSwitchPending) is the storage ref this
// launcher started switching to, written before its first record and cleared
// once ECOS answers with a verdict. ECOS's records cannot tell "our run
// created the storage and then failed or was canceled before the switch" from
// "an administrator switched back": without the marker the first reads as the
// second, permanently. The secret write (NsPersistedState.
// ContentStorageSecretWrite) is what the launcher last wrote into its secret,
// because ECOS never hands a secret's data back.
//
// ECOS's existence attribute: `_notExists` is answered TRUE only by a value
// that does not exist (EmptyAttValue, ecos-data's NonExistentDbRecord) and by
// a record source that is not registered (which answers EmptyAttValue). An
// EXISTING record never says false — DbRecord, a bean value and the eapps
// config proxy all leave the attribute unanswered (null). So "missing" is
// exactly `true`, and a create aimed at a source that is not registered yet
// fails and is retried rather than read as final.
//
// Concurrency: OnRuntimeEvent is called from the runtime's event fan-out and
// returns at once (it only spawns). None of these methods take Daemon.configMu
// while holding p.mu — d.active() is always read BEFORE p.mu — and Reset is
// called right after configMu is released on the switch paths.

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
	secrets      func(wsID, nsID string, ws *bundle.WorkspaceConfig) (map[string]string, error)
	now          func() time.Time
	pollInterval time.Duration
	readyTimeout time.Duration
	retryAfter   time.Duration

	// kickQueued coalesces a burst of runtime events into one evaluation.
	kickQueued atomic.Bool

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
	lastRunAt time.Time
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
	ws         *bundle.WorkspaceConfig
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
		secrets: func(wsID, nsID string, ws *bundle.WorkspaceConfig) (map[string]string, error) {
			// Boxed only when non-nil: a typed-nil *SecretService would pass
			// loadNamespaceSecrets' nil check and panic inside it.
			if d.secretService == nil {
				return nil, errors.New("no secret store")
			}
			// READ: never stores or invents a value. The generation already
			// stored what this namespace uses; a constant nobody stored answers
			// its default, a GENERATED value nobody stored answers nothing — no
			// container was started with it — and credentials() refuses it as
			// missing, before ECOS is asked anything.
			return loadNamespaceSecrets(d.secretService, wsID, nsID, ws, nsSecretsRead)
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
	defer p.mu.Unlock()
	p.adoptLocked("", "")
}

// adoptLocked makes the provisioner belong to another namespace: the job in
// flight is canceled and every verdict forgotten.
func (p *contentStorageProvisioner) adoptLocked(wsID, nsID string) {
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
	// Read the daemon and the runtime BEFORE p.mu: the lock rule above.
	act := p.d.active()
	if act.nsConfig == nil {
		return
	}
	in, gate := p.gateFor(act)

	p.mu.Lock()
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
	if act.runtime == nil || act.nsConfig.ContentStorageType() != namespace.ContentStorageS3 ||
		act.contentStorageProblem != nil {
		return csJobInput{}, csGateNotApplicable
	}
	s3 := act.workspaceConfig.ContentStorageS3()
	if s3 == nil {
		return csJobInput{}, csGateNotApplicable
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
		runtime: act.runtime, ws: act.workspaceConfig, s3: *s3,
	}, csGateOpen
}

// longOpOwnsNamespaceData reports whether the long-operation holder takes the
// namespace's data away from under everyone: a snapshot restoring volumes, a
// dependency migration or rollback stopping the namespace. An update pass or
// an ordinary request does not, and the connection does not wait for them.
func longOpOwnsNamespaceData(k longOpKind) bool {
	return k == longOpSnapshot || k == longOpMigration || k == longOpDepsRollback
}

// csPlatformRunning reports whether every app the connection needs is RUNNING.
func csPlatformRunning(rt *namespace.Runtime, provider string) bool {
	for _, name := range append(slices.Clone(csPlatformApps), provider) {
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

// finish releases the single-flight slot.
func (p *contentStorageProvisioner) finish(j *csJob) {
	p.mu.Lock()
	if p.job == j {
		p.job = nil
	}
	p.mu.Unlock()
	j.cancel()
	close(j.done)
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

// csRecordIDs are the records the connection owns, derived from the provider
// app. Never ecos-content's own built-in `content-storage-s3*` ids: those are
// the product's example, and an administrator may have filled them in.
type csRecordIDs struct {
	provider string
	secret   string
	endpoint string
	storage  string
}

func csRecordIDsFor(provider string) csRecordIDs {
	return csRecordIDs{
		provider: provider,
		secret:   "emodel/secret@" + csSecretLocalID(provider),
		endpoint: "emodel/endpoint@" + csStorageLocalID(provider),
		storage:  "content/storage@" + csStorageLocalID(provider),
	}
}

func csSecretLocalID(provider string) string  { return provider + "-s3-credentials" }
func csStorageLocalID(provider string) string { return provider + "-s3" }

// csCredentials is the S3 identity handed to ECOS, resolved.
type csCredentials struct {
	accessKey, secretKey string
}

// redact hides the secret key in a text that is about to be logged or shown.
// An answer from ECOS has no business echoing it, but nothing guarantees it.
func (c csCredentials) redact(s string) string {
	if c.secretKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.secretKey, "***")
}

// credentials resolves the S3 identity through the namespace's own secret
// values — the same resolution `${secret:}` gets in the generation, so ECOS is
// handed the key the provider's container was started with. A value that
// cannot be read is the error state, before anything is asked or written.
func (p *contentStorageProvisioner) credentials(in csJobInput) (csCredentials, msg.Message) {
	values, err := p.secrets(in.wsID, in.nsID, in.ws)
	if err != nil {
		if errors.Is(err, storage.ErrSecretsLocked) {
			return csCredentials{}, msg.New("contentStorage.msg.provision.secretsLocked", "app", in.s3.App)
		}
		return csCredentials{}, msg.New("contentStorage.msg.provision.secretsUnreadable", "error", err.Error())
	}
	access, missingAccess := namespace.ResolveSecretRefs(in.s3.AccessKey, values)
	secret, missingSecret := namespace.ResolveSecretRefs(in.s3.SecretKey, values)
	missing := slices.Concat(missingAccess, missingSecret)
	slices.Sort(missing)
	if missing = slices.Compact(missing); len(missing) > 0 {
		return csCredentials{}, msg.New("contentStorage.msg.provision.secretMissing",
			"app", in.s3.App, "secrets", strings.Join(missing, ", "))
	}
	return csCredentials{accessKey: access, secretKey: secret}, msg.Message{}
}

// run is one connection attempt.
func (p *contentStorageProvisioner) run(ctx context.Context, j *csJob) {
	defer p.finish(j)
	in := j.in
	ids := csRecordIDsFor(in.s3.App)

	creds, problem := p.credentials(in)
	if !problem.Empty() {
		p.fail(j, problem)
		return
	}
	deadline := p.now().Add(p.readyTimeout)
	for {
		client, snap, ok := p.awaitReady(ctx, j, ids, creds, deadline)
		if !ok {
			return
		}
		step, done := p.pass(ctx, client, j, snap, ids, creds)
		if done || ctx.Err() != nil {
			return
		}
		if !step.retryable || !p.now().Before(deadline) {
			p.fail(j, step.msg)
			return
		}
		slog.Info("An S3 content storage step did not go through yet; asking ECOS again",
			"ns", in.nsID, "reason", englishForLogs.Render(step.msg))
		if !p.pause(ctx) {
			return
		}
	}
}

// pass decides on one reading of ECOS and acts on it. done is true when the
// run is over — a verdict recorded, or the run canceled or postponed, each
// already said — and step is what failed otherwise, for the caller to retry
// or report.
func (p *contentStorageProvisioner) pass(ctx context.Context, client ecosRecords, j *csJob, snap csSnapshot, ids csRecordIDs, creds csCredentials) (step *csStepError, done bool) {
	pending := j.in.runtime.ContentStorageSwitch() == ids.storage
	if state, settled := csDecide(snap, ids, pending); settled {
		// A verdict from ECOS ends whatever switch was pending: ours is the
		// default now, or an administrator chose another storage. A run that
		// is no longer current says and writes nothing more.
		if !p.recordSwitch(ctx, j, "") {
			return nil, true
		}
		// The verdict is not a reason to leave the launcher's own secret on a
		// key the provider no longer accepts: "connected" over a 403 is the
		// one answer worse than none.
		if failed, ended := p.keepOwnRecords(ctx, client, j, snap, ids, creds); failed != nil || ended {
			return failed, ended
		}
		p.settle(j, state, snap.defaultRef)
		return nil, true
	}
	if !p.mayWrite(ctx, j) {
		return nil, true
	}
	// Recorded BEFORE the first write, so a failure or a daemon death anywhere
	// between creating the storage and switching the default is finished by
	// the next run instead of read as an administrator's choice. A run that is
	// no longer current writes nothing at all.
	if !p.recordSwitch(ctx, j, ids.storage) {
		return nil, true
	}
	p.setState(j, api.ContentStorageStateProvisioning, msg.Message{})
	if failed := p.provision(ctx, client, j, snap, ids, creds); failed != nil {
		return failed, false
	}
	p.recordSwitch(ctx, j, "")
	p.settle(j, api.ContentStorageStateConnected, ids.storage)
	return nil, true
}

// mayWrite is the last look before a write into ECOS: nothing may be written
// for a namespace that is no longer active, or into a platform a snapshot is
// about to replace. A platform that went away postpones the run (abort).
func (p *contentStorageProvisioner) mayWrite(ctx context.Context, j *csJob) bool {
	if ctx.Err() != nil {
		return false
	}
	if !p.platformStillUp(j) {
		p.abort(j)
		return false
	}
	return true
}

// recordSwitch writes the pending-switch marker (a storage ref) or clears it
// (""), and answers false when the run is no longer the current one — then
// nothing is written, here or into ECOS. Held under p.mu with the job check
// on purpose: Reset (namespace switch, delete) takes p.mu BEFORE the runtime
// is shut down and its record deleted, so a run that lost the race cannot
// write a state row back for a namespace that is gone. A failed write is
// logged by the runtime and stays owed; the in-memory marker still serves
// this process.
func (p *contentStorageProvisioner) recordSwitch(ctx context.Context, j *csJob, storageRef string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j != p.job || ctx.Err() != nil {
		return false
	}
	_ = j.in.runtime.SetContentStorageSwitch(storageRef)
	return true
}

// pause waits one poll interval; false when the run was canceled meanwhile.
func (p *contentStorageProvisioner) pause(ctx context.Context) bool {
	timer := time.NewTimer(p.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// settle records a verdict from ECOS. Said in the log once per change, not on
// every re-verification.
func (p *contentStorageProvisioner) settle(j *csJob, state, defaultRef string) {
	if !p.setState(j, state, msg.Message{}) {
		return
	}
	if state == api.ContentStorageStateConnected {
		slog.Info("S3 content storage is connected", "ns", j.in.nsID, "storage", defaultRef)
		return
	}
	slog.Info("S3 content storage is managed by an administrator: the launcher leaves the default storage alone",
		"ns", j.in.nsID, "defaultStorage", defaultRef)
}

// csDecide is the verdict ECOS's own records already give, before anything is
// written. settled is false only when the launcher should create what is
// missing and switch the default. switchPending is whether this launcher's
// own switch to our storage was started and never confirmed.
func csDecide(snap csSnapshot, ids csRecordIDs, switchPending bool) (state string, settled bool) {
	if snap.defaultRef != "" && snap.defaultRef != csLocalStorageRef {
		if snap.defaultRef == ids.storage {
			return api.ContentStorageStateConnected, true
		}
		return api.ContentStorageStateAdminManaged, true
	}
	if snap.storageExists && !switchPending {
		// Ours exists and the default is LOCAL, and it was not this launcher
		// that left it half-way: somebody switched back.
		return api.ContentStorageStateAdminManaged, true
	}
	return "", false
}

// awaitReady polls until ECOS can answer the decision, the platform goes away
// (abort), the answer is one that waiting will not change (error), or the
// readiness budget runs out (error). ok is true with a snapshot to decide on.
func (p *contentStorageProvisioner) awaitReady(ctx context.Context, j *csJob, ids csRecordIDs, creds csCredentials, deadline time.Time) (ecosRecords, csSnapshot, bool) {
	for {
		if ctx.Err() != nil {
			return nil, csSnapshot{}, false
		}
		if !p.platformStillUp(j) {
			p.abort(j)
			return nil, csSnapshot{}, false
		}
		client, snap, err := p.probe(ctx, j.in.runtime, ids)
		if err == nil {
			return client, snap, true
		}
		if ctx.Err() != nil {
			return nil, csSnapshot{}, false
		}
		if !csRetryable(err) {
			p.fail(j, msg.New("contentStorage.msg.provision.readFailed", "error", creds.redact(err.Error())))
			return nil, csSnapshot{}, false
		}
		if !p.now().Before(deadline) {
			p.fail(j, msg.New("contentStorage.msg.provision.notReady",
				"minutes", strconv.Itoa(int(p.readyTimeout/time.Minute)), "error", creds.redact(err.Error())))
			return nil, csSnapshot{}, false
		}
		slog.Debug("ECOS is not ready for the S3 content storage connection yet", "ns", j.in.nsID, "err", err)
		if !p.pause(ctx) {
			return nil, csSnapshot{}, false
		}
	}
}

// probe resolves the gateway and reads the snapshot through it.
func (p *contentStorageProvisioner) probe(ctx context.Context, rt *namespace.Runtime, ids csRecordIDs) (ecosRecords, csSnapshot, error) {
	addr, err := p.gatewayAddr(ctx, rt)
	if err != nil {
		return nil, csSnapshot{}, err
	}
	client := p.records(addr)
	snap, err := csReadSnapshot(ctx, client, ids)
	return client, snap, err
}

// csStepError is why provision stopped: the operator's sentence, and whether
// the same run may go back to reading ECOS and try again.
type csStepError struct {
	msg       msg.Message
	retryable bool
}

// stepFailed builds a csStepError from a records call that failed.
func stepFailed(err error, m msg.Message) *csStepError {
	return &csStepError{msg: m, retryable: csStepRetryable(err)}
}

// csStepRetryable reports whether a failed WRITE is worth another pass of the
// same run. Narrower than the readiness poll's rule: a write answered with an
// ordinary ERROR (HTTP 500) is a refusal that is said at once — the next
// qualifying event retries it anyway — while no answer at all, a gateway or
// upstream that is not there yet, and a record source that is not registered
// yet (its app still starting: ECOS answered "does not exist" for it, which is
// why the create was attempted) are the platform still coming up. Every write
// is safe to repeat: the pass re-reads ECOS first, so a create that landed
// before its answer was lost is seen and skipped, and the default switch is
// idempotent.
func csStepRetryable(err error) bool {
	var rec *ecosRecordsError
	if !errors.As(err, &rec) {
		return false
	}
	switch rec.Status {
	case 0, http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return slices.ContainsFunc(rec.Messages, csSourceNotFound)
}

// csSourceNotFound reports whether a records message says the record source
// is not registered (records' RecordsSourceNotFoundException, "Source is not
// found with id …", and the older "Records source is not found: …").
func csSourceNotFound(message string) bool {
	return strings.Contains(strings.ToLower(message), "source is not found")
}

// provision brings the launcher's existing secret and endpoint up to date,
// creates the missing records and switches the default; a non-nil answer is
// why it stopped. Every write is logged, and none is repeated within a pass.
func (p *contentStorageProvisioner) provision(ctx context.Context, client ecosRecords, j *csJob, snap csSnapshot, ids csRecordIDs, creds csCredentials) *csStepError {
	// Before the switch: a secret left by an earlier run, a restored database
	// or an administrator may hold a key the provider no longer accepts.
	plan, readErr := p.planOwnRecords(ctx, client, j, snap, ids, creds)
	if readErr != nil {
		return csReadStep(readErr, creds)
	}
	if step := p.writeOwnRecords(ctx, client, j, ids, plan, creds); step != nil {
		return step
	}

	app := ids.provider
	creates := []struct {
		exists bool
		ref    string
		create string
		atts   map[string]any
	}{
		{snap.secretExists, ids.secret, "emodel/secret@", map[string]any{
			"id":        csSecretLocalID(app),
			"name":      app + " S3 credentials",
			"type":      "BASIC",
			"data?json": map[string]any{"username": creds.accessKey, "password": creds.secretKey},
		}},
		{snap.endpointExists, ids.endpoint, "emodel/endpoint@", map[string]any{
			"id":          csStorageLocalID(app),
			"name":        app + " S3",
			"url":         j.in.s3.URL,
			"credentials": ids.secret,
		}},
		{snap.storageExists, ids.storage, "content/storage@", map[string]any{
			"id":   csStorageLocalID(app),
			"name": app + " S3",
			"type": "S3",
			"config?json": map[string]any{
				"endpoint":   ids.endpoint,
				"bucketName": j.in.s3.Bucket,
			},
		}},
	}
	for _, c := range creates {
		if c.exists {
			continue
		}
		if _, err := client.Mutate(ctx, c.create, c.atts); err != nil {
			return stepFailed(err, msg.New("contentStorage.msg.provision.createFailed",
				"record", c.ref, "error", creds.redact(err.Error())))
		}
		slog.Info("Created an ECOS record for the S3 content storage", "ns", j.in.nsID, "record", c.ref)
		if c.ref == ids.secret {
			p.rememberSecretWrite(ctx, client, j, ids, creds)
		}
	}

	_, err := client.Mutate(ctx, csConfigRef, map[string]any{
		"_value?json": map[string]any{"ref": ids.storage, "config": map[string]any{}},
	})
	if err != nil {
		return stepFailed(err, msg.New("contentStorage.msg.provision.setDefaultFailed",
			"storage", ids.storage, "error", creds.redact(err.Error())))
	}
	slog.Info("Switched the default content storage", "ns", j.in.nsID, "storage", ids.storage)

	after, err := csReadSnapshot(ctx, client, ids)
	if err != nil {
		return csReadStep(err, creds)
	}
	if after.defaultRef != ids.storage {
		actual := after.defaultRef
		if actual == "" {
			actual = "—"
		}
		return &csStepError{msg: msg.New("contentStorage.msg.provision.verifyFailed", "storage", ids.storage, "actual", actual)}
	}
	return nil
}

// csSnapshot is what ECOS says about the connection.
type csSnapshot struct {
	// defaultRef is the default content storage, "" when it names none.
	defaultRef     string
	secretExists   bool
	endpointExists bool
	storageExists  bool
}

// csNotReadyError is an answer that says ECOS is not done starting: a
// default-storage config with no value yet. Asking again later is the answer
// to it.
type csNotReadyError struct{ reason string }

func (e *csNotReadyError) Error() string { return e.reason }

// csRetryable reports whether the readiness poll should ask again.
func csRetryable(err error) bool {
	var notReady *csNotReadyError
	var rec *ecosRecordsError
	switch {
	case errors.As(err, &notReady):
		return true
	case errors.As(err, &rec):
		return rec.retryable()
	case errors.Is(err, namespace.ErrAppAddressUnknown), errors.Is(err, namespace.ErrAppNotRunning):
		// A gateway being recreated; platformStillUp ends the wait if it is
		// really gone.
		return true
	default:
		return false
	}
}

// csReadSnapshot reads the default storage and the existence of the three
// records.
//
// The config's value is the readiness signal: a config artifact that is not
// deployed yet is an empty record whose `_value` is null, and so is one whose
// value nobody set — neither is "nothing chosen". A value that is not an
// object carrying a string `ref` is not one this launcher understands, and so
// not one it may replace: a hard error, not a reason to write.
//
// A record is missing only when ECOS says `_notExists` is TRUE; an existing
// record leaves the attribute unanswered (see the file comment), so null,
// absent and false all mean it is there.
func csReadSnapshot(ctx context.Context, client ecosRecords, ids csRecordIDs) (csSnapshot, error) {
	cfg, err := client.Query(ctx, []string{csConfigRef}, map[string]string{"value": "_value?json"})
	if err != nil {
		return csSnapshot{}, fmt.Errorf("read the default content storage: %w", err)
	}
	raw := cfg[0]["value"]
	if jsonNull(raw) {
		return csSnapshot{}, &csNotReadyError{reason: csConfigRef + " has no value yet (not deployed yet?)"}
	}
	defaultRef, ok := csDefaultRef(raw)
	if !ok {
		// A value this launcher does not understand is not one it may replace.
		return csSnapshot{}, fmt.Errorf("unexpected value of %s: %s", csConfigRef, truncateText(string(raw), 200))
	}

	refs := []string{ids.secret, ids.endpoint, ids.storage}
	recs, err := client.Query(ctx, refs, map[string]string{"notExists": "_notExists?bool"})
	if err != nil {
		return csSnapshot{}, fmt.Errorf("read the content storage records: %w", err)
	}
	exists := make([]bool, len(refs))
	for i := range refs {
		notExists, answered := jsonBool(recs[i]["notExists"])
		exists[i] = !answered || !notExists
	}
	return csSnapshot{
		defaultRef:     defaultRef,
		secretExists:   exists[0],
		endpointExists: exists[1],
		storageExists:  exists[2],
	}, nil
}

// csDefaultRef reads the storage ref out of the default-storage config value:
// a JSON object whose `ref` is a string ("" means nothing chosen). ok is false
// for any other shape — an array, a scalar, an object without `ref`, a `ref`
// that is null or not a string.
func csDefaultRef(raw json.RawMessage) (ref string, ok bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return "", false
	}
	refRaw, present := obj["ref"]
	if !present || !strings.HasPrefix(strings.TrimSpace(string(refRaw)), `"`) {
		return "", false
	}
	if json.Unmarshal(refRaw, &ref) != nil {
		return "", false
	}
	return strings.TrimSpace(ref), true
}

// jsonBool reads a JSON boolean; ok is false for anything else, null included.
func jsonBool(raw json.RawMessage) (value, ok bool) {
	var b *bool
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil || b == nil {
		return false, false
	}
	return *b, true
}

// jsonNull reports whether a raw JSON value is absent or null.
func jsonNull(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}
