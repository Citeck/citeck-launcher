package namespace

// The one piece of content-storage state the launcher keeps: which storage it
// is in the middle of switching ecos-content to (NsPersistedState.
// ContentStorageSwitchPending). It lives on the Runtime for the reason every
// persisted field does — persistState rebuilds the whole record from Runtime
// fields, so a value kept anywhere else would be erased by the next unrelated
// write.

// RestoreContentStorageSwitch installs the persisted marker on the load path.
// No persist, for the reason RestoreDependencyState does not: the load path
// runs before the caller has acted on ShouldStart, and a write there would
// record r.status while it is still the placeholder STOPPED.
func (r *Runtime) RestoreContentStorageSwitch(storageRef string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contentStorageSwitch = storageRef
}

// ContentStorageSwitch answers the storage ref whose default switch the
// launcher started and has not seen confirmed, "" when none.
func (r *Runtime) ContentStorageSwitch() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contentStorageSwitch
}

// SetContentStorageSwitch records (a ref) or clears ("") the marker and
// persists it inline: it is written right before the launcher's first write
// into ECOS, and a daemon that dies between creating the storage and
// switching the default must find it on its next start. A failed write is
// returned and stays owed — r.dirty keeps it for the loop tail, like every
// other inline mutation — and the in-memory value still serves this process.
func (r *Runtime) SetContentStorageSwitch(storageRef string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.contentStorageSwitch == storageRef {
		return nil
	}
	r.contentStorageSwitch = storageRef
	return r.persistUnderLock("content-storage-switch")
}
