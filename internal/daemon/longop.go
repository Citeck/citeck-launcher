package daemon

import (
	"net/http"
	"sync"

	"github.com/citeck/citeck-launcher/internal/api"
)

// longOpBusyMessage is the single wording for "the daemon is in the middle of
// something that owns the namespace". Shared by the HTTP 409 and by the async
// Update & Start pass, which reports it through NamespaceDto.UpdateError
// instead — one action must not read as two different refusals depending on
// which layer caught it.
const longOpBusyMessage = "a snapshot or dependency migration is in progress"

// tryLongOp claims the long-operation lock for the duration of a synchronous
// handler. ok=false means a snapshot or a dependency migration is running and
// the 409 has already been written; the caller must return immediately.
// Callers `defer release()`.
//
// This is a TryLock, never a Lock: a mutating request must be refused while a
// migration runs, not queued behind it — a migration recreates containers and
// rewrites volumes for minutes, and an HTTP client waiting that long has
// already given up.
//
// release is IDEMPOTENT. A handler that hands work to a background goroutine
// must let go of the lock before the hand-off (see handleStartNamespace) while
// still deferring the release for its early returns; without idempotence that
// pattern is an "unlock of unlocked mutex" panic waiting for the first error
// path to be added.
func (d *Daemon) tryLongOp(w http.ResponseWriter) (release func(), ok bool) {
	if !d.longOpMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeLongOpInProgress,
			longOpBusyMessage+" — wait for it to finish")
		return nil, false
	}
	return sync.OnceFunc(d.longOpMu.Unlock), true
}
