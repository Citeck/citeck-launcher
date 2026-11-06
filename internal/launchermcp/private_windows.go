package launchermcp

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows Chmod(0600) does not remove inherited read ACEs. Restrict only this
// isolated MCP directory/file, never the launcher home or other configuration.
func privatePermissions(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return mcpStorageError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return errors.New("MCP private storage must not be a symlink or special file")
	}
	nativePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return mcpStorageError(err)
	}
	attributes, err := windows.GetFileAttributes(nativePath)
	if err != nil {
		return mcpStorageError(err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("MCP private storage must not be a reparse point")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return mcpStorageError(err)
	}
	existing, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return mcpStorageError(err)
	}
	owner, _, err := existing.Owner()
	if err != nil {
		return mcpStorageError(err)
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return errors.New("MCP private storage must be owned by the daemon user")
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return mcpStorageError(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return mcpStorageError(err)
	}
	return mcpStorageError(windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
}
