package namespace

// The two pieces of content-storage state the launcher keeps: which storage it
// is in the middle of switching ecos-content to (NsPersistedState.
// ContentStorageSwitchPending), and what it last wrote into the ECOS secret it
// owns (NsPersistedState.ContentStorageSecretWrite). Neither is a verdict —
// that is re-derived from ECOS on every run. They live on the Runtime for the
// reason every persisted field does: persistState rebuilds the whole record
// from Runtime fields, so a value kept anywhere else would be erased by the
// next unrelated write.

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

// RestoreContentStorageSecretWrite installs the persisted record of the
// launcher's last S3 credentials write on the load path. No persist, for the
// reason RestoreContentStorageSwitch does not.
func (r *Runtime) RestoreContentStorageSecretWrite(w *ContentStorageSecretWrite) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contentStorageSecret = copySecretWrite(w)
}

// ContentStorageSecretWrite answers the launcher's record of its last write of
// the S3 credentials into ECOS — a copy — or nil when there is none.
func (r *Runtime) ContentStorageSecretWrite() *ContentStorageSecretWrite {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return copySecretWrite(r.contentStorageSecret)
}

// SetContentStorageSecretWrite records a write of the S3 credentials into ECOS
// and persists it inline, so the next daemon does not rewrite a secret that is
// already current. A failed write is returned and stays owed, as for the
// switch marker; losing it costs one redundant rewrite of the secret, never a
// stale one.
func (r *Runtime) SetContentStorageSecretWrite(w *ContentStorageSecretWrite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if secretWriteEqual(r.contentStorageSecret, w) {
		return nil
	}
	r.contentStorageSecret = copySecretWrite(w)
	return r.persistUnderLock("content-storage-secret-write")
}

func copySecretWrite(w *ContentStorageSecretWrite) *ContentStorageSecretWrite {
	if w == nil {
		return nil
	}
	cp := *w
	return &cp
}

func secretWriteEqual(a, b *ContentStorageSecretWrite) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
