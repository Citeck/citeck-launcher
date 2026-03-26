package cli

import (
	"errors"
	"testing"
)

func TestExitCodes_UniqueValues(t *testing.T) {
	codes := map[int]string{
		ExitOK:                "OK",
		ExitError:             "ERROR",
		ExitConfigError:       "CONFIG_ERROR",
		ExitDaemonNotRunning:  "DAEMON_NOT_RUNNING",
		ExitNotConfigured:     "NOT_CONFIGURED",
		ExitNotFound:          "NOT_FOUND",
		ExitDockerUnavailable: "DOCKER_UNAVAILABLE",
		ExitTimeout:           "TIMEOUT",
		ExitUnhealthy:         "UNHEALTHY",
		ExitConflict:          "CONFLICT",
	}

	// If any code is duplicated, the map will have fewer entries
	if len(codes) != 10 {
		t.Errorf("expected 10 unique exit codes, got %d (some codes are duplicated)", len(codes))
	}
}

func TestExitCodes_CorrectValues(t *testing.T) {
	tests := []struct {
		name string
		code int
		want int
	}{
		{"OK", ExitOK, 0},
		{"ERROR", ExitError, 1},
		{"CONFIG_ERROR", ExitConfigError, 2},
		{"DAEMON_NOT_RUNNING", ExitDaemonNotRunning, 3},
		{"NOT_CONFIGURED", ExitNotConfigured, 4},
		{"NOT_FOUND", ExitNotFound, 5},
		{"DOCKER_UNAVAILABLE", ExitDockerUnavailable, 6},
		{"TIMEOUT", ExitTimeout, 7},
		{"UNHEALTHY", ExitUnhealthy, 8},
		{"CONFLICT", ExitConflict, 9},
	}

	for _, tt := range tests {
		if tt.code != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, tt.code, tt.want)
		}
	}
}

func TestExitCodeError(t *testing.T) {
	err := exitWithCode(ExitTimeout, "timed out after %ds", 30)

	// Should implement error interface
	if err.Error() != "timed out after 30s" {
		t.Errorf("Error() = %q", err.Error())
	}

	// Should be extractable via errors.As
	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatal("errors.As failed for ExitCodeError")
	}
	if ece.Code != ExitTimeout {
		t.Errorf("Code = %d, want %d", ece.Code, ExitTimeout)
	}

	// Unwrap should return inner error
	if err.Unwrap() == nil {
		t.Fatal("Unwrap returned nil")
	}
}
