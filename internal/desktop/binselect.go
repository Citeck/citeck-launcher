package desktop

import (
	"os"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/update"
)

// SelectDaemonBinary returns the path to the daemon binary the supervisor should
// exec as the child process.
//
// Prefer the newest healthy (good/pending) staged payload under
// config.UpdatesDir() whose version is strictly newer than currentVersion
// (never-downgrade), falling back to the running (bundled) executable when none
// qualifies. currentVersion is the wrapper's own ldflags-injected version.
func SelectDaemonBinary(currentVersion string) (string, error) {
	return selectDaemonBinaryFrom(config.UpdatesDir(), currentVersion)
}

// selectDaemonBinaryFrom is the testable core (explicit updates dir).
func selectDaemonBinaryFrom(updatesDir, currentVersion string) (string, error) {
	if path, ok := update.SelectBest(updatesDir, currentVersion); ok {
		return path, nil
	}
	return os.Executable() //nolint:wrapcheck // trivial passthrough; caller contextualizes
}
