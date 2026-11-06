package launchermcp

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func prepareDBDirectory(raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", mcpStorageError(err)
	}
	parent := filepath.Dir(abs)
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return "", mcpStorageError(err)
	}
	// Canonicalize trusted profile ancestors, but never follow a reparse point
	// at the private leaf. Its protected DACL is inherited by SQLite sidecars.
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", mcpStorageError(err)
	}
	dir := filepath.Join(parent, filepath.Base(abs))
	if err = os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", mcpStorageError(err)
	}
	if err = privatePermissions(dir, true); err != nil {
		return "", mcpStorageError(err)
	}
	return dir, nil
}

func lockDBOwner(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, mcpStorageError(err)
	}
	// No delete sharing: another process cannot replace the locked pathname.
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, mcpStorageError(err)
	}
	f := os.NewFile(uintptr(h), path)
	if err = privatePermissions(path, false); err == nil {
		err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	}
	if err != nil {
		return nil, errors.Join(errors.New("MCP database is already owned or its lock is unsafe"), err, f.Close())
	}
	return f, nil
}

// Windows has no supported directory fsync equivalent. SQLite synchronous=FULL
// and File.Sync use FlushFileBuffers for file data; fence replacement below uses
// MoveFileEx WRITE_THROUGH for namespace persistence. Do not substitute a failing
// FlushFileBuffers(directory) or claim hardware power-loss validation.
func syncDBDirectory(path string) error {
	return mcpStorageError(privatePermissions(path, true))
}

func replaceSQLMarker(root *os.Root, dir, name string) error {
	pinned, err := root.Stat(".")
	if err != nil {
		return mcpStorageError(err)
	}
	actual, err := os.Lstat(dir)
	if err != nil || !os.SameFile(pinned, actual) {
		return errors.New("MCP private parent changed before fence replacement")
	}
	from, err := windows.UTF16PtrFromString(filepath.Join(dir, name))
	if err != nil {
		return mcpStorageError(err)
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(dir, "state.json"))
	if err != nil {
		return mcpStorageError(err)
	}
	return mcpStorageError(windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH))
}
