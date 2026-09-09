package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// nsStub answers GetNamespace with whatever the test set.
type nsStub struct {
	ns   *api.NamespaceDto
	err  error
	call int
}

func (s *nsStub) GetNamespace() (*api.NamespaceDto, error) {
	s.call++
	return s.ns, s.err
}

func withEnglish(t *testing.T) {
	t.Helper()
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
}

// The `citeck status` line. A store that keeps refusing is otherwise invisible
// to the CLI: the per-app commands answered success (they did succeed), and the
// only trace is one WARN in the daemon log.
func TestStateWriteStatusLine(t *testing.T) {
	withEnglish(t)

	assert.Empty(t, stateWriteStatusLine(nil), "no namespace, no line")
	assert.Empty(t, stateWriteStatusLine(&api.NamespaceDto{}),
		"a namespace whose writes are landing must not carry the line")

	line := stateWriteStatusLine(&api.NamespaceDto{StateWriteError: "disk quota exceeded"})
	assert.Contains(t, line, "disk quota exceeded",
		"the line must name the store's own reason — it is the only actionable part")
}

// The per-app action commands. The action really happened, so the exit code
// stays 0 and the command's own success message stands; what is added is that
// nothing recorded it.
func TestAppliedButNotSavedWarning(t *testing.T) {
	withEnglish(t)

	t.Run("clean store says nothing", func(t *testing.T) {
		c := &nsStub{ns: &api.NamespaceDto{}}
		assert.Empty(t, appliedButNotSavedWarning(c))
		assert.Equal(t, 1, c.call)
	})

	t.Run("refused write is reported with its consequence", func(t *testing.T) {
		c := &nsStub{ns: &api.NamespaceDto{StateWriteError: "disk quota exceeded"}}
		lines := appliedButNotSavedWarning(c)
		require.NotEmpty(t, lines)
		joined := strings.Join(lines, "\n")
		assert.Contains(t, joined, "disk quota exceeded")
		// Naming the failure without naming what it costs is what left the
		// operator guessing in the first place.
		assert.Contains(t, strings.ToLower(joined), "restart",
			"the warning must say what is lost, and when")
	})

	// Best-effort by design: this runs AFTER an action that already succeeded,
	// so a daemon that will not answer (an older one with no such field, a
	// namespace that is not configured) must not turn a successful command
	// into a failed one.
	t.Run("an unreachable daemon is not an error", func(t *testing.T) {
		c := &nsStub{err: errors.New("connection refused")}
		assert.Empty(t, appliedButNotSavedWarning(c))
	})

	t.Run("a nil namespace is not an error", func(t *testing.T) {
		c := &nsStub{}
		assert.Empty(t, appliedButNotSavedWarning(c))
	})
}

// The binary-upgrade abort. By the time this renders, the daemon is already
// down and the containers are already detached — aborting undoes nothing. Its
// value is that the operator is told BEFORE a version change is layered on top
// of a lost state, and that restarting the same binary is the safe next step.
// So the text has to carry all three: what failed, what is stale, and what to
// do.
func TestStateNotSavedAbortLines(t *testing.T) {
	withEnglish(t)
	setInstallTarget(t, "/opt/citeck/bin/citeck")

	joined := strings.Join(stateNotSavedAbortLines("disk quota exceeded"), "\n")

	assert.Contains(t, joined, "disk quota exceeded", "the cause")
	assert.Contains(t, joined, "/opt/citeck/bin/citeck",
		"the binary that was NOT replaced must be named — it is what the operator restarts")
	low := strings.ToLower(joined)
	assert.Contains(t, low, "not", "the abort must say the binary was not changed")
	for _, stale := range []string{"config", "depend"} {
		assert.Contains(t, low, stale, "the text must say WHAT the next daemon would get wrong")
	}
}

// The sentinel the upgrade refuses on has to survive errors.As through the
// wrapping the lifecycle does, and has to name the store's reason.
func TestStateNotSavedErrorCarriesTheDetail(t *testing.T) {
	err := detachStateError(&api.ActionResultDto{Success: true, StateSaveError: "disk quota exceeded"})
	require.Error(t, err)
	var target *stateNotSavedError
	require.ErrorAs(t, err, &target)
	assert.Equal(t, "disk quota exceeded", target.detail)
	assert.Contains(t, err.Error(), "disk quota exceeded")

	assert.NoError(t, detachStateError(&api.ActionResultDto{Success: true}),
		"a clean detach must not be turned into a failure")
	assert.NoError(t, detachStateError(nil),
		"an older daemon answers no such field — that is not evidence of a loss")
}
