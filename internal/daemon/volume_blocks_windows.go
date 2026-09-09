package daemon

import "io/fs"

// fileAllocatedBytes has no answer on Windows: the allocated size lives in
// FILE_STANDARD_INFO and is not carried on the fs.FileInfo the standard library
// hands back. 0 means "not answerable here", so depsEnv.VolumeSize falls back
// to the apparent size alone.
//
// That fallback is never the load-bearing answer in practice: the branch this
// feeds is SERVER mode, and server mode is Linux only (install.sh refuses every
// other OS). On Windows the launcher runs in desktop mode, whose volumes are
// named Docker volumes measured inside the engine by a utils container.
func fileAllocatedBytes(fs.FileInfo) int64 { return 0 }
