package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func TestDaemonHoldsRecoveryLockThroughSlowBootAndReleasesOnFailure(t *testing.T) {
	h := newBootHarness(t)
	_, err := fsutil.AcquireProfileLock(config.HomeDir())
	require.ErrorIs(t, err, fsutil.ErrProfileBusy)
	require.Error(t, h.abortBoot(t, errors.New("test boot failed")))
	lock, err := fsutil.AcquireProfileLock(config.HomeDir())
	require.NoError(t, err)
	require.NoError(t, lock.Close())
}

func TestDaemonWaitsForThePreviousProcessesShutdownTail(t *testing.T) {
	home := t.TempDir()
	lock, err := fsutil.AcquireProfileLock(home)
	require.NoError(t, err)
	defer lock.Close()
	release := time.AfterFunc(30*time.Millisecond, func() { _ = lock.Close() })
	defer release.Stop()
	next, err := acquireDaemonProfileLock(home, home+"/no-socket", time.Second)
	require.NoError(t, err)
	require.NoError(t, next.Close())
}

func TestDaemonDoesNotConstructWhileOfflineRecoveryOwnsProfile(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	t.Setenv("CITECK_RUN", daemonSocketDir(t))
	lock, err := fsutil.AcquireProfileLock(config.HomeDir())
	require.NoError(t, err)
	defer lock.Close()
	previous := constructDaemonFn
	defer func() { constructDaemonFn = previous }()
	constructDaemonFn = func(StartOptions, config.DaemonConfig, string) (*Daemon, error) {
		t.Fatal("must refuse before constructing daemon")
		return nil, nil
	}
	require.ErrorIs(t, Start(StartOptions{Foreground: true, NoUI: true, Version: "test"}), fsutil.ErrProfileBusy)
}
