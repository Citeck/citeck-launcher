package daemon

import (
	"context"
	"net/http"

	"github.com/citeck/citeck-launcher/internal/launchermcp"
)

type targetLockKey struct{}

func targetOf(a activeNamespace) launchermcp.Target {
	v := launchermcp.Target{WorkspaceID: a.workspaceID}
	if a.nsConfig != nil {
		v.NamespaceID = a.nsConfig.ID
	}
	return v
}
func sameActiveTarget(a, b activeNamespace) bool {
	return targetOf(a) == targetOf(b) && a.runtime == b.runtime
}

// targetMu never replaces longOp or reloadMu. Both Stop and a slow reload take
// its read side, so Stop remains an escape hatch. Selection uses TryLock and
// refuses instead of waiting while a target-pinned operation is in progress.
func (d *Daemon) pinActiveOperation(r *http.Request) func() {
	if held, _ := r.Context().Value(targetLockKey{}).(bool); held {
		return func() {}
	}
	d.targetMu.RLock()
	return d.targetMu.RUnlock
}
func (d *Daemon) pinSelection(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if held, _ := r.Context().Value(targetLockKey{}).(bool); held {
		return func() {}, true
	}
	if !d.targetMu.TryLock() {
		writeErrorCode(w, 409, "TARGET_BUSY", "target operation in progress")
		return nil, false
	}
	return d.targetMu.Unlock, true
}
func targetHeldContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, targetLockKey{}, true)
}
