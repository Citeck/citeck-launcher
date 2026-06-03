package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// Restart-loop tuning. Mirrors RunDaemonLoop (internal/desktop/daemon.go) so the
// out-of-process supervisor backs off identically to the historical in-process
// loop: 5s→60s exponential backoff, reset once the child survives >30s, give up
// after 20 consecutive fast crashes.
const (
	supervisorInitialBackoff = 5 * time.Second
	supervisorMaxBackoff     = 60 * time.Second
	supervisorMaxFailures    = 20
	supervisorHealthyUptime  = 30 * time.Second

	readyPollInterval = 200 * time.Millisecond

	// daemonDialTimeout caps the unix-socket dial+round-trip for the default
	// production ReadyCheck and for the graceful Stop shutdown POST. Short,
	// because a hung daemon must not block the wrapper.
	daemonDialTimeout = 2 * time.Second
)

// Supervisor spawns the daemon as a separate child process (`<bin> start
// --_daemon --desktop [Args...]`) and supervises it: readiness polling, crash
// restart with backoff, and graceful shutdown. It is pure Go (no Wails imports)
// so it can be unit-tested on the host; GUI wiring lives in the desktop runner.
//
// The webview→daemon path is unchanged — the daemon still listens on the unix
// socket at config.SocketPath().
type Supervisor struct {
	// BinaryPath is the daemon binary to exec. Required.
	BinaryPath string
	// Args are appended after the fixed ["start","--_daemon","--desktop"] verbs.
	Args []string
	// ExtraEnv is appended to os.Environ() for the child.
	ExtraEnv []string
	// Stdin is written verbatim to the child's stdin. The child
	// (runDaemonMode) reads ONE line as the master password, so the caller
	// passes the password LINE including its trailing newline. For desktop the
	// caller passes "\n" (empty password) — the desktop daemon.Start ignores
	// opts.MasterPassword entirely (default-password auto-unlocks; custom
	// password defers to the Web UI; unencrypted stays unencrypted), so feeding
	// an empty line preserves behavior for both default- and custom-password
	// desktop users. See the supervisor commit note / daemon/server.go.
	Stdin string
	// ReadyCheck reports whether the daemon is accepting requests. If nil, a
	// production check polling GET api.Health over config.SocketPath() is used.
	ReadyCheck func() bool
	// LogWriter receives the child's stdout+stderr. If nil, os.Stderr is used.
	LogWriter io.Writer
	// OnExit, if non-nil, is called with the child's exit error each time the
	// child exits unexpectedly (i.e. not during a clean Stop/ctx-cancel). The
	// desktop runner wires this to DaemonStatus.SetError so the loading/error
	// splash still surfaces the daemon's last error (parity with the historical
	// in-process RunDaemonLoop, which set Status.SetError on unexpected exit).
	// Optional and nil-safe — tests leave it unset.
	OnExit func(err error)

	mu       sync.Mutex
	cmd      *exec.Cmd
	pid      int
	done     chan struct{} // closed when the current child exits
	ready    atomic.Bool
	stopping atomic.Bool

	// stopCh is closed by Stop (guarded by stopOnce) to wake a superviseLoop
	// sleeping in the restart backoff so it returns immediately instead of
	// after up to supervisorMaxBackoff. Initialized at the top of Start before
	// any goroutine is launched, so the -race-visible write happens-before the
	// goroutine reads (Start is the single setup site; there is no constructor).
	stopCh   chan struct{}
	stopOnce sync.Once
}

// Start spawns the daemon child and launches the ready-poll and supervise
// goroutines. It returns once the first child has been started (not once it is
// ready); call Ready to observe readiness. The child is restarted on crash
// until ctx is canceled, Stop is called, or the failure budget is exhausted.
func (s *Supervisor) Start(ctx context.Context) error {
	if s.BinaryPath == "" {
		return errors.New("supervisor: BinaryPath is required")
	}
	if s.ReadyCheck == nil {
		s.ReadyCheck = defaultReadyCheck
	}
	// Initialize stopCh before launching goroutines so superviseLoop's read of
	// it is safely ordered after this write (no constructor exists; Start is the
	// sole setup site).
	s.stopCh = make(chan struct{})

	if err := s.spawn(ctx); err != nil {
		return err
	}

	go s.readyLoop(ctx)
	go s.superviseLoop(ctx)
	return nil
}

