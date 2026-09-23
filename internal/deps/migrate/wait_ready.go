package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/citeck/citeck-launcher/internal/msg"
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
	err := pollUntil(ctx, env, container, copyReadyTimeout, copyReadyPoll,
		func() msg.Message { return progressWaiting(what, container) },
		func(ctx context.Context) (bool, error) { return answers(ctx), nil },
		p)
	if errors.Is(err, errPollDeadline) {
		return fmt.Errorf("%s in %s did not become ready within %s", what, container, copyReadyTimeout)
	}
	return err
}

// errPollDeadline is what pollUntil answers when its deadline passes. Each
// caller words its own timeout, because only the caller knows what it was
// waiting FOR.
var errPollDeadline = errors.New("deadline passed")

// pollUntil is the one poll loop the copy-upgrade waits share: it asks check
// until it answers done, answers an error, the container dies, the deadline
// passes or the context is canceled.
//
// check's error ends the wait at once — that is how a wait tells "not there
// yet" from "it will never get there" (a Qdrant collection whose optimizer
// reports an error is not going to settle by being asked again). waiting is
// the progress line reported between two polls; it is a function so it can
// name what the last check found.
func pollUntil(
	ctx context.Context, env Env, container string, timeout, every time.Duration,
	waiting func() msg.Message,
	check func(context.Context) (done bool, err error),
	p StepProgress,
) error {
	deadline := time.Now().Add(timeout)
	for {
		running, err := env.ContainerRunning(ctx, container)
		if err != nil {
			return fmt.Errorf("check %s: %w", container, err)
		}
		if !running {
			return fmt.Errorf("container %s is not running", container)
		}
		done, err := check(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return errPollDeadline
		}
		p(0, waiting())
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", container, ctx.Err())
		case <-time.After(every):
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
