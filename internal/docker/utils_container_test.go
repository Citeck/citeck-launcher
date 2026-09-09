package docker

import (
	"errors"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAFailedInspectIsAnErrorNotAnExitCode is the whole point of utilsExitCode.
// RunUtilsContainer used to answer a failed inspect with (exitCode -1, err
// nil), which is indistinguishable from a command that RAN and failed — and
// its callers are all written as "err means it could not be run, a code means
// it ran": the dependency-pin seeding probe reads PG_VERSION out of a volume
// through this call and its verdict is deliberately tri-state, so blurring
// "the engine would not tell us how the cat exited" into "the cat failed"
// turns an infrastructure problem into a claim about the user's data. The same
// rule the migration Env already states for ContainerRunning: an absent
// container is an ANSWER, a failed inspect is not.
func TestAFailedInspectIsAnErrorNotAnExitCode(t *testing.T) {
	code, err := utilsExitCode(container.InspectResponse{}, errors.New("connection refused"))

	require.Error(t, err, "a failed inspect must not be reported as a plausible-looking exit code")
	assert.Contains(t, err.Error(), "connection refused", "the engine's own error must survive to the caller")
	assert.Equal(t, -1, code)
}

// TestAnInspectWithNoStateIsAnErrorNotAPanic pins the second failure shape of
// the same call. InspectResponse.State is a POINTER, so the old
// inspect.State.ExitCode dereferenced whatever the engine sent: a response
// without a state block panicked inside the daemon instead of reporting that
// the exit code is unknown.
func TestAnInspectWithNoStateIsAnErrorNotAPanic(t *testing.T) {
	code, err := utilsExitCode(container.InspectResponse{State: nil}, nil)

	require.Error(t, err)
	assert.Equal(t, -1, code)
}

// TestASuccessfulInspectReportsTheContainersOwnExitCode pins the ordinary
// path, both halves of it: a utils container that succeeded reports 0 with no
// error (a `cat` that read the file), and one that failed reports ITS code
// with no error, so the caller can classify the failure from the output.
func TestASuccessfulInspectReportsTheContainersOwnExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 137} {
		code, err := utilsExitCode(container.InspectResponse{
			State: &container.State{ExitCode: want},
		}, nil)

		require.NoError(t, err, "a container that ran is never an error, whatever its code")
		assert.Equal(t, want, code)
	}
}