// spawn builds and starts one child process, recording its handle/pid and a
// fresh done channel under the mutex. It also (best-effort) persists the child
// pid so a restarted wrapper can reap an orphan via ReapOrphanDaemon.
func (s *Supervisor) spawn(ctx context.Context) error {
	args := append([]string{"start", "--_daemon", "--desktop"}, s.Args...)
	cmd := exec.CommandContext(ctx, s.BinaryPath, args...) //nolint:gosec // G204: BinaryPath is our own daemon binary
	cmd.Stdin = strings.NewReader(s.Stdin)
	cmd.Env = append(os.Environ(), s.ExtraEnv...)
	if s.LogWriter != nil {
		cmd.Stdout = s.LogWriter
		cmd.Stderr = s.LogWriter
	} else {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("supervisor: start daemon child: %w", err)
	}

	pid := cmd.Process.Pid
	done := make(chan struct{})

	s.mu.Lock()
	s.cmd = cmd
	s.pid = pid
	s.done = done
	s.mu.Unlock()

	s.ready.Store(false)
	writeDaemonPid(pid)
	slog.Info("Daemon child started", "pid", pid)
	return nil
}

// readyLoop polls ReadyCheck every readyPollInterval until ctx is done, setting
// the ready flag whenever the check passes. It keeps running for the whole ctx
// lifetime so readiness is re-observed after a respawn (spawn resets ready to
// false on each restart).
func (s *Supervisor) readyLoop(ctx context.Context) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()
	for {
		if s.ReadyCheck() {
			s.ready.Store(true)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// superviseLoop waits for the current child to exit and restarts it with
// RunDaemonLoop-style backoff, unless ctx is canceled or Stop was requested.
func (s *Supervisor) superviseLoop(ctx context.Context) {
	backoff := supervisorInitialBackoff
	failures := 0

	for {
		s.mu.Lock()
		cmd := s.cmd
		done := s.done
		s.mu.Unlock()

		startedAt := time.Now()
		err := cmd.Wait()
		close(done) // unblock Wait()

		if ctx.Err() != nil || s.stopping.Load() {
			return // clean shutdown — do not restart
		}

		if time.Since(startedAt) > supervisorHealthyUptime {
			backoff = supervisorInitialBackoff
			failures = 0
		} else {
			failures++
		}
		slog.Error("Daemon child exited unexpectedly", "err", err, "retry", backoff, "failures", failures)
		if s.OnExit != nil {
			s.OnExit(err)
		}

		if failures >= supervisorMaxFailures {
			slog.Error("Daemon child failed too many times; giving up restarts", "failures", failures)
			<-ctx.Done()
			return
		}

		select {
		case <-time.After(backoff):
			backoff = min(backoff*2, supervisorMaxBackoff)
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return // Stop requested during backoff — do not respawn
		}

		if ctx.Err() != nil || s.stopping.Load() {
			return
		}
		if err := s.spawn(ctx); err != nil {
			slog.Error("Daemon child respawn failed", "err", err)
			failures++
		}
	}
}

// Ready reports whether the daemon child has passed its readiness check.
func (s *Supervisor) Ready() bool {
	return s.ready.Load()
}

// Pid returns the current child process pid, or 0 if none is running.
func (s *Supervisor) Pid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pid
}

// Wait blocks up to timeout for the current child process to exit. On timeout
// it kills the child. It returns once the child has exited (or been killed).
func (s *Supervisor) Wait(timeout time.Duration) {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()
	if cmd == nil || done == nil {
		return
	}

	select {
	case <-done:
		return
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done // superviseLoop closes done once the child's cmd.Wait() returns
	}
}

// Stop gracefully shuts the daemon down: it records stop intent so the
// supervise goroutine does not restart, POSTs api.DaemonShutdown over the unix
// socket, then waits a short grace period and kills the child if still alive.
// Best-effort — returns nil even if the shutdown POST fails, as long as the
// process exits.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.stopping.Store(true)
	// Wake a superviseLoop sleeping in the restart backoff. stopOnce makes this
	// safe under repeated Stop calls; the nil guard tolerates Stop-before-Start.
	if s.stopCh != nil {
		s.stopOnce.Do(func() { close(s.stopCh) })
	}

	if err := postDaemonShutdown(ctx); err != nil {
		slog.Warn("Daemon shutdown POST failed; will kill child", "err", err)
	}
	s.Wait(daemonStopGrace)
	return nil
}

