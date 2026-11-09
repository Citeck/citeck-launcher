package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Chmod alone does not make a Windows backup private. Set an owner-only,
// protected inheritable DACL on the central backup directories. Original entries
// keep their ACL for rollback; copied bytes inherit this private DACL. Pin and
// identity-check the actual handle before changing security.
func secureRecoveryBackup(root *os.Root, leaf string) error {
	expected, err := root.Lstat(leaf)
	if err != nil {
		return fmt.Errorf("inspect private recovery entry: %w", err)
	}
	native, err := windows.UTF16PtrFromString(filepath.Join(root.Name(), leaf))
	if err != nil {
		return fmt.Errorf("private recovery path: %w", err)
	}
	handle, err := windows.CreateFile(native, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("open private recovery entry: %w", err)
	}
	f := os.NewFile(uintptr(handle), leaf)
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(expected, actual) {
		return errors.New("recovery entry changed before setting private permissions")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("recovery owner: %w", err)
	}
	inherit := ""
	if actual.IsDir() {
		inherit = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return fmt.Errorf("private recovery security descriptor: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("private recovery ACL: %w", err)
	}
	if err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("restrict recovery ACL: %w", err)
	}
	return nil
}

// Windows does not support POSIX directory fsync through os.File.Sync.
// Backup files themselves are synced; cross-file power-loss atomicity is not
// promised on Windows (see offline-file-recovery.md).
func syncRecoveryDirectory(_ *os.Root) error { return nil }
