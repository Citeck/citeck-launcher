//go:build unix

package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

// SameFilesystem reports whether two EXISTING paths live on the same
// filesystem. It is the honest form of a question callers otherwise answer by
// comparing path prefixes: a directory nested under another one is on a
// different filesystem the moment something is mounted in between, and two
// unrelated paths can share one.
//
// A path that cannot be stat'ed is an ERROR, never an answer. Every caller
// uses this to decide whether two writes have to fit in one place, so a
// guessed "different" silently halves what it demands — the same reason
// AvailableDiskSpace's callers must not read its 0 as "the disk is full".
//
// st_dev is the identity here: it is what the kernel itself uses to tell
// filesystems apart (a hard link across it is EXDEV), and it is stable for a
// mount for as long as it is mounted, which is longer than any preflight.
func SameFilesystem(a, b string) (bool, error) {
	da, err := statDev(a)
	if err != nil {
		return false, err
	}
	db, err := statDev(b)
	if err != nil {
		return false, err
	}
	return da == db, nil
}

// statDev is the device id of the filesystem holding path. It follows
// symlinks, because a symlinked volumes directory is answered by where it
// leads, not by where the link file sits.
func statDev(path string) (uint64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat %s: no filesystem id available", path)
	}
	return uint64(sys.Dev), nil //nolint:unconvert // Dev is uint64 on linux but int32 on darwin
}
