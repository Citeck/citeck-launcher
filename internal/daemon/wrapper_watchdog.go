package daemon

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// wrapperWatchdogInterval is how often the desktop daemon child polls whether
// its supervising wrapper is still alive.
const wrapperWatchdogInterval = 2 * time.Second

// watchWrapperLifecycle binds the daemon's lifecycle to the desktop wrapper that
// spawned it. The wrapper passes its PID via CITECK_WRAPPER_PID; if that process
// disappears (e.g. it was SIGKILLed, bypassing its graceful-quit path), the
// daemon detaches (containers left running — the kubelet principle) and exits,
// instead of lingering as an orphan until the next launch's reap.
//
// This is the cross-platform backstop. On Linux the kernel's Pdeathsig (set by
// the supervisor's SysProcAttr) is the primary, near-instant guard; on macOS and
// Windows there is no Pdeathsig equivalent, so this poll is the real mechanism.
// No-op when CITECK_WRAPPER_PID is unset (server / CLI mode).
func (d *Daemon) watchWrapperLifecycle() {
	raw := os.Getenv("CITECK_WRAPPER_PID")
	if raw == "" {
		return
	}
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		slog.Warn("Ignoring invalid CITECK_WRAPPER_PID")
		return
	}
	go func() {
		ticker := time.NewTicker(wrapperWatchdogInterval)
		defer ticker.Stop()
		for range ticker.C {
			if !processAlive(pid) {
				slog.Warn("Desktop wrapper process gone, detaching (containers left running)")
				d.shutdown(true) // idempotent via shutdownOnce; matches the SIGTERM path
				return
			}
		}
	}()
}
