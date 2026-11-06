package fsutil

import "os"

// Go's Windows file flags use this value for FILE_FLAG_OPEN_REPARSE_POINT
// (os.Root.OpenFile passes the flag through to Openat). Stat then inspects the
// reparse point itself; OpenRegularRooted refuses it rather than its target.
const openReparsePoint = 0x00200000

func openRootedNoFollow(root *os.Root, leaf string) (*os.File, error) {
	return root.OpenFile(leaf, os.O_RDONLY|openReparsePoint, 0)
}
