package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// upgradeHarness redirects the install target into a temp dir, stubs the two
// daemon steps of the lifecycle, and answers every confirmation with the
// default. It returns the new-binary path and a pointer to "did the new daemon
// get started".
func upgradeHarness(t *testing.T, stopErr error) (newBin string, started *bool) {
	t.Helper()
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)

	dir := t.TempDir()
	target := filepath.Join(dir, "citeck")
	require.NoError(t, os.WriteFile(target, []byte("OLD BINARY"), 0o755)) //nolint:gosec // test fixture
	setInstallTarget(t, target)
	setSystemdUnitPath(t, filepath.Join(dir, "no-such-unit.service"))

	newBin = filepath.Join(dir, "citeck-new")
	require.NoError(t, os.WriteFile(newBin, []byte("NEW BINARY"), 0o755)) //nolint:gosec // test fixture

	oldYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = oldYes })

	oldStop, oldStart := stopDaemonFn, startDaemonFn
	t.Cleanup(func() { stopDaemonFn, startDaemonFn = oldStop, oldStart })
	stopDaemonFn = func(string) error { return stopErr }
	ran := false
	started = &ran
	startDaemonFn = func() error { ran = true; return nil }
	return newBin, started
}

func installedBinary(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(installTarget) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	return string(data)
}

// The upgrade window the retry cannot cover: after Detach() there is no runtime
// loop left, so the final state write is the last chance. When it is refused
// the containers keep running but the record of them is stale, and the very
// next thing the installer did was swap the binary on top of that.
//
// Aborting does NOT undo the loss — by this point the daemon is down and the
// containers are detached. Its value is that the operator is told before a
// version change is layered on top, and that restarting the SAME binary is the
// safe next step. Which is exactly why the swap must not have happened.
func TestUpgradeAbortsWhenTheOldDaemonCouldNotSaveItsState(t *testing.T) {
	newBin, started := upgradeHarness(t, &stateNotSavedError{detail: "disk quota exceeded"})

	err := lifecycleUpgrade(newBin, "2.10.0", "2.11.0")

	require.Error(t, err, "a lost state must not be a successful upgrade")
	assert.Contains(t, err.Error(), "disk quota exceeded")
	assert.Equal(t, "OLD BINARY", installedBinary(t),
		"the binary must NOT be swapped on top of a state that was already lost")
	assert.False(t, *started, "and the new version must not be started")
}

// The ordinary case is untouched: a detach that saved its state upgrades.
func TestUpgradeProceedsWhenTheStateWasSaved(t *testing.T) {
	newBin, started := upgradeHarness(t, nil)

	require.NoError(t, lifecycleUpgrade(newBin, "2.10.0", "2.11.0"))

	assert.Equal(t, "NEW BINARY", installedBinary(t))
	assert.True(t, *started)
}

// A stop that failed for any OTHER reason keeps its existing behavior: the
// swap is refused too, but the message is the daemon-stop one, not the
// state-loss one.
func TestUpgradeStillAbortsOnAnOrdinaryStopFailure(t *testing.T) {
	newBin, started := upgradeHarness(t, assert.AnError)

	err := lifecycleUpgrade(newBin, "2.10.0", "2.11.0")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop old daemon")
	assert.Equal(t, "OLD BINARY", installedBinary(t))
	assert.False(t, *started)
}

// Rollback is the escape hatch from a binary that does not work. Refusing it
// over a state write would take that hatch away exactly when it is needed, so
// it warns and continues — the asymmetry with the upgrade above is deliberate.
func TestRollbackWarnsAboutALostStateButStillRollsBack(t *testing.T) {
	_, started := upgradeHarness(t, &stateNotSavedError{detail: "disk quota exceeded"})
	// Stage a backup for runRollback to restore.
	require.NoError(t, os.WriteFile(installTarget+installBackupSuffix, []byte("PREVIOUS BINARY"), 0o755)) //nolint:gosec // test fixture

	require.NoError(t, runRollback())

	assert.Equal(t, "PREVIOUS BINARY", installedBinary(t),
		"a lost state must not block the way back off a broken binary")
	assert.True(t, *started)
}

// fakeDaemonSocket serves the two endpoints stopDaemonPreservePlatform talks to
// over a real Unix socket at the path the CLI's own client resolves, so the
// whole path — DetectTransport, the status probe, the detach POST, the
// stop wait and the verdict — is exercised, not just the classification helper.
// The daemon reports running:false once it has been asked to shut down.
type fakeDaemonSocket struct {
	stateSaveError string
	// stopDelay is how long after the detach POST the daemon keeps reporting
	// running:true — a real one answers the POST and only then winds down.
	stopDelay time.Duration
	stopped   atomic.Bool
	downAt    atomic.Int64 // unix nanos; 0 until the detach POST lands
}

func (f *fakeDaemonSocket) isDown() bool {
	at := f.downAt.Load()
	return at != 0 && time.Now().UnixNano() >= at
}

func (f *fakeDaemonSocket) serve(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "") // force the unix transport even on a box with one set

	ln, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc(api.DaemonStatus, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DaemonStatusDto{Running: !f.isDown()})
	})
	mux.HandleFunc(api.DaemonShutdown, func(w http.ResponseWriter, _ *http.Request) {
		f.stopped.Store(true)
		f.downAt.Store(time.Now().Add(f.stopDelay).UnixNano())
		_ = json.NewEncoder(w).Encode(api.ActionResultDto{
			Success: true, Message: "Detaching daemon", StateSaveError: f.stateSaveError,
		})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// The wiring between the two halves: the daemon reports the lost state on the
// detach response, and the installer's stop step has to turn that into the
// sentinel the upgrade refuses on. Without this the abort above is unreachable
// in production — every stub in the tests would still pass.
func TestTheStopStepTurnsAReportedStateLossIntoARefusal(t *testing.T) {
	f := &fakeDaemonSocket{stateSaveError: "disk quota exceeded"}
	f.serve(t)

	err := stopDaemonPreservePlatform("2.11.0")

	require.Error(t, err, "a detach that lost the state must not look like a clean stop")
	var notSaved *stateNotSavedError
	require.ErrorAs(t, err, &notSaved)
	assert.Equal(t, "disk quota exceeded", notSaved.detail)
	assert.True(t, f.stopped.Load(), "and the daemon must still have been asked to detach")
}

// A daemon that saved its state stops exactly as before — and the step still
// WAITS for it to be gone. Returning as soon as the POST is answered would let
// the installer swap the binary while the old daemon is still winding down.
func TestTheStopStepIsSilentWhenTheStateWasSaved(t *testing.T) {
	const stopDelay = 400 * time.Millisecond
	f := &fakeDaemonSocket{stopDelay: stopDelay}
	f.serve(t)

	start := time.Now()
	require.NoError(t, stopDaemonPreservePlatform("2.11.0"))

	assert.True(t, f.stopped.Load())
	assert.GreaterOrEqual(t, time.Since(start), stopDelay,
		"the step must not return before the old daemon is actually gone")
}
