package daemon

import (
	"crypto/rand"
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
)

// Keep completed outcomes independently of the high-volume application event
// ring. Active operations are never evicted; the last 128 completed operations
// remain queryable until daemon restart, even after an SSE replay gap.
const snapshotOperationHistory = 128

type snapshotOperationRecord struct {
	workspaceID         string
	namespaceID         string
	observerNamespaceID string
	event               api.EventDto
}

type snapshotOperation struct {
	d  *Daemon
	id string
}

func (d *Daemon) newSnapshotOperation(wsID, nsID string) snapshotOperation {
	op := snapshotOperation{d: d, id: rand.Text()}
	// Creation imports into a new namespace BEFORE activation. Keep the
	// request's origin able to observe its outcome as well as the target.
	act := d.active()
	observerNSID := nsID
	if act.workspaceID == wsID {
		observerNSID = ""
		if act.nsConfig != nil {
			observerNSID = act.nsConfig.ID
		}
	}
	d.snapshotOpsMu.Lock()
	defer d.snapshotOpsMu.Unlock()
	if d.snapshotOps == nil {
		d.snapshotOps = make(map[string]snapshotOperationRecord)
	}
	d.snapshotOps[op.id] = snapshotOperationRecord{workspaceID: wsID, namespaceID: nsID, observerNamespaceID: observerNSID, event: api.EventDto{OperationID: op.id, NamespaceID: nsID, Type: "snapshot_pending"}}
	return op
}

func (op snapshotOperation) emit(event api.EventDto) {
	event.OperationID = op.id
	op.d.snapshotOpsMu.Lock()
	record, found := op.d.snapshotOps[op.id]
	if !found {
		op.d.snapshotOpsMu.Unlock()
		return
	}
	wasTerminal := snapshotTerminal(record.event.Type)
	if wasTerminal {
		op.d.snapshotOpsMu.Unlock()
		return
	}
	if event.NamespaceID == "" {
		event.NamespaceID = record.namespaceID
	}
	record.event = event
	op.d.snapshotOps[op.id] = record
	if snapshotTerminal(event.Type) && !wasTerminal {
		op.d.snapshotCompleted = append(op.d.snapshotCompleted, op.id)
		if len(op.d.snapshotCompleted) > snapshotOperationHistory {
			oldest := op.d.snapshotCompleted[0]
			op.d.snapshotCompleted = op.d.snapshotCompleted[1:]
			delete(op.d.snapshotOps, oldest)
		}
	}
	op.d.snapshotOpsMu.Unlock()
	op.d.broadcastEvent(event)
}

func snapshotTerminal(kind string) bool {
	return kind == "snapshot_complete" || kind == "snapshot_error"
}

func (d *Daemon) handleSnapshotOperation(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	d.snapshotOpsMu.Lock()
	record, ok := d.snapshotOps[r.PathValue("id")]
	d.snapshotOpsMu.Unlock()
	nsID := ""
	if act.nsConfig != nil {
		nsID = act.nsConfig.ID
	}
	if !ok || record.workspaceID != act.workspaceID || (record.namespaceID != nsID && record.observerNamespaceID != nsID) {
		writeError(w, http.StatusNotFound, "snapshot operation status is unavailable; completion cannot be confirmed")
		return
	}
	writeJSON(w, record.event)
}
