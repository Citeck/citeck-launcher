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
		r.persistRetryAt = r.nowFunc().Add(r.persistRetryDelay())
		return
	}
	if r.persistFailStreak > 0 {
		slog.Info("Namespace state write recovered",
			"namespace", r.nsID, "mutation", mutation, "refusedWrites", r.persistFailStreak)
	}
	r.persistFailStreak = 0
	r.persistRetryAt = time.Time{}
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
