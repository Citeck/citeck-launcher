package bundle

// Test helpers that must be callable from OTHER packages' tests (the daemon's,
// chiefly), which _test.go files cannot provide. Mirrors
// internal/namespace/runtime_testutil.go.

// SetWorkspaceSyncErrForTest injects the workspace-repo sync error the resolver
// would have recorded, so a caller can exercise how that failure is reported
// without standing up an unreachable git remote.
func (r *Resolver) SetWorkspaceSyncErrForTest(err error) {
	r.wsSyncErr = err
}
