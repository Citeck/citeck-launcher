// Package jvmattach speaks the HotSpot dynamic attach protocol, so the launcher
// can pull a thread dump (or any other jcmd verb) out of a JVM it supervises
// without a JDK anywhere — not on the host, not in the container image.
//
// Why it exists: the Citeck runtime images ship a JRE, so `jcmd` is absent —
// the pre-restart capture recorded `jcmd failed: exit=127` at exactly the moment
// a thread dump mattered — and their PID 1 is `sh -c /entrypoint.sh`, not java,
// so even a JDK image would have needed the real pid first. The protocol itself
// is small and has been stable since JDK 6: a trigger file, SIGQUIT, then a
// NUL-separated text exchange over a unix socket the JVM creates.
//
// The alternative was to vendor a third-party attach binary (jattach) into the
// build. This is ~200 lines of stdlib Go instead, with no binary blob to pin,
// verify and re-license.
//
// Availability: Linux hosts only, and only when the daemon's uid matches the
// JVM's (or is root) — HotSpot enforces that on both the trigger file's owner
// and the socket's peer credentials. That covers server mode (rootful Docker,
// daemon as root) and desktop on Linux (rootless Docker: container root IS the
// user). On macOS / Windows the containers live in a VM whose /proc the host
// cannot see, so Supported is false and callers fall back to signaling the JVM
// from inside the container.
package jvmattach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Attach protocol constants (hotspot/os/posix/attachListener_posix.cpp).
const (
	// protocolVersion is the only version HotSpot has ever spoken.
	protocolVersion = "1"
	// attachArgCount is fixed: the JVM reads exactly three argument slots,
	// padding with empty strings, and stops reading after them.
	attachArgCount = 3
)

var (
	// ErrUnsupported is returned on platforms where the host cannot see the
	// target's /proc (everything but Linux).
	ErrUnsupported = errors.New("jvm attach is only supported on linux hosts")
	// ErrNotAJVM guards the SIGQUIT below: that signal starts the attach
	// listener in a JVM, but TERMINATES an ordinary process. Never signal
	// anything that has not been positively identified as a JVM.
	ErrNotAJVM = errors.New("target process is not a jvm")
	// ErrNoJVM is returned by FindJVM when no JVM exists in the subtree.
	ErrNoJVM = errors.New("no jvm found in process tree")
)

// Attacher performs dynamic attach against a JVM visible in this host's /proc,
// including one running inside a container: /proc/<pid>/root reaches the
// container's filesystem, and NSpid gives the pid the JVM knows itself by.
//
// The zero value is not usable — call New.
type Attacher struct {
	// ProcRoot is the procfs mount point. Overridden in tests.
	ProcRoot string
	// SocketWait bounds the wait for the attach listener to come up after the
	// trigger. A JVM in a long GC pause answers late or not at all, which is
	// precisely the state usually being captured, so this must be finite.
	SocketWait time.Duration
	// PollInterval is how often the listener socket is polled into existence.
	PollInterval time.Duration

	// signal is a seam: tests exercise the trigger/poll dance without a real
	// process to kill.
	signal func(pid int) error
}

// New returns an Attacher with production defaults.
func New() *Attacher {
	return &Attacher{
		ProcRoot:     "/proc",
		SocketWait:   5 * time.Second,
		PollInterval: 100 * time.Millisecond,
		signal:       sendQuit,
	}
}

// ThreadDump returns a full thread dump — the same output as `jcmd <pid>
// Thread.print`, and the reason this package exists.
func (a *Attacher) ThreadDump(ctx context.Context, pid int) (string, error) {
	return a.Command(ctx, pid, "threaddump")
}

// Command runs one attach command (HotSpot's verb names: threaddump,
// properties, jcmd, dumpheap, ...) against the JVM at host pid and returns its
// output.
//
// A non-zero completion code from the JVM is returned as an error carrying the
// JVM's own message rather than as output: a caller writing the result into a
// diagnostics file must not mistake "Unknown command" for a dump.
func (a *Attacher) Command(ctx context.Context, pid int, cmd string, args ...string) (string, error) {
	if !Supported {
		return "", ErrUnsupported
	}
	if len(args) > attachArgCount {
		return "", fmt.Errorf("attach takes at most %d arguments, got %d", attachArgCount, len(args))
	}
	// A NUL inside an argument would silently shift every following field —
	// the protocol has no escaping.
	for _, s := range append([]string{cmd}, args...) {
		if strings.ContainsRune(s, 0) {
			return "", errors.New("attach command and arguments must not contain NUL")
		}
	}

	nsPID, err := a.NSPid(pid)
	if err != nil {
		return "", err
	}
	if isJVM, err := a.IsJVM(pid); err != nil {
		return "", err
	} else if !isJVM {
		return "", fmt.Errorf("pid %d: %w", pid, ErrNotAJVM)
	}

	sock := a.socketPath(pid, nsPID)
	if _, statErr := os.Stat(sock); statErr != nil {
		if err := a.startListener(ctx, pid, nsPID, sock); err != nil {
			return "", err
		}
	}
	return a.exchange(ctx, sock, cmd, args)
}

