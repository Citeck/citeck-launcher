package cli

import "fmt"

const (
	ExitOK               = 0
	ExitError            = 1
	ExitConfigError      = 2
	ExitDaemonNotRunning = 3
	ExitNotConfigured    = 4
	ExitNotFound         = 5
	ExitDockerUnavailable = 6
	ExitTimeout          = 7
	ExitUnhealthy        = 8
	ExitConflict         = 9
)

// ExitCodeError wraps an error with a specific exit code.
// Returned from RunE; handled in Execute() to call os.Exit with the code.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e ExitCodeError) Error() string { return e.Err.Error() }
func (e ExitCodeError) Unwrap() error { return e.Err }

func exitWithCode(code int, msg string, args ...any) ExitCodeError {
	return ExitCodeError{Code: code, Err: fmt.Errorf(msg, args...)}
}
