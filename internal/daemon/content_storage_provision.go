package daemon

import (
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// The one-time connection of ecos-content to an S3 namespace's provider: the
// daemon creates the secret, endpoint and storage records in ECOS and switches
// `default-content-storage` to them, once, through the gateway's records API.
//
// THIS FILE IS THE SEAM. What it holds today is the shape every caller depends
// on — the status the namespace DTO reports, the hook the runtime events reach,
// the retry the route calls, the reset a namespace switch performs — with the
// job itself not implemented: every S3 namespace reports "pending". The rules
// the job has to keep are the ones written down for it:
//
//   - triggered from OnRuntimeEvent once gateway, emodel, eapps and content are
//     all RUNNING (never waiting for the NAMESPACE to be running: one unrelated
//     app can keep it STALLED), single-flight per namespace, bound to d.bgCtx
//     and canceled by Reset;
//   - skipped, state left pending, while a snapshot or a dependency migration
//     holds the long-operation lock; retried on the next qualifying event;
//   - reaches the gateway directly, bypassing nginx: Runtime.AppHTTPAddress
//     (gateway, 0) is the address the startup probes use, SERVER_PORT included;
//   - derives everything from ECOS's own records on every run — no persisted
//     "done" flag — and never overwrites what an administrator chose;
//   - never runs for a DB namespace.
//
// Concurrency contract for whoever fills it in: OnRuntimeEvent is called from
// the runtime's event fan-out and must return at once (spawn, never block),
// and none of these methods may take Daemon.configMu while holding p.mu —
// Reset is called right after configMu is released on the switch paths, and
// handleGetNamespace calls Status with a d.active() snapshot in hand.

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

	mu sync.Mutex
	// nsID is the namespace status belongs to; "" before anything happened.
	// Status for any other namespace answers pending, so a namespace switch
	// can never show the previous namespace's verdict even before Reset ran.
	nsID   string
	status ContentStorageStatus
}

func newContentStorageProvisioner(d *Daemon) *contentStorageProvisioner {
	return &contentStorageProvisioner{d: d}
}

// contentStorageProv returns the daemon's provisioner, creating it on first use.
func (d *Daemon) contentStorageProv() *contentStorageProvisioner {
	d.contentStorageOnce.Do(func() {
		d.contentStorage = newContentStorageProvisioner(d)
	})
	return d.contentStorage
}

// OnRuntimeEvent is the hook handleRuntimeEvent calls for every runtime event
// of the active namespace. It must not block. STUB: the job is not
// implemented yet, so nothing is triggered.
func (p *contentStorageProvisioner) OnRuntimeEvent(_ api.EventDto) {}

// Retry runs the job now for the given namespace (the Retry button). The
// caller has already checked that the namespace stores its content in S3.
// STUB: records nothing and starts nothing.
func (p *contentStorageProvisioner) Retry(_ string) {}

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
	p.nsID = ""
	p.status = ContentStorageStatus{}
}