// socketPath is where the JVM's attach listener lives. Reached through
// /proc/<pid>/root so a containerized JVM needs no bind mount, and named with
// the NAMESPACE pid because that is the pid the JVM knows itself by.
func (a *Attacher) socketPath(pid, nsPID int) string {
	return filepath.Join(a.procRoot(), strconv.Itoa(pid), "root", "tmp", ".java_pid"+strconv.Itoa(nsPID))
}

// triggerPath is the file whose existence tells a JVM handling SIGQUIT to start
// its attach listener instead of printing a thread dump to stdout. HotSpot looks
// for it in its cwd and in /tmp; /tmp is the reliable one (mode 1777 in every
// image, whereas a cwd may be read-only).
func (a *Attacher) triggerPath(pid, nsPID int) string {
	return filepath.Join(a.procRoot(), strconv.Itoa(pid), "root", "tmp", ".attach_pid"+strconv.Itoa(nsPID))
}

func (a *Attacher) procRoot() string {
	if a.ProcRoot == "" {
		return "/proc"
	}
	return a.ProcRoot
}

// startListener creates the trigger file, sends SIGQUIT and waits for the
// listener socket to appear.
//
// The trigger file is removed on the way out even on failure: left behind, it
// turns the next ordinary SIGQUIT (an operator asking for a thread dump on
// stdout) into a silent attach-listener start.
func (a *Attacher) startListener(ctx context.Context, pid, nsPID int, sock string) error {
	trigger := a.triggerPath(pid, nsPID)
	// #nosec G304 -- path is /proc/<int>/root/tmp/.attach_pid<int>
	f, err := os.OpenFile(trigger, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	switch {
	case err == nil:
		_ = f.Close()
		defer func() { _ = os.Remove(trigger) }()
	case errors.Is(err, os.ErrExist):
		// Someone else is mid-attach (or a previous attempt died). Don't
		// remove a trigger this call did not create — the other attach may
		// still be waiting on it.
	default:
		return fmt.Errorf("create attach trigger %s: %w", trigger, err)
	}

	if err := a.signal(pid); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(a.SocketWait)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for {
		if _, err := os.Stat(sock); err == nil {
			return nil
		}
		if !time.Now().Add(a.PollInterval).Before(deadline) {
			return fmt.Errorf("attach listener did not start within %s (pid %d)", a.SocketWait, pid)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for attach listener: %w", ctx.Err())
		case <-time.After(a.PollInterval):
		}
	}
}

// exchange performs the request/response over the listener socket.
//
// Wire format: version NUL command NUL arg0 NUL arg1 NUL arg2 NUL. The reply is
// a decimal completion code on its own line followed by the command's output.
func (a *Attacher) exchange(ctx context.Context, sock, cmd string, args []string) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return "", fmt.Errorf("connect to attach listener %s: %w", sock, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		// Best effort: a missed deadline only costs us the ctx-less blocking
		// read below, which the caller's own timeout still bounds.
		_ = conn.SetDeadline(deadline)
	}

	var req strings.Builder
	req.WriteString(protocolVersion)
	req.WriteByte(0)
	req.WriteString(cmd)
	req.WriteByte(0)
	for i := range attachArgCount {
		if i < len(args) {
			req.WriteString(args[i])
		}
		req.WriteByte(0)
	}
	if _, writeErr := io.WriteString(conn, req.String()); writeErr != nil {
		return "", fmt.Errorf("write attach request: %w", writeErr)
	}

	raw, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("read attach response: %w", err)
	}
	return parseResponse(string(raw))
}

// parseResponse splits the completion code from the payload.
func parseResponse(raw string) (string, error) {
	code, body, found := strings.Cut(raw, "\n")
	if !found {
		return "", fmt.Errorf("malformed attach response: %q", truncate(raw, 120))
	}
	rc, err := strconv.Atoi(strings.TrimSpace(code))
	if err != nil {
		return "", fmt.Errorf("malformed attach completion code %q", truncate(code, 40))
	}
	if rc != 0 {
		return "", fmt.Errorf("attach command failed (code %d): %s", rc, truncate(strings.TrimSpace(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
