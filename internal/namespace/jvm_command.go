package namespace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/jvmattach"
)

// Deliberate, operator-requested JVM diagnostics — the `citeck jcmd` /
// `jstack` / `jmap` family. The pre-restart capture in reconciler.go is the
// other caller of the same machinery; the difference is that this one is asked
// for, so it may take its time and it suspends the liveness probe first.
//
// SIGQUIT, the third mechanism the pre-restart capture falls back to, is NOT
// available here: it can produce exactly one thing (a thread dump) and only
// into the app's own stdout. A command the operator typed has to come back to
// the operator.

// ErrNotJVMApp and ErrAppNotRunning let the daemon answer plainly instead of
// reporting a docker exec failure the operator cannot act on.
var (
	ErrNotJVMApp     = errors.New("not a JVM app")
	ErrAppNotRunning = errors.New("app is not running")
	ErrAppUnknown    = errors.New("app not found in this namespace")
)

// heapDumpTimeFormat stamps a manual dump with when it was TAKEN. Same shape
// as the one RotateHeapDumps uses, so a manual dump and an OOM dump are the
// same kind of thing to whoever reads the directory — and to the rotation that
// keeps the directory bounded.
const heapDumpTimeFormat = "20060102T150405Z"

// JVMCommand runs one jcmd-style command against an app's JVM and returns its
// output. The app's liveness probe is suspended for the duration: several of
// these (GC.heap_dump above all) stop the world for longer than the probe
// tolerates, and restarting an app because it was busy answering us would be
// an own goal.
func (r *Runtime) JVMCommand(ctx context.Context, appName, command string, args ...string) (string, error) {
	containerID, _, err := r.jvmTargetContainer(appName)
	if err != nil {
		return "", err
	}
	var out string
	err = r.WithLivenessSuspended(appName, func() error {
		var cmdErr error
		out, cmdErr = r.attachJcmd(ctx, containerID, command, args...)
		return cmdErr
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// HeapDump writes a heap dump into the app's export directory and returns its
// file name and size (not the path — the caller reaches the file through the
// export API, which is the only route that also works when the daemon is on
// another host).
//
// Gzip is used wherever the JVM has it: an uncompressed dump is the size of the
// live heap (measured on a running webapp — 57 MB in 3.2 s at level 1). It is
// not available everywhere, though: `GC.heap_dump -gz=1` on Java 8 fails the
// whole command with "Unknown argument ... in diagnostic command" (measured on
// temurin-1.8.0_482), so a legacy image gets a plain .hprof rather than an
// error. The suffix follows the actual format, since that file goes on to be
// downloaded by name.
func (r *Runtime) HeapDump(ctx context.Context, appName string, now time.Time) (file string, size int64, err error) {
	_, jvmMajor, targetErr := r.jvmTargetContainer(appName)
	if targetErr != nil {
		return "", 0, targetErr
	}
	if r.volumesBase == "" {
		return "", 0, errors.New("no runtime files directory for this namespace")
	}
	// The container writes the file, so the directory has to be there and be
	// writable by the image's user — which is exactly what EnsureExportDir is
	// for. Normally the container start already did this.
	if dirErr := EnsureExportDir(r.volumesBase, appName); dirErr != nil {
		return "", 0, dirErr
	}

	stamp := now.UTC().Format(heapDumpTimeFormat)
	args := []string{"-gz=1"}
	file = fmt.Sprintf("%s-%s.hprof.gz", appName, stamp)
	if !SupportsGzipHeapDump(jvmMajor) {
		args = nil
		file = fmt.Sprintf("%s-%s.hprof", appName, stamp)
	}
	out, cmdErr := r.JVMCommand(ctx, appName, "GC.heap_dump", append(args, ExportMountPath+"/"+file)...)
	if cmdErr != nil {
		return "", 0, cmdErr
	}
	// HotSpot reports success in prose ("Heap dump file created [… bytes …]"),
	// and refuses to overwrite an existing file with an error it also delivers
	// as ordinary output. So the file on disk is the verdict, not the text.
	path := filepath.Join(ExportDirFor(r.volumesBase, appName), file)
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", 0, fmt.Errorf("heap dump did not appear at %s: %s", path, truncateForLog(out))
	}
	slog.Info("Heap dump written", "app", appName, "file", file, "bytes", info.Size())
	return file, info.Size(), nil
}

// jvmTargetContainer resolves the container to attach to, refusing plainly
// when the app is not a JVM or is not running.
func (r *Runtime) jvmTargetContainer(appName string) (containerID string, jvmMajor int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	app, ok := r.apps[appName]
	if !ok {
		// The namespace may simply not be started. Answering from the generated
		// definitions keeps the distinction the operator cares about: "there is
		// no such app" versus "it is not running right now".
		if def, known := r.generatedDefForApp(appName); known {
			if !def.IsJVM {
				return "", 0, fmt.Errorf("%s: %w", appName, ErrNotJVMApp)
			}
			return "", 0, fmt.Errorf("%s: %w", appName, ErrAppNotRunning)
		}
		return "", 0, fmt.Errorf("%s: %w", appName, ErrAppUnknown)
	}
	if !app.Def.IsJVM {
		// Kind is the wrong test — the proxy is KindCiteckCore running nginx,
		// while alfresco and solr are KindCiteckAdditional JVMs.
		return "", 0, fmt.Errorf("%s: %w", appName, ErrNotJVMApp)
	}
	// The gate is the CONTAINER, not the RUNNING status. An app whose startup
	// probe is still failing sits in STARTING, and a wedged one sits in
	// STALLED/FAILED — those are precisely the moments an operator wants a
	// thread dump, and refusing them would leave the tool useful only when
	// nothing is wrong. Measured on the stand: `integrations` OOMed on
	// metaspace, stayed up as a process, and the launcher reported STARTING for
	// two hours — with a status gate, the one app worth attaching to was the one
	// app that could not be attached to.
	if app.ContainerID == "" || app.Status == AppStatusStopped || app.Status == AppStatusStopping {
		return "", 0, fmt.Errorf("%s: %w (status %s)", appName, ErrAppNotRunning, app.Status)
	}
	return app.ContainerID, app.Def.JVMMajor, nil
}

// attachJcmd speaks the `jcmd` attach verb, from the host if this host can see
// the JVM and from inside the container otherwise.
//
// The whole command line goes into ONE argument slot. Measured against a
// running webapp: `jcmd` with ("VM.flags", "-all") in separate slots runs a
// bare VM.flags and drops the option, while "VM.flags -all" in a single slot
// does what was asked.
func (r *Runtime) attachJcmd(ctx context.Context, containerID, command string, args ...string) (string, error) {
	line := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if jvmattach.Supported {
		out, err := r.attachHostCommand(ctx, containerID, "jcmd", line)
		if err == nil {
			return out, nil
		}
		slog.Debug("Host attach command failed, trying the in-container client",
			"container", containerID, "cmd", line, "err", err)
	}
	return r.runAttachInContainer(ctx, containerID, "jcmd", line)
}

// attachHostCommand runs one attach command against the JVM behind
// containerID, from this host's /proc.
func (r *Runtime) attachHostCommand(ctx context.Context, containerID, verb string, args ...string) (string, error) {
	info, err := r.docker.InspectContainer(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("inspect container: %w", err)
	}
	if info.State == nil || info.State.Pid <= 0 {
		return "", errors.New("container has no host pid")
	}
	att := jvmattach.New()
	pid, err := att.FindJVM(info.State.Pid, diagJVMSearchDepth)
	if err != nil {
		return "", fmt.Errorf("find jvm under pid %d: %w", info.State.Pid, err)
	}
	out, err := att.Command(ctx, pid, verb, args...)
	if err != nil {
		return "", fmt.Errorf("attach to jvm pid %d: %w", pid, err)
	}
	return out, nil
}
