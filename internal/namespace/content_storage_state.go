package namespace

// The runtime persists unfinished switches and confirmation history in the
// same state record as lifecycle data. Neither replaces reading ECOS config.

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
// into ECOS, and a daemon that dies between writing the default and
// confirming it must find it on its next start. A failed write is
// returned and stays owed — r.dirty keeps it for the loop tail, like every
// other inline mutation — and the in-memory value still serves this process.
func (r *Runtime) SetContentStorageSwitch(storageRef string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.contentStorageSwitch == storageRef && !r.dirty.Load() {
		return nil
	}
	r.contentStorageSwitch = storageRef
	return r.persistUnderLock("content-storage-switch")
}

// RestoreContentStorageDefaultSet restores switch history without persisting.
func (r *Runtime) RestoreContentStorageDefaultSet(set bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contentStorageDefaultSet = set
}

// ContentStorageDefaultSet distinguishes initial LOCAL from a later admin choice.
func (r *Runtime) ContentStorageDefaultSet() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contentStorageDefaultSet
}

// CompleteContentStorageSwitch atomically records confirmation and clears the WAL.
func (r *Runtime) CompleteContentStorageSwitch() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.contentStorageDefaultSet && r.contentStorageSwitch == "" && !r.dirty.Load() {
		return nil
	}
	r.contentStorageDefaultSet = true
	r.contentStorageSwitch = ""
	return r.persistUnderLock("content-storage-default-set")
}