// daemonStopGrace is how long Stop waits for a graceful exit after the shutdown
// POST before force-killing the child.
const daemonStopGrace = 5 * time.Second

// --- default production ReadyCheck / shutdown POST over the unix socket ---

// unixSocketClient builds an HTTP client that dials the daemon's unix socket.
// Mirrors the focus.go pattern.
func unixSocketClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: daemonDialTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// defaultReadyCheck reports ready when GET api.Health over the daemon socket
// returns a 2xx status.
func defaultReadyCheck() bool {
	ctx, cancel := context.WithTimeout(context.Background(), daemonDialTimeout)
	defer cancel()
	client := unixSocketClient(config.SocketPath())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon"+api.Health, http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// postDaemonShutdown POSTs api.DaemonShutdown over the daemon socket with
// leave_running=true. The desktop "kubelet principle" is that closing the
// launcher DETACHES — the platform containers are left running and re-adopted on
// the next launch — rather than tearing the namespace down. This matches the
// legacy in-process behavior (Wails ctx cancel → d.shutdown(true)); explicit
// teardown stays available via the UI Stop button.
func postDaemonShutdown(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, daemonDialTimeout)
	defer cancel()
	client := unixSocketClient(config.SocketPath())
	req, err := http.NewRequestWithContext(dialCtx, http.MethodPost, "http://daemon"+api.DaemonShutdown+"?leave_running=true", http.NoBody)
	if err != nil {
		return fmt.Errorf("build shutdown request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post daemon shutdown: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("daemon shutdown returned %d", resp.StatusCode)
	}
	return nil
}

// --- pid-file persistence + orphan reaping ---

// writeDaemonPid records the child pid to config.DaemonPidPath() (best-effort).
func writeDaemonPid(pid int) {
	if err := os.MkdirAll(config.RunDir(), 0o755); err != nil { //nolint:gosec // run dir needs 0o755
		slog.Warn("Failed to create run dir for daemon pid file", "err", err)
		return
	}
	if err := os.WriteFile(config.DaemonPidPath(), []byte(strconv.Itoa(pid)), 0o644); err != nil { //nolint:gosec // G306: pid file is not sensitive
		slog.Warn("Failed to write daemon pid file", "err", err)
	}
}

// ReapOrphanDaemon kills a daemon child orphaned by a previous wrapper crash.
// It is a package-level function (not a Supervisor method) because the desktop
// runner calls it during instance-lock acquisition, before any Supervisor
// exists.
//
// It reads config.DaemonPidPath(); if absent it is a no-op. If the recorded pid
// is alive it sends SIGTERM, waits briefly, then SIGKILL if still alive, and
// removes the pid file.
//
// pid-reuse caveat: on a long-running host the recorded pid could have been
// recycled by an unrelated process. On Linux we narrow this by reading
// /proc/<pid>/cmdline and only killing if it contains "--_daemon"; on other
// platforms (and if /proc is unreadable) we cannot verify and kill optimistically.
func ReapOrphanDaemon() error {
	pidPath := config.DaemonPidPath()
	data, err := os.ReadFile(pidPath) //nolint:gosec // G304: path from trusted config
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read daemon pid file: %w", err)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil || pid <= 0 {
		_ = os.Remove(pidPath)
		return nil //nolint:nilerr // malformed pid file: clean it up, nothing to reap
	}

	defer func() { _ = os.Remove(pidPath) }()

	if !isProcessAlive(pid) {
		return nil
	}
	if !looksLikeOurDaemon(pid) {
		slog.Warn("Orphan pid is alive but does not look like our daemon; not killing", "pid", pid)
		return nil
	}

	signalTerminate(pid)
	if waitForExit(pid, daemonStopGrace) {
		return nil
	}
	signalKill(pid)
	return nil
}

// looksLikeOurDaemon best-effort verifies the pid is our daemon child to reduce
// pid-reuse risk. On Linux it inspects /proc/<pid>/cmdline for "--_daemon"; on
// other platforms (no /proc) it returns true (cannot verify).
func looksLikeOurDaemon(pid int) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)) //nolint:gosec // G304: reading our own daemon's cmdline
	if err != nil {
		return true // cannot verify — fall back to optimistic kill
	}
	// cmdline args are NUL-separated.
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.Contains(cmdline, "--_daemon")
}

// waitForExit polls until the process exits or the timeout elapses. Returns
// true if the process exited within the timeout.
func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !isProcessAlive(pid)
}
