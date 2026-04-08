package desktop

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the given URL in the default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // G204: URL comes from internal link generation, not user input
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // G204: URL comes from internal link generation, not user input
	default:
		cmd = exec.Command("xdg-open", url) //nolint:gosec // G204: URL comes from internal link generation, not user input
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	return nil
}
