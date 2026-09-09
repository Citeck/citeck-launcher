package namespace

import (
	"log/slog"
	"time"
)

// Retry policy for a state write the store refused. persistUnderLock leaves
// r.dirty set on failure, so the runtimeLoop tail would otherwise re-attempt
// the write on EVERY iteration — ticker, command, worker result and signal
// alike — for as long as the store stays broken.
//
// Measured on the real loop (mock Docker, one and eight RUNNING apps, the
// production 1s ticker) with a store that always refuses:
//
//   - a store that fails instantly: ~2–2.7 attempts per second, independent of
//     the app count. Harmless on its own.
//   - a store that fails SLOWLY — 100 ms per call, the shape of a hung
//     network FS or a SQLite busy timeout: 24 attempts in 3.7 s, i.e. ~65% of
//     the loop's wall clock spent inside a dead store, and every attempt holds
//     r.mu for its whole latency: a concurrent r.Status() (an RLock, what the
//     DTO and the CLI take) measured a worst case of a full 100 ms.
//
// That is the case the backoff exists for. At a 5-second hang the loop would
// stop stepping apps, dispatching probes and answering readers altogether —
// the runtime would be unresponsive because its disk is, which is a much worse
// failure than the lost write the retry is there to prevent. Backing the retry
// off exponentially keeps the debt real (it is still retried until it lands)
// while capping what a permanently broken store costs the loop.
//
// Only the TAIL retry is gated. A mutator that writes inline records fresh
// user intent and returns the error to its caller, so it always attempts.
const (
	// defaultPersistRetryBase is the delay after the first failure; it doubles
	// per consecutive failure up to persistRetryMax. Overridable per Runtime
	// (tests shrink it, like tickerPeriod).
	defaultPersistRetryBase = 1 * time.Second
	persistRetryMax         = 60 * time.Second
)

// Retry policy for the FINAL state write, the one doDetach makes. The tail
// retry above does not cover it: after Detach() the runtime loop is gone, so
// nothing would ever try again, and the containers stay running for the next
// daemon to adopt with whatever the last successful write said.
//
// The budget is deliberately small, and both ends of it are set by measurement
// rather than taste:
//
//   - What a retry can actually fix here is TRANSIENT: a SQLite SQLITE_BUSY
//     (another connection holding the write lock), a brief EAGAIN, an NFS or
//     fuse hiccup. Those clear in milliseconds. The failures that motivate the
//     whole feature — a full disk, a read-only mount, EACCES, a damaged
//     database — answer identically at attempt 3 and at attempt 300, so a
//     longer budget buys nothing but a slower exit.
//   - The ceiling is set by what is already waiting on this shutdown. The
//     desktop supervisor force-kills the daemon 5s after the shutdown POST
//     (internal/desktop/supervisor.go daemonStopGrace) and `citeck install`
//     gives the whole stop 30s (internal/cli/installer_lifecycle.go
//     daemonStopTimeout) — and doDetach has ALREADY spent up to 5s polling for
//     worker drain before it reaches this write. A multi-second retry here
//     would turn "your state was not saved" into "your daemon was killed",
//     which loses the report as well as the state.
//
// Three attempts at 200ms and 400ms is ≤600ms of added wait, ~2 orders of
// magnitude above a healthy write and still under a tenth of the tightest
// budget above even against the pathological store measured for the tail retry
// (100ms per refusal ⇒ ~900ms total).
const (
	detachPersistAttempts  = 3
	detachPersistRetryBase = 200 * time.Millisecond
)

// persistOnDetach makes the runtime's last state write, retrying a bounded
// number of times before giving up. Returns the final error, or nil once a
// write lands.
//
// Must be called WITHOUT r.mu held: it takes the lock per attempt and sleeps
// between them unlocked, so a reader (r.Status(), the DTO) is not blocked for
// the whole budget. That is safe because doDetach runs on the runtimeLoop
// goroutine with r.detaching already set and every worker canceled — nothing
// else mutates the runtime while this runs.
func (r *Runtime) persistOnDetach() error {
	delay := detachPersistRetryBase
	var err error
	for attempt := 1; attempt <= detachPersistAttempts; attempt++ {
		r.mu.Lock()
		err = r.persistState()
		r.mu.Unlock()
		if err == nil {
			if attempt > 1 {
				slog.Info("Namespace state saved on detach after a retry",
					"namespace", r.nsID, "attempt", attempt)
			}
			return nil
		}
		if attempt == detachPersistAttempts {
			break
		}
		slog.Warn("Detach state write refused; retrying",
			"namespace", r.nsID, "attempt", attempt, "of", detachPersistAttempts, "err", err)
		time.Sleep(delay)
		delay *= 2
	}
	return err
}

