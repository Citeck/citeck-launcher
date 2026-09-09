package migrate

import (
	"context"
	"fmt"
)

// The steps both migration plans share, in one place.
//
// They were identical in both files, which is worth removing for the ordinary
// reason and for one specific one: createTargetVolume carries the write-ahead
// discipline the engine's whole contract rests on (journal the volume, THEN
// create it) and the refusal of a volume that appeared after the preflight. A
// second copy of those is a second place for them to drift apart, and the way
// they would drift — one plan quietly deleting a volume nobody agreed to lose
// — is not a difference a test would notice unless somebody wrote it twice.

// stopNamespaceStep stops the namespace when it is running. Both plans start
// with it: nothing may run against the data while it is being moved.
func stopNamespaceStep(ctx context.Context, env Env) error {
	if !env.IsRunning() {
		return nil
	}
	if err := env.StopNamespace(ctx); err != nil {
		return fmt.Errorf("stop namespace: %w", err)
	}
	return nil
}

// pullImageStep fetches the target image before anything irreversible starts,
// so a registry that cannot be reached costs a stopped namespace and nothing
// else.
func pullImageStep(ctx context.Context, env Env, image string, p StepProgress) error {
	if err := env.PullImage(ctx, image, func(pct float64) { p(pct, "pulling "+image) }); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

// createTargetVolume makes the volume a migration writes into.
//
// Two rules meet here. (1) Write-ahead: the journal claims the volume BEFORE
// it is created, so a crash between the two cannot leave a volume the rollback
// does not know about. (2) The existence check is repeated even though the
// preflight did it, because a volume can appear in between — and if it did,
// and the user has not confirmed replacing it, this FAILS instead of deleting
// data nobody agreed to lose. The order of the two is deliberate: the refusal
// happens BEFORE anything is journaled, because a journal that claims somebody
// else's volume would have the rollback delete it.
func createTargetVolume(ctx context.Context, env Env, j *Journal, volume string, replaceExisting bool) error {
	exists, err := env.VolumeExists(ctx, volume)
	if err != nil {
		return fmt.Errorf("check volume %s: %w", volume, err)
	}
	if exists && !replaceExisting {
		return fmt.Errorf("volume %s already exists and replacing it was not confirmed", volume)
	}
	j.CreatedVolume = volume
	if err := j.Persist(); err != nil {
		return fmt.Errorf("journal the new volume: %w", err)
	}
	if exists {
		if err := env.RemoveVolume(ctx, volume); err != nil {
			return fmt.Errorf("remove the existing volume %s: %w", volume, err)
		}
	}
	if err := env.CreateVolume(ctx, volume); err != nil {
		return fmt.Errorf("create volume %s: %w", volume, err)
	}
	return nil
}
