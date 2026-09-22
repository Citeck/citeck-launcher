package migrate

import (
	"context"
	"errors"
	"testing"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dump and the new cluster COEXIST: the scratch directory is removed only
// in Finalize, after the commit, and the new cluster is built next to the old
// data. So when both live on ONE filesystem — the ordinary server layout,
// where the dump directory and the data volumes are directories under the same
// volumes base — the peak demand is the SUM, and checking each half against the
// same free space independently passes a disk with room for only one of them.
// The migration then dies of ENOSPC in the middle of the restore, with the
// namespace already stopped: the rollback saves the data, but this is exactly
// the failure the preflight exists to prevent BEFORE anything is stopped.
//
// envWith17Data holds 2 GiB, so each half needs 2.5 GiB and the pair 5 GiB.
func TestASharedFilesystemMustHoldTheDumpAndTheNewClusterAtOnce(t *testing.T) {
	ctx := context.Background()

	t.Run("room for one half is refused", func(t *testing.T) {
		env := envWith17Data()
		env.SharedFS = true
		env.FreeHost, env.FreeVolume = 3<<30, 3<<30
		res := PostgresMigrator{ID: deps.Postgres}.Preflight(ctx, env, Path{from17, to18})

		assert.False(t, res.OK, "3 GiB holds either the dump or the new cluster, not both")
		assert.True(t, res.SharedFilesystem)
		assert.Equal(t, res.RequiredHostBytes+res.RequiredVolumeBytes, res.RequiredTotalBytes)
		joined := joinEN(res.Problems)
		assert.Contains(t, joined, "dump", "the message names what needs the space")
		assert.Contains(t, joined, "new cluster")
		assert.Contains(t, joined, "5.0 GiB", "the sum, not either half")
		assert.Contains(t, joined, "3.0 GiB", "and what is actually free")
		assert.Empty(t, res.Warnings, "a filesystem the env answered for is not a doubt")
	})

	t.Run("room for both passes", func(t *testing.T) {
		env := envWith17Data()
		env.SharedFS = true
		env.FreeHost, env.FreeVolume = 6<<30, 6<<30
		res := PostgresMigrator{ID: deps.Postgres}.Preflight(ctx, env, Path{from17, to18})

		assert.True(t, res.OK, res.Problems)
		assert.Equal(t, int64(5<<30), res.RequiredTotalBytes)
	})

	// The two measurements answer for one filesystem, so they should agree —
	// but they are taken by two different mechanisms (a host statfs and, on a
	// desktop, df inside a container), and the honest reading of a disagreement
	// is the SMALLER number: it is the one that can run out.
	t.Run("a disagreement is read at its smaller end", func(t *testing.T) {
		env := envWith17Data()
		env.SharedFS = true
		env.FreeHost, env.FreeVolume = 100<<30, 4<<30
		res := PostgresMigrator{ID: deps.Postgres}.Preflight(ctx, env, Path{from17, to18})

		assert.False(t, res.OK)
		assert.Contains(t, joinEN(res.Problems), "4.0 GiB")
	})
}

// Two filesystems are still checked independently: on a macOS/Windows desktop
// the dump lands on the host while the new cluster is built inside the Docker
// VM, and demanding the sum of both on each would refuse a migration that fits
// twice over.
func TestSeparateFilesystemsKeepThePerFilesystemCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("one half each is enough", func(t *testing.T) {
		env := envWith17Data()
		env.SharedFS = false
		env.FreeHost, env.FreeVolume = 3<<30, 3<<30
		res := PostgresMigrator{ID: deps.Postgres}.Preflight(ctx, env, Path{from17, to18})

		assert.True(t, res.OK, res.Problems)
		assert.False(t, res.SharedFilesystem)
		assert.Zero(t, res.RequiredTotalBytes, "a sum across two disks means nothing")
	})

	t.Run("each half is still checked on its own", func(t *testing.T) {
		env := envWith17Data()
		env.SharedFS = false
		env.FreeHost = 1 << 30
		res := PostgresMigrator{ID: deps.Postgres}.Preflight(ctx, env, Path{from17, to18})

		assert.False(t, res.OK)
		joined := joinEN(res.Problems)
		assert.Contains(t, joined, "host")
		assert.NotContains(t, joined, "share one filesystem")
	})
}

// "Cannot tell" is not "different filesystems". The env answers from a stat
// that can fail (a volumes base that is not there) or from an engine that can
// refuse, and the two mistakes are not symmetric: over-requiring refuses a
// migration that would have fit and says exactly why, while under-requiring is
// an ENOSPC halfway through a restore on a stopped namespace. So the doubt
// takes the safe direction — and is reported, because a refusal for a
// requirement the operator cannot derive from their own disk is a mystery.
func TestAnUnanswerableFilesystemIdentityRequiresTheSumAndSaysSo(t *testing.T) {
	env := envWith17Data()
	env.SharedFS = false // the unsafe answer, which the failure must not produce
	env.FreeHost, env.FreeVolume = 3<<30, 3<<30
	env.FailOn["sharedfs:"] = errors.New("stat /var/lib/citeck: no such file or directory")

	res := PostgresMigrator{ID: deps.Postgres}.Preflight(context.Background(), env, Path{from17, to18})

	require.False(t, res.OK)
	assert.True(t, res.SharedFilesystem, "the safe direction")
	assert.Equal(t, int64(5<<30), res.RequiredTotalBytes)
	warned := joinEN(res.Warnings)
	assert.Contains(t, warned, "no such file or directory", "the reason it could not tell")
	assert.Contains(t, joinEN(res.Problems), "5.0 GiB")
}
