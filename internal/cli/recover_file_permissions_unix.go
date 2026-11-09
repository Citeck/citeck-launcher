//go:build !windows

package cli

import (
	"fmt"
	"os"
)

// The owner-only backup directory protects the moved entry without changing
// the original's mode/ownership, so a failed state write can restore it intact.
func secureRecoveryBackup(root *os.Root, leaf string) error {
	if leaf != "." {
		return nil
	}
	if err := root.Chmod(".", 0o700); err != nil {
		return fmt.Errorf("protect recovery directory: %w", err)
	}
	return nil
}

func syncRecoveryDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open recovery directory for sync: %w", err)
	}
	defer f.Close()
	if err = f.Sync(); err != nil {
		return fmt.Errorf("sync recovery directory: %w", err)
	}
	return nil
}
