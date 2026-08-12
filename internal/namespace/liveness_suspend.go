package namespace

import (
	"log/slog"
	"sync"
)

// Suspending an app's liveness probe for the duration of a deliberate
// diagnostic.
//
// A `GC.heap_dump` on a multi-GB heap stops the world for longer than any
// tolerance window worth having: a stop-the-world pause does not refuse the
// probe's connection, it just never answers, so every probe inside the pause
// burns its full timeout and counts as a failure. The launcher is the one
// asking for the dump, so it is the launcher that must stop watching — for
// exactly as long as it takes and not a tick longer.
//
// The suspension is a FLAG the runtime loop reads; nothing outside the loop
// touches livenessNextAt. The loop keeps owning probe scheduling (it pushes the
// next probe out while the flag is up), which is what keeps this from becoming
// a second, racing scheduler.

// SuspendLivenessProbe suspends the liveness probe for one app and returns the
// function that resumes it. Callers MUST defer the release: an app left
// unwatched because a diagnostic returned early is a worse failure than the one
// being diagnosed.
//
// Suspensions are refcounted — a thread dump taken while a heap dump is running
// is a normal thing to want, and the first release must not re-arm the probe
// under the second holder. Release is idempotent.
func (r *Runtime) SuspendLivenessProbe(appName string) func() {
	r.mu.Lock()
	r.livenessSuspended[appName]++
	depth := r.livenessSuspended[appName]
	r.mu.Unlock()
	slog.Debug("Liveness probe suspended", "app", appName, "holders", depth)

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.livenessSuspended[appName] <= 1 {
				delete(r.livenessSuspended, appName)
			} else {
				r.livenessSuspended[appName]--
			}
			remaining := r.livenessSuspended[appName]
			r.mu.Unlock()
			slog.Debug("Liveness probe resumed", "app", appName, "holders", remaining)
		})
	}
}

// WithLivenessSuspended runs fn with the app's liveness probe suspended and
// resumes it whatever fn does — return, error or panic.
func (r *Runtime) WithLivenessSuspended(appName string, fn func() error) error {
	release := r.SuspendLivenessProbe(appName)
	defer release()
	return fn()
}

// livenessSuspendedLocked reports whether the app's probe is currently
// suspended. Caller holds r.mu.
func (r *Runtime) livenessSuspendedLocked(appName string) bool {
	return r.livenessSuspended[appName] > 0
}
