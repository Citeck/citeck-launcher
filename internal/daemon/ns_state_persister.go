package daemon

import (
	"encoding/json"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// nsStatePersister adapts storage.Store to namespace.NsStatePersister, bound
// to one workspace+namespace. Injected into a Runtime so the runtime persists
// state through the store without importing internal/storage.
type nsStatePersister struct {
	store      storage.Store
	wsID, nsID string
}

func (p nsStatePersister) SaveNamespaceState(status, stateJSON string) error {
	return p.store.SaveNamespaceState(p.wsID, p.nsID, status, stateJSON) //nolint:wrapcheck // thin adapter
}

// loadNsStateFromStore reads + unmarshals the persisted runtime state for a
// namespace. Returns nil when absent or unparseable (caller treats nil as
// "first start" — matches the old file-based LoadNsState contract).
func loadNsStateFromStore(store storage.Store, wsID, nsID string) *namespace.NsPersistedState {
	js, ok, err := store.LoadNamespaceState(wsID, nsID)
	if err != nil {
		slog.Warn("Failed to load namespace state", "ws", wsID, "ns", nsID, "err", err)
		return nil
	}
	if !ok || js == "" {
		return nil
	}
	var st namespace.NsPersistedState
	if err := json.Unmarshal([]byte(js), &st); err != nil {
		slog.Warn("Failed to unmarshal namespace state", "ws", wsID, "ns", nsID, "err", err)
		return nil
	}
	return &st
}
