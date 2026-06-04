package namespace

// NsStatePersister persists the per-namespace runtime state blob. It is
// implemented by the daemon (delegating to storage.Store, bound to a
// workspace+namespace) and injected into the runtime so internal/namespace
// never imports internal/storage. status is the denormalized current status;
// stateJSON is the marshaled NsPersistedState.
type NsStatePersister interface {
	SaveNamespaceState(status, stateJSON string) error
}
