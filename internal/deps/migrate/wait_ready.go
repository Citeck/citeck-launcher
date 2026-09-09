package migrate

import (
	"context"
	"fmt"
	"time"
)

// The readiness discipline the copy-upgrade migrators share.
//
// It lives in its own file rather than in either dependency's, because both
// need exactly the same loop around a different question, and "the container
// is running" is not the answer for any of these: a RabbitMQ node answers
// `ping` and opens 5672 long before it has booted, and ZooKeeper's admin
// server lags 10-30 s behind its client port on a cold start. Two copies of a
// poll loop is also two places for the context handling and the deadline to
// drift apart — and the shape a wrong one produces (a step running against a
// server that is not there yet) is a flaky migration, not a failing test.
const (
	// copyReadyTimeout bounds waiting for a temp server on the copy. Measured:
	// a 4.2 RabbitMQ node on a copied volume boots in under two minutes, a
	// ZooKeeper in one to two seconds. The margin is for a cold host that is
	// also unpacking an image layer.
	copyReadyTimeout = 5 * time.Minute
	copyReadyPoll    = 2 * time.Second
)

// waitForReady polls answers until it says yes, the container dies, the
// deadline passes or the context is canceled.
//
// what names the thing being waited for in the progress line and in the
// timeout error ("RabbitMQ", "ZooKeeper"), because the operator reads them
// without knowing which plan is running.
func waitForReady(
	ctx context.Context, env Env, container, what string,
	p StepProgress, answers func(context.Context) bool,
) error {
	deadline := time.Now().Add(copyReadyTimeout)
	for {
		running, err := env.ContainerRunning(ctx, container)
		if err != nil {
			return fmt.Errorf("check %s: %w", container, err)
		}
		if !running {
			return fmt.Errorf("container %s is not running", container)
		}
		if answers(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s in %s did not become ready within %s", what, container, copyReadyTimeout)
		}
		p(0, "waiting for "+what+" in "+container)
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", container, ctx.Err())
		case <-time.After(copyReadyPoll):
		}
	}
}

// execOK reports whether a command ran AND exited 0. It is the shape every
// readiness probe here needs: a probe that cannot tell "the command failed"
// from "the command could not be run" would report a broker as ready the
// moment Docker stopped answering.
func execOK(ctx context.Context, env Env, container string, cmd []string) bool {
	_, _, code, err := env.Exec(ctx, container, cmd)
	return err == nil && code == 0
}
