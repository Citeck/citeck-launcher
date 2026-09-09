package deps

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A namespace that has never migrated — and every state file written before
// the rollback target existed — has no target. Both halves matter: ok=false is
// what hides the rollback offer, and Gen() still answers 1 so nothing else
// changes meaning.
func TestAStateFileWithNoRollbackTargetHasNone(t *testing.T) {
	var st DependencyState
	require.NoError(t, json.Unmarshal([]byte(`{"image":"postgres:17"}`), &st))

	prev, ok := st.Previous()
	assert.False(t, ok)
	assert.Equal(t, DependencyState{}, prev)
	assert.Equal(t, 1, st.Gen())

	// And it is written back byte-identically: neither Prev field appears.
	b, err := json.Marshal(st)
	require.NoError(t, err)
	assert.JSONEq(t, `{"image":"postgres:17"}`, string(b))
}

func TestPreviousIsTheStateARollbackWouldRestore(t *testing.T) {
	st := DependencyState{Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:17.5", PrevVolumeGen: 1}
	prev, ok := st.Previous()
	require.True(t, ok)
	assert.Equal(t, DependencyState{Image: "postgres:17.5", VolumeGen: 1}, prev)

	// A previous state recorded before the counter existed answers generation
	// 1 — the same normalization Gen() does, applied where the value is read,
	// so a caller that inspects VolumeGen directly cannot see a 0.
	old := DependencyState{Image: "postgres:18", PrevImage: "postgres:17"}
	prev, ok = old.Previous()
	require.True(t, ok)
	assert.Equal(t, DependencyState{Image: "postgres:17", VolumeGen: 1}, prev)
	assert.Equal(t, 1, prev.Gen())
}

// The rollback is ONE STEP DEEP by design: keeping the chain would mean
// keeping every generation's volume forever, which is the opposite of what the
// launcher tells the operator to do with the retained one.
func TestWithPreviousDropsThePreviousStatesOwnTarget(t *testing.T) {
	older := DependencyState{Image: "postgres:16", VolumeGen: 1}
	prev := DependencyState{Image: "postgres:17.5", VolumeGen: 2}.WithPrevious(older)
	require.Equal(t, "postgres:16", prev.PrevImage)

	cur := DependencyState{Image: "postgres:18.6", VolumeGen: 3}.WithPrevious(prev)
	assert.Equal(t, "postgres:17.5", cur.PrevImage)
	assert.Equal(t, 2, cur.PrevVolumeGen)

	back, ok := cur.Previous()
	require.True(t, ok)
	assert.Empty(t, back.PrevImage, "the restored state must offer no further rollback")
	assert.Zero(t, back.PrevVolumeGen)
	_, ok = back.Previous()
	assert.False(t, ok)
}

// A rollback CLEARS the target: there is no roll-forward action, and an offer
// to re-adopt the newer volume would silently discard everything written since
// the rollback.
func TestWithoutPreviousClearsTheTarget(t *testing.T) {
	st := DependencyState{Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:17.5", PrevVolumeGen: 1}
	cleared := st.WithoutPrevious()
	assert.Equal(t, DependencyState{Image: "postgres:18.6", VolumeGen: 2}, cleared)
	_, ok := cleared.Previous()
	assert.False(t, ok)

	// The receiver is a value, so the original is untouched.
	_, ok = st.Previous()
	assert.True(t, ok)
}

// An empty previous image is "no target", not "a target with no image": a
// generation with nothing to run on would be an offer the launcher could not
// keep.
func TestWithPreviousOfNothingIsNoTarget(t *testing.T) {
	st := DependencyState{Image: "postgres:18.6", VolumeGen: 2}.WithPrevious(DependencyState{VolumeGen: 4})
	assert.Empty(t, st.PrevImage)
	assert.Zero(t, st.PrevVolumeGen)
	_, ok := st.Previous()
	assert.False(t, ok)
}

// The Prev fields are omitempty for the same reason VolumeGen is: a state file
// for a namespace that has never migrated must stay byte-identical to what
// every earlier launcher wrote.
func TestRollbackTargetRoundTrips(t *testing.T) {
	b, err := json.Marshal(DependencyState{Image: "postgres:18.6", VolumeGen: 2,
		PrevImage: "postgres:17.5", PrevVolumeGen: 1})
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"image":"postgres:18.6","volumeGen":2,"prevImage":"postgres:17.5","prevVolumeGen":1}`,
		string(b))

	var st DependencyState
	require.NoError(t, json.Unmarshal([]byte(
		`{"image":"postgres:18.6","volumeGen":2,"prevImage":"postgres:17.5","prevVolumeGen":1}`), &st))
	prev, ok := st.Previous()
	require.True(t, ok)
	assert.Equal(t, "postgres:17.5", prev.Image)
	assert.Equal(t, 1, prev.Gen())
}

// MigrationResult.Kind discriminates a rollback from a migration in the ONE
// result slot a namespace has. Empty means migration on purpose: every state
// file already written stays meaningful, the same rule VolumeGen follows.
func TestMigrationResultKind(t *testing.T) {
	assert.Empty(t, ResultKindMigration)
	assert.Equal(t, "rollback", ResultKindRollback)

	b, err := json.Marshal(MigrationResult{ID: Postgres, From: "postgres:17", To: "postgres:18"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "kind")

	var res MigrationResult
	require.NoError(t, json.Unmarshal([]byte(`{"id":"postgres","from":"a","to":"b"}`), &res))
	assert.Equal(t, ResultKindMigration, res.Kind)

	b, err = json.Marshal(MigrationResult{ID: Postgres, From: "postgres:18.6", To: "postgres:17.5",
		Kind: ResultKindRollback})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"kind":"rollback"`)
}
