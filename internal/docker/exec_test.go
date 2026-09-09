package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecInContainerAppendsStderrToStdout pins the concatenation ORDER of the
// convenience wrapper. It is a contract and not a detail: every caller that
// still uses ExecInContainer scans one transcript for a marker (the Keycloak
// and RabbitMQ init actions, the exec probes), and a version that returned
// stderr first would put a diagnostic line ahead of the output the caller
// matches on. The split call is injected because the real one needs a live
// engine; the join is the only thing this wrapper does.
func TestExecInContainerAppendsStderrToStdout(t *testing.T) {
	split := func(_ context.Context, _ string, _ []string) (string, string, int, error) {
		return "on-stdout\n", "on-stderr\n", 0, nil
	}

	out, code, err := execInContainerVia(context.Background(), split, "cid", []string{"true"})

	require.NoError(t, err)
	assert.Equal(t, "on-stdout\non-stderr\n", out,
		"stdout first, stderr appended, nothing inserted between them")
	assert.Equal(t, 0, code)
}

// TestExecInContainerPassesTheCommandThroughUntouched guards the other half of
// a wrapper: it must forward what it was given. A wrapper that dropped or
// reordered argv would run a different command than the caller asked for, and
// the whole jcmd line going into ONE argument slot is exactly the class of
// mistake this launcher has already paid for once.
func TestExecInContainerPassesTheCommandThroughUntouched(t *testing.T) {
	var gotID string
	var gotCmd []string
	split := func(_ context.Context, containerID string, cmd []string) (string, string, int, error) {
		gotID, gotCmd = containerID, cmd
		return "", "", 0, nil
	}

	_, _, err := execInContainerVia(context.Background(), split, "citeck_postgres_prod",
		[]string{"psql", "-c", "select 1"})

	require.NoError(t, err)
	assert.Equal(t, "citeck_postgres_prod", gotID)
	assert.Equal(t, []string{"psql", "-c", "select 1"}, gotCmd)
}

// TestExecInContainerReportsAFailureWithoutLosingWhatWasWritten pins that the
// wrapper is transparent about the two things its callers separate: err means
// the command could not be RUN, exitCode means it ran and failed — and neither
// may swallow the output already produced, which is the only evidence a caller
// has about what happened before the failure.
func TestExecInContainerReportsAFailureWithoutLosingWhatWasWritten(t *testing.T) {
	boom := errors.New("no such container")
	split := func(_ context.Context, _ string, _ []string) (string, string, int, error) {
		return "partial", "boom", -1, boom
	}

	out, code, err := execInContainerVia(context.Background(), split, "cid", []string{"true"})

	require.ErrorIs(t, err, boom)
	assert.Equal(t, -1, code)
	assert.Equal(t, "partialboom", out)

	failed := func(_ context.Context, _ string, _ []string) (string, string, int, error) {
		return "", "psql: FATAL\n", 2, nil
	}
	out, code, err = execInContainerVia(context.Background(), failed, "cid", []string{"psql"})
	require.NoError(t, err, "a command that ran and failed is not an error")
	assert.Equal(t, 2, code)
	assert.Equal(t, "psql: FATAL\n", out)
}
