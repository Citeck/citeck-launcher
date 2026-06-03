package desktop

import "os"

// SelectDaemonBinary returns the path to the daemon binary the supervisor
// should exec as the child process.
//
// Spec 2a: always the running (bundled) executable. This is the seam Spec 2b
// extends to prefer the newest healthy payload under config.UpdatesDir(),
// falling back here when none exists or all are marked failed.
func SelectDaemonBinary() (string, error) {
	return os.Executable() //nolint:wrapcheck // trivial passthrough; caller contextualizes
}
