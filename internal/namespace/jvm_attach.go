package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/appfiles"
)

// Delivery of the embedded HotSpot attach client into a running container.
//
// This is the MIDDLE of the three ways the launcher can reach a containerized
// JVM, and the only one that works everywhere while still returning the answer
// to the caller rather than to the app's log:
//
//	host attach (internal/jvmattach)  — nothing enters the container, but it
//	                                    needs the JVM in THIS host's /proc:
//	                                    Linux only, and not through a VM or a
//	                                    remote DOCKER_HOST.
//	embedded class (here)             — architecture-independent, run by the
//	                                    image's own java, needs no JDK in the
//	                                    image.
//	SIGQUIT                           — always available, but the JVM answers
//	                                    into its own stdout, so the dump has to
//	                                    be fished back out of the container log.
const (
	// attachClassDir is where the class is delivered. /tmp because it is the
	// one directory every image has and every user can write, and because the
	// attach socket the client talks to lives there anyway.
	attachClassDir = "/tmp"

	// attachThreadDumpCmd is HotSpot's own verb name — the attach protocol
	// does not take jcmd spellings like "Thread.print" (it answers "Operation
	// Thread.print not recognized!"; measured against a running webapp). The
	// host-side path uses the same verb, so the two mechanisms stay in step.
	attachThreadDumpCmd = "threaddump"

	// attachCleanupTimeout bounds the removal of the class. It runs on a
	// context detached from the caller's, so that a diagnostic which timed out
	// still tidies up — but detached is not unbounded.
	attachCleanupTimeout = 10 * time.Second
)

// attachClassRemotePath is the class's full path inside the container.
func attachClassRemotePath() string {
	return attachClassDir + "/" + appfiles.AttachClassFileName
}

// runAttachInContainer delivers the embedded attach client, runs one attach
// command with the image's own java, and removes the class again.
//
// The class is copied in non-executable and is deleted whether or not the
// command succeeded: the launcher must not leave anything behind in a running
// production container.
func (r *Runtime) runAttachInContainer(ctx context.Context, containerID, cmd string, args ...string) (string, error) {
	class, err := appfiles.AttachClass()
	if err != nil {
		return "", fmt.Errorf("embedded attach client: %w", err)
	}
	if copyErr := r.docker.CopyFileToContainer(ctx, containerID, attachClassDir, appfiles.AttachClassFileName, class); copyErr != nil {
		// Nothing was delivered, so there is nothing to clean up — and an
		// unconditional `rm` here would spend an exec saying so.
		return "", fmt.Errorf("deliver attach client: %w", copyErr)
	}
	defer r.removeAttachClass(ctx, containerID)

	// pid 0 tells the client to find the JVM itself: it is not pid 1 (the
	// images run `sh -c /entrypoint.sh`), and it is not this process either.
	argv := append([]string{"java", "-cp", attachClassDir, appfiles.AttachClassName, "0", cmd}, args...)
	out, code, err := r.docker.ExecInContainer(ctx, containerID, argv)
	if err != nil {
		return "", fmt.Errorf("run attach client: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("attach client exit=%d: %s", code, truncateForLog(out))
	}
	if strings.TrimSpace(out) == "" {
		// Exit 0 with no output is not a result. Returning it as one would
		// hand an empty thread dump to a caller that had a working fallback.
		return "", fmt.Errorf("attach client returned no output for %q", cmd)
	}
	return out, nil
}

func (r *Runtime) removeAttachClass(ctx context.Context, containerID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attachCleanupTimeout)
	defer cancel()
	if _, code, err := r.docker.ExecInContainer(cleanupCtx, containerID, []string{"rm", "-f", attachClassRemotePath()}); err != nil || code != 0 {
		slog.Warn("Failed to remove the attach client from the container",
			"container", containerID, "path", attachClassRemotePath(), "exit", code, "err", err)
	}
}
