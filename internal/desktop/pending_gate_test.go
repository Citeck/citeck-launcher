package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stagedVersion is the payload every case here stages; the wrapper's own
// version in these tests is always older (2.5.0), so it is selectable.
const stagedVersion = "2.6.0"

// stagePayload writes a manifest entry in the given state, with a real file on
// disk (SelectBestEntry stats the path).
func stagePayload(t *testing.T, dir string, state update.State) {
	t.Helper()
	bin := filepath.Join(dir, stagedVersion+"-citeck")
	require.NoError(t, os.WriteFile(bin, []byte("payload"), 0o600))
	require.NoError(t, update.AddStaged(dir, update.Entry{Version: stagedVersion, Path: bin, SHA256: "x"}))
	require.NoError(t, update.MarkState(dir, stagedVersion, state))
}

func stateOf(t *testing.T, dir string) update.State {
	t.Helper()
	m, err := update.Load(dir)
	require.NoError(t, err)
	for _, e := range m.Entries {
		if e.Version == stagedVersion {
			return e.State
		}
	}
	return ""
}

// A pending payload that comes up is promoted, so it is not re-judged on every
// later start.
func TestGatePendingPayload_PromotesAHealthyOne(t *testing.T) {
	dir := t.TempDir()
	stagePayload(t, dir, update.StatePending)

	sv := &Supervisor{BinaryPath: "unused"}
	sv.ready.Store(true)

	assert.False(t, GatePendingPayload(t.Context(), sv, dir, "2.5.0", time.Second))
	assert.Equal(t, update.StateGood, stateOf(t, dir))
}

// The case the gate exists for: an update was staged and applied, the wrapper
// never got to health-gate it, and the payload does not come up. Before this,
// every subsequent start selected it again — forever, since nothing but the swap
// path ever writes `failed`.
func TestGatePendingPayload_FailsAndRollsBackADeadOne(t *testing.T) {
	dir := t.TempDir()
	stagePayload(t, dir, update.StatePending)

	sv := &Supervisor{BinaryPath: "unused"} // never becomes ready

	assert.True(t, GatePendingPayload(t.Context(), sv, dir, "2.5.0", 300*time.Millisecond))
	assert.Equal(t, update.StateFailed, stateOf(t, dir))

	// The rollback is only real if the payload stops being selected.
	_, ok := update.SelectBestEntry(dir, "2.5.0")
	assert.False(t, ok, "a failed payload must not stay selectable")
	assert.True(t, update.IsVersionFailed(dir, stagedVersion), "and must not be re-staged")
}

// Already judged, or nothing staged: the gate must not touch the manifest.
func TestGatePendingPayload_LeavesJudgedAndAbsentPayloadsAlone(t *testing.T) {
	dir := t.TempDir()
	stagePayload(t, dir, update.StateGood)
	sv := &Supervisor{BinaryPath: "unused"} // never ready: would fail the gate if it ran

	assert.False(t, GatePendingPayload(t.Context(), sv, dir, "2.5.0", 300*time.Millisecond))
	assert.Equal(t, update.StateGood, stateOf(t, dir))

	empty := t.TempDir()
	assert.False(t, GatePendingPayload(t.Context(), sv, empty, "2.5.0", 300*time.Millisecond))
}

// A payload the wrapper is quitting on has not been judged — recording a verdict
// there would condemn a possibly-healthy release on the strength of a shutdown.
func TestGatePendingPayload_RecordsNoVerdictWhenShuttingDown(t *testing.T) {
	dir := t.TempDir()
	stagePayload(t, dir, update.StatePending)
	sv := &Supervisor{BinaryPath: "unused"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.False(t, GatePendingPayload(ctx, sv, dir, "2.5.0", time.Second))
	assert.Equal(t, update.StatePending, stateOf(t, dir))
}