// notePersistOutcomeUnderLock records the outcome of a state write: it keeps
// the consecutive-failure streak, schedules the next tail retry, and reports
// the streak's two edges to the log.
//
// The log policy is the point. A line per attempt would put a WARN in the
// daemon log a few times a second for as long as the disk is full, burying
// whatever the operator went looking for; one line per STREAK says the same
// thing, and the recovery line — carrying how many writes were refused — is
// what tells them it ended. Attempts in between are DEBUG.
//
// The streak lives on the Runtime, so it is per namespace (one namespace's
// broken store cannot mute another's report) and in memory only (a fresh
// process always reports its first failure).
//
// Must be called with r.mu held.
func (r *Runtime) notePersistOutcomeUnderLock(mutation string, err error) {
	if err != nil {
		r.persistFailStreak++
		if r.persistFailStreak == 1 {
			slog.Warn("Failed to persist namespace state; the write is still owed — the runtime loop retries it until one lands (a stopped namespace, which has no loop, retries at its next start)",
				"namespace", r.nsID, "mutation", mutation, "err", err)
		} else {
			slog.Debug("Namespace state write still failing",
				"namespace", r.nsID, "mutation", mutation, "consecutiveFailures", r.persistFailStreak, "err", err)
		}
		r.persistFailErr = err
		r.persistRetryAt = r.nowFunc().Add(r.persistRetryDelay())
		return
	}
	if r.persistFailStreak > 0 {
		slog.Info("Namespace state write recovered",
			"namespace", r.nsID, "mutation", mutation, "refusedWrites", r.persistFailStreak)
	}
	r.persistFailStreak = 0
	r.persistFailErr = nil
	r.persistRetryAt = time.Time{}
}

// StateWriteError reports why this namespace's state is not reaching the
// store, or "" when the last write landed.
//
// This is the operator-facing half of the retry above. The retry made the
// WRITE honest — a refused write is still owed and is attempted until one
// lands — but it did nothing for AWARENESS: every mutator that persists
// inline (StopApp, StartApp, RestartApp, UpdateAppDef, ResetAppDef,
// WriteEditedFile, ResetEditedFile, ClearRestartEvents) returns nil on a
// refused write, because the action itself succeeded; the container really
// stopped, the patch really applied. So on a store that keeps failing the CLI
// and the UI reported success while nothing was being recorded, and the
// operator found out at the next daemon start.
//
// It is deliberately derived from the failure STREAK rather than kept
// separately: one source of truth means what the surfaces report and what the
// loop is retrying cannot drift, and the condition clears itself the moment a
// write lands — including a write the tail made long after the response went
// out, which no per-call result could ever have covered.
func (r *Runtime) StateWriteError() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stateWriteErrorUnderLock()
}

// stateWriteErrorUnderLock is StateWriteError for callers already holding r.mu
// (ToNamespaceDto). Must be called with r.mu held.
func (r *Runtime) stateWriteErrorUnderLock() string {
	if r.persistFailStreak == 0 || r.persistFailErr == nil {
		return ""
	}
	return r.persistFailErr.Error()
}

// persistRetryDelay is the wait after the current failure streak: base,
// doubling per consecutive failure, capped at persistRetryMax. Must be called
// with r.mu held (reads the streak).
func (r *Runtime) persistRetryDelay() time.Duration {
	base := r.persistRetryBase
	if base <= 0 {
		base = defaultPersistRetryBase
	}
	d := base
	for i := 1; i < r.persistFailStreak; i++ {
		d *= 2
		if d >= persistRetryMax {
			return persistRetryMax
		}
	}
	if d > persistRetryMax {
		return persistRetryMax
	}
	return d
}

// persistRetryDueUnderLock reports whether the loop tail may attempt the owed
// write now. It is true whenever no failure streak is open, so an ordinary
// per-iteration persist is never delayed. Must be called with r.mu held.
func (r *Runtime) persistRetryDueUnderLock() bool {
	if r.persistRetryAt.IsZero() {
		return true
	}
	return !r.nowFunc().Before(r.persistRetryAt)
}
