package desktop

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/update"
)

// fakeRestarter records the health budget each Restart was given and fails the
// ones the test asks it to.
type fakeRestarter struct {
	budgets []time.Duration
	errs    []error
}

func (f *fakeRestarter) Restart(_ context.Context, healthTimeout time.Duration) error {
	f.budgets = append(f.budgets, healthTimeout)
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func stagedUpdatesDir(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, update.AddStaged(dir, update.Entry{Version: version, Path: dir + "/citeck"}))
	require.NoError(t, update.MarkState(dir, version, update.StatePending))
	return dir
}

func entryState(t *testing.T, dir, version string) update.State {
	t.Helper()
	m, err := update.Load(dir)
	require.NoError(t, err)
	for _, e := range m.Entries {
		if e.Version == version {
			return e.State
		}
	}
	t.Fatalf("no manifest entry for %s", version)
	return ""
}

// The swap gate judges a NEW, unproven binary and may fail fast.
func TestASuccessfulSwapIsMarkedGoodAndKeepsTheSwapBudget(t *testing.T) {
	dir := stagedUpdatesDir(t, "2.11.7")
	sup := &fakeRestarter{}

	reload := ApplyDaemonSwap(context.Background(), sup, dir, "2.11.7")

	assert.True(t, reload, "the new daemon answered; the webview must be pointed at it")
	assert.Equal(t, []time.Duration{UpdateHealthTimeout}, sup.budgets)
	assert.Equal(t, update.StateGood, entryState(t, dir, "2.11.7"))
}

// The rollback is the opposite question: the binary it restarts into is KNOWN
// good and may be an OLDER release that still binds its socket only at the END
// of its boot — the very defect this change fixes. Judging it by the swap's
// fail-fast budget is how a rollback "also fails" and leaves the user with no
// daemon at all.
func TestTheRollbackRestartGetsItsOwnMoreGenerousBudget(t *testing.T) {
	dir := stagedUpdatesDir(t, "2.11.7")
	sup := &fakeRestarter{errs: []error{errors.New("not ready")}}

	reload := ApplyDaemonSwap(context.Background(), sup, dir, "2.11.7")

	require.Len(t, sup.budgets, 2, "a failed gate must roll back")
	assert.Equal(t, UpdateHealthTimeout, sup.budgets[0], "the swap keeps the fail-fast budget")
	assert.Equal(t, RollbackHealthTimeout, sup.budgets[1])
	assert.Greater(t, RollbackHealthTimeout, UpdateHealthTimeout,
		"a known-good binary is worth waiting longer for than an unproven one")
	assert.Equal(t, update.StateFailed, entryState(t, dir, "2.11.7"))
	assert.True(t, reload, "the previous daemon is answering again")
}

// If even the rollback restart does not come up there is nothing listening on
// the socket: reloading the webview and refetching the title would only replace
// the dialog with a proxy error page.
func TestNothingIsReloadedWhenTheRollbackRestartAlsoFails(t *testing.T) {
	dir := stagedUpdatesDir(t, "2.11.7")
	sup := &fakeRestarter{errs: []error{errors.New("not ready"), errors.New("still not ready")}}

	reload := ApplyDaemonSwap(context.Background(), sup, dir, "2.11.7")

	assert.False(t, reload, "there is no daemon to reload against")
	assert.Equal(t, update.StateFailed, entryState(t, dir, "2.11.7"))
}

// The webview's proxy gate opens on a 30 s timer too, so a document request can
// land on a daemon that is still booting. Its 503 must keep the loading page,
// not render as text.
func TestTheBootRefusalIsRecognizedByTheWebviewProxy(t *testing.T) {
	starting := []byte(`{"error":"Service Unavailable","code":"DAEMON_STARTING","message":"the daemon is still starting; retry in a moment"}`)
	assert.True(t, IsDaemonStartingBody(http.StatusServiceUnavailable, starting))

	assert.False(t, IsDaemonStartingBody(http.StatusOK, starting), "only a 503 is a refusal")
	assert.False(t, IsDaemonStartingBody(http.StatusServiceUnavailable,
		[]byte(`{"error":"Service Unavailable","code":"LONG_OP_IN_PROGRESS","message":"a snapshot is running"}`)),
		"another 503 is the daemon answering for itself and must reach the UI")
	assert.False(t, IsDaemonStartingBody(http.StatusServiceUnavailable, []byte("<html>nginx</html>")))
}
