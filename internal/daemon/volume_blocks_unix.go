//go:build !windows

package daemon

import (
	"io/fs"
	"syscall"
)

// fileAllocatedBytes is how much disk a file actually OCCUPIES, as opposed to
// how large it claims to be. The two differ in both directions and a volume's
// space requirement is the larger of them (see depsEnv.VolumeSize): a sparse
// file occupies far less than its size, and a small or preallocated one
// occupies more.
//
// st_blocks is in 512-byte units by POSIX definition — NOT in the filesystem's
// block size — on every unix Go builds for.
//
// 0 means "not answerable here", which the caller reads as "no allocation
// information", never as "this file is empty".
func fileAllocatedBytes(info fs.FileInfo) int64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return st.Blocks * 512
}
