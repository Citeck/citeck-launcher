package daemon

import (
	"net/http"
	"slices"
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
	//
	// TRAP for whoever wires the migration engine: it MUST claim the lock
	// itself — d.longOp.TryLock(longOpMigration), ownership transferred into
	// the background goroutine — and must NOT go through tryLongOp, which
	// labels its holder longOpRequest. longOpRequest is in
	// tolerateLifecycleWork, so a migration mislabelled that way would let
	// namespace Start/Stop and the per-app toggles run right beside the volume
	// rewrite it exists to protect, with no error anywhere.
	longOpMigration longOpKind = "migration"
	// longOpUpdatePass is an asynchronous pass that re-drives the namespace on
	// the user's behalf — the queued Update & Start pass, and the attach-toggle
	// regeneration. Together with longOpRequest it is tolerated by the five
	// lifecycle routes (see tolerateLifecycleWork): the pass IS a start,
	// refusing Stop would take away the escape hatch for a pass stuck in a slow
	// git pull, and refusing the per-app toggles would break a burst of them.
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
//
// Between the mutex being taken and the owner being stored there is a window,
// nanoseconds wide, in which a concurrent reader sees the lock held and the
// owner still longOpNone. It fails CLOSED both ways: the refusal message
// degrades to the vague wording, and a TOLERATED route (Start, Stop, a per-app
// toggle) is refused with a spurious 409 instead of proceeding, because
// longOpNone is in no tolerance set. Refusing a click that a retry fixes is the
// right side to fail on; the alternative — storing the owner before the lock —
// would let a reader attribute the lock to a kind that never held it.
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

// longOpTolerance is the SET of holder kinds a route may proceed ALONGSIDE.
// The split it encodes is between the two daemon-owned long operations that
// take the namespace's data away from under everyone — a snapshot restoring
// volumes, a dependency migration rewriting them — and ordinary namespace
// lifecycle work, which the launcher has always allowed to overlap.
type longOpTolerance []longOpKind

// allows reports whether a route with this policy may run beside holder.
// longOpNone is not a member of any policy, so a holder that let go between the
// failed TryLock and the read is refused like any unknown one — the lock being
// genuinely free is the plain TryLock's case, not this one.
func (t longOpTolerance) allows(holder longOpKind) bool {
	return slices.Contains(t, holder)
}

var (
	// tolerateNothing refuses whoever holds the lock. The policy of 14 of the
	// 19 gated routes — everything that is not lifecycle work: an app config or
	// file edit, a reload, a bundle upgrade, a namespace edit/delete/activate,
	// a workspace switch or delete, a volume delete. Any of those beside ANY
	// long operation is exactly what the gate is for.
	tolerateNothing longOpTolerance
	// tolerateLifecycleWork is the policy of the five LIFECYCLE routes:
	// handleStartNamespace, handleStopNamespace, and the three per-app toggles
	// handleAppStart, handleAppStop, handleAppRestart. They are refused by
	// longOpSnapshot and longOpMigration — the two operations that own the
	// namespace's data — and tolerate the two that are just the launcher
	// working on the namespace:
	//
	//   - longOpUpdatePass: an ordinary start, or an attach-toggle
	//     regeneration. A second namespace click must FOLD into the single-slot
	//     update queue (the documented contract, force OR-ed) rather than 409,
	//     and Stop is the escape hatch from a pass stuck in a slow git pull.
	//     Refusing either would turn the git-pull/generate window of a normal
	//     start into a daemon-wide refusal — and for the per-app toggles it
	//     would 409 every app after the first in `citeck stop onlyoffice
	//     attorneys ecom …`, since the first toggle's regeneration holds the
	//     lock for its whole doReload.
	//   - longOpRequest: another synchronous mutating handler. handleReload-
	//     Namespace holds the lock for the whole of doReload — minutes on an
	//     enterprise namespace — and a Stop, or a per-app toggle, during a
	//     reload has been accepted by every release before this feature
	//     (Runtime.Stop only enqueues a command). Refusing it would be a new,
	//     unexplained dead end.
	tolerateLifecycleWork = longOpTolerance{longOpUpdatePass, longOpRequest}
)

// tryLongOp claims the long-operation lock for the duration of a synchronous
// handler. ok=false means the daemon is busy with something that owns the
// namespace, the 409 naming that holder has already been written, and the
// caller must return immediately. Callers `defer release()`.
//
// tolerate is the policy (see longOpTolerance). A tolerated holder returns
// ok=true with a NO-OP release — the route runs WITHOUT the lock, exactly as it
// did before this feature existed.
//
// The check-then-act window that opens on the tolerated path is deliberate and
// harmless: the update pass re-checks the lock itself and reports the refusal
// through recordUpdateFailure, so nothing is silently lost.
//
// release is IDEMPOTENT. A handler that hands work to a background goroutine
// must let go of the lock before the hand-off (handleStartNamespace,
// handleAppStart/Stop) while still deferring the release for its early returns;
// without idempotence that pattern is an "unlock of unlocked mutex" panic
// waiting for the first error path to be added.
func (d *Daemon) tryLongOp(w http.ResponseWriter, tolerate longOpTolerance) (release func(), ok bool) {
	if d.longOp.TryLock(longOpRequest) {
		return sync.OnceFunc(d.longOp.Unlock), true
	}
	// ONE read of the holder: it feeds both the policy check and the message,
	// and a second read could see a different (or no) holder.
	holder := d.longOp.Holder()
	if tolerate.allows(holder) {
		return func() {}, true
	}
	writeErrorCode(w, http.StatusConflict, api.ErrCodeLongOpInProgress,
		holder.busyMessage()+" — wait for it to finish")
	return nil, false
}
