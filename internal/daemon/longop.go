package daemon

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/api"
)

// longOpKind names WHO holds the long-operation lock. It exists because the
// refusal has to be truthful: a route that answered "a snapshot or dependency
// migration is in progress" during the git-pull/generate window of an ORDINARY
// start named two things that were not happening, and sent the operator looking
// for a snapshot nobody took.
type longOpKind string

const (
	// longOpNone is the zero holder. As a tryLongOp policy it means "tolerate
	// nothing" — refuse whoever holds the lock.
	longOpNone longOpKind = ""
	// longOpSnapshot is a snapshot export/import. Its routes keep answering
	// SNAPSHOT_IN_PROGRESS; this kind is what names it to everyone else.
	longOpSnapshot longOpKind = "snapshot"
	// longOpMigration is a dependency migration: it stops the namespace,
	// rewrites volumes and recreates containers, so it excludes everything.
	longOpMigration longOpKind = "migration"
	// longOpUpdatePass is an asynchronous pass that re-drives the namespace on
	// the user's behalf — the queued Update & Start pass, and the attach-toggle
	// regeneration. It is the one holder Start and Stop tolerate (see
	// tryLongOp): the pass IS a start, and refusing Stop would take away the
	// escape hatch for a pass that is stuck in a slow git pull.
	longOpUpdatePass longOpKind = "update-pass"
	// longOpRequest is a synchronous mutating handler holding the lock for its
	// own duration (a reload, a namespace edit, a delete).
	longOpRequest longOpKind = "request"
)

// busyMessage is what the operator is told when this holder refuses them.
func (k longOpKind) busyMessage() string {
	switch k {
	case longOpSnapshot:
		return "a snapshot is in progress"
	case longOpMigration:
		return "a dependency migration is in progress"
	case longOpUpdatePass:
		return "an update pass is in progress"
	case longOpRequest:
		return "another namespace operation is in progress"
	default:
		// longOpNone: the holder released between the failed TryLock and this
		// read. Rare, and not worth a retry loop — the request is refused
		// either way, and a vague true sentence beats a precise false one.
		return "another long operation is in progress"
	}
}

// longOpLock is THE exclusive long-operation lock plus the identity of whoever
// holds it. Daemon-global — one active namespace per daemon — so a migration
// also blocks an unrelated snapshot action, which is intended.
//
// The owner is read WITHOUT the mutex (by definition: you read it because you
// failed to take the lock), hence the atomic. It is stored after the lock is
// taken and cleared before it is dropped, so a reader can only ever see the
// real holder or longOpNone — never a stale different kind.
type longOpLock struct {
	mu    sync.Mutex
	owner atomic.Value // longOpKind
}

// TryLock claims the lock for kind, or reports false without blocking. Never
// Lock: a mutating request must be refused while a long operation runs, not
// queued behind it — a migration recreates containers for minutes and an HTTP
// client waiting that long has already given up.
func (l *longOpLock) TryLock(kind longOpKind) bool {
	if !l.mu.TryLock() {
		return false
	}
	l.owner.Store(kind)
	return true
}

// Unlock releases the lock. Clearing the owner FIRST keeps the invariant that a
// lock-free reader never attributes the lock to a holder that has let go.
func (l *longOpLock) Unlock() {
	l.owner.Store(longOpNone)
	l.mu.Unlock()
}

// Holder reports the current owner, or longOpNone when the lock is free (or was
// released a moment ago).
func (l *longOpLock) Holder() longOpKind {
	k, _ := l.owner.Load().(longOpKind)
	return k
}

// tryLongOp claims the long-operation lock for the duration of a synchronous
// handler. ok=false means the daemon is busy with something that owns the
// namespace, the 409 naming that holder has already been written, and the
// caller must return immediately. Callers `defer release()`.
//
// tolerate is the policy: the ONE holder kind this route may proceed ALONGSIDE
// (longOpNone ⇒ tolerate nothing). A tolerated holder returns ok=true with a
// no-op release — the route runs WITHOUT the lock, exactly as it did before
// this feature existed. Only handleStartNamespace and handleStopNamespace use
// it, and only for longOpUpdatePass:
//
//   - Start must fold into the single-slot update queue (the documented
//     click-folding contract, force OR-ed) instead of 409-ing the second click;
//   - Stop is the escape hatch from a pass stuck in a slow git pull, and taking
//     it away is strictly worse than letting the stop race a reload, which is
//     what every release before this one did anyway.
//
// The check-then-act window that opens on the tolerated path is deliberate and
// harmless: the pass re-checks the lock itself and reports the refusal through
// recordUpdateFailure, so nothing is silently lost.
//
// release is IDEMPOTENT. A handler that hands work to a background goroutine
// must let go of the lock before the hand-off (handleStartNamespace,
// handleAppStart/Stop) while still deferring the release for its early returns;
// without idempotence that pattern is an "unlock of unlocked mutex" panic
// waiting for the first error path to be added.
func (d *Daemon) tryLongOp(w http.ResponseWriter, tolerate longOpKind) (release func(), ok bool) {
	if d.longOp.TryLock(longOpRequest) {
		return sync.OnceFunc(d.longOp.Unlock), true
	}
	// ONE read of the holder: it feeds both the policy check and the message,
	// and a second read could see a different (or no) holder.
	holder := d.longOp.Holder()
	if tolerate != longOpNone && holder == tolerate {
		return func() {}, true
	}
	writeErrorCode(w, http.StatusConflict, api.ErrCodeLongOpInProgress,
		holder.busyMessage()+" — wait for it to finish")
	return nil, false
}
