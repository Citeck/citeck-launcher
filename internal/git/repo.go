package git

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// CloneOrPull clones a repo if not present, or pulls latest changes.
func CloneOrPull(repoURL, branch, destDir string) error {
	if _, err := os.Stat(filepath.Join(destDir, ".git")); err == nil {
		return pull(destDir, branch)
	}
	return clone(repoURL, branch, destDir)
}

func clone(repoURL, branch, destDir string) error {
	slog.Info("Cloning repository", "url", repoURL, "branch", branch, "dir", destDir)
	start := time.Now()

	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}

	cmd := exec.Command("git", "clone", "--branch", branch, "--depth", "1", repoURL, destDir)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", repoURL, err)
	}

	slog.Info("Repository cloned", "dir", destDir, "elapsed", time.Since(start))
	return nil
}

func pull(destDir, branch string) error {
	slog.Info("Pulling repository", "dir", destDir, "branch", branch)

	cmd := exec.Command("git", "-C", destDir, "pull", "--ff-only", "origin", branch)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull in %s: %w", destDir, err)
	}
	return nil
}
