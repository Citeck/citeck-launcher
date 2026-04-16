package workers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpKindStringForEveryOp(t *testing.T) {
	cases := []struct {
		op   OpKind
		want string
	}{
		{OpPull, "pull"},
		{OpStart, "start"},
		{OpStop, "stop"},
		{OpInit, "init"},
		{OpProbe, "probe"},
		{OpRemoveNetwork, "removeNetwork"},
		{OpStats, "stats"},
		{OpReconcileDiff, "reconcileDiff"},
		{OpLivenessProbe, "livenessProbe"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.op.String(), "OpKind(%d).String()", int(c.op))
	}
	// Sentinel for unknown op; guards against silent fallthrough.
	assert.Equal(t, "unknown", OpKind(999).String())
}

func TestResultZeroValueUsable(t *testing.T) {
	var res Result
	assert.Equal(t, TaskID{}, res.TaskID)
	assert.Equal(t, int64(0), res.AttemptID)
	require.NoError(t, res.Err)
	assert.Nil(t, res.Payload)

	res = Result{
		TaskID:    TaskID{App: "x", Op: OpPull},
		AttemptID: 7,
		Err:       errors.New("boom"),
		Payload:   PullPayload{Digest: "sha256:abc"},
	}
	assert.Equal(t, "x", res.TaskID.App)
	assert.Equal(t, OpPull, res.TaskID.Op)
	assert.Equal(t, int64(7), res.AttemptID)
	require.Error(t, res.Err)
	p, ok := res.Payload.(PullPayload)
	assert.True(t, ok)
	assert.Equal(t, "sha256:abc", p.Digest)
}
