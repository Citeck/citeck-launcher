package h2migrate

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// ---------------------------------------------------------------------------
// Partial (lenient) user-map reads must be VISIBLE.
//
// The strict layout/meta read closed the "reader defect looks like an empty
// store" hole one level up. One level DOWN the same hole was still open: a
// lenient user-data walk tolerated a torn sub-tree, returned the surviving
// entries with a nil error, and Migrate reported a clean success. A user whose
// storage.db lost one B-tree page would silently lose whole workspaces /
// namespaces / secrets and see a launcher that looks fine.
// ---------------------------------------------------------------------------

const (
	// wsMapName mirrors the Kotlin scope naming Database.kt uses for workspace
	// entities, so importWorkspaces' `!workspace` suffix match fires.
	wsMapName = "entities/global!workspace"
	wsJSON    = `{"name":"Enterprise","repoUrl":"https://git.example.com/e.git","repoBranch":"develop"}`
)

// buildWorkspaceStore materializes a synthetic MVStore whose single user-data
// map holds one workspace entity.
//
// With brokenSubtree the map root is an internal node whose SECOND child points
// into a chunk that does not exist: the first child's entries are recoverable,
// the rest of the sub-tree is not. That is the torn-write shape a real store
// takes when one page is clipped — the store as a whole still opens and its
// layout/meta index still verifies, which is exactly why the loss used to be
// invisible. Without it the root IS the leaf and the read is clean.
func buildWorkspaceStore(t *testing.T, brokenSubtree bool) string {
	t.Helper()
	const (
		chunkID   = 6
		metaMapID = 1
		dataMapID = 9
		leafOff   = 1200
		nodeOff   = 1700
		metaOff   = 2200
		layoutOff = 2700
	)

	pages := map[int][]byte{
		leafOff: encodeLeaf(dataMapID, [][2]string{{"enterprise", wsJSON}}, true),
	}

	rootOff := leafOff
	if brokenSubtree {
		pages[nodeOff] = encodeNode(dataMapID, []string{"m"}, []int64{
			pagePos(chunkID, leafOff),
			pagePos(99, 2048), // chunk 99 does not exist
		})
		rootOff = nodeOff
	}

	pages[metaOff] = encodeLeaf(metaMapID, [][2]string{
		{"name." + wsMapName, strconv.FormatInt(dataMapID, 16)},
	}, false)
	// Layout keys in H2's sorted order: meta.id < root.1 < root.9.
	pages[layoutOff] = encodeLeaf(0, [][2]string{
		{"meta.id", strconv.FormatInt(metaMapID, 16)},
		{"root." + strconv.FormatInt(metaMapID, 16), strconv.FormatInt(pagePos(chunkID, metaOff), 16)},
		{"root." + strconv.FormatInt(dataMapID, 16), strconv.FormatInt(pagePos(chunkID, rootOff), 16)},
	}, false)

	c := synthChunk{
		id: chunkID, block: 2, blocks: 1, version: 4,
		layoutRoot: pagePos(chunkID, layoutOff),
		pages:      pages,
	}
	return writeSynthStore(t, []synthChunk{c}, c, nil)
}

// newStoreForSynth opens a SQLite store in the same directory the synthetic
// storage.db was written to, so Migrate(homeDir, …) finds both.
func newStoreForSynth(t *testing.T, dbPath string) (string, storage.Store) {
	t.Helper()
	homeDir := filepath.Dir(dbPath)
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return homeDir, store
}

// TestPartialUserMapReadIsCounted is the unit-level half: the reader itself must
// know it dropped something. Before this, pageOpts.reject logged at Debug and
// returned nil, so readPage / ReadMap / DumpForImport all reported success.
func TestPartialUserMapReadIsCounted(t *testing.T) {
	s, err := OpenMVStore(buildWorkspaceStore(t, true))
	require.NoError(t, err)
	defer s.Close()

	entries, err := s.ReadMap(wsMapName)
	require.NoError(t, err, "user maps stay lenient — partial beats nothing")
	assert.Len(t, entries, 1, "the reachable leaf must still come through")

	pr := s.partialReadSummary()
	require.NotNil(t, pr, "a dropped sub-tree must be counted, not swallowed")
	assert.Equal(t, []string{wsMapName}, pr.Maps, "the summary must name the affected map")
	assert.Positive(t, pr.LostSubtrees)
	assert.NotEmpty(t, pr.Defects, "at least one underlying defect must be sampled")
}

// TestCleanUserMapReadCountsNothing is the control: an intact store must leave
// the accumulator empty, or every clean migration would raise a false alarm.
func TestCleanUserMapReadCountsNothing(t *testing.T) {
	s, err := OpenMVStore(buildWorkspaceStore(t, false))
	require.NoError(t, err)
	defer s.Close()

	dump, err := s.DumpForImport()
	require.NoError(t, err)
	require.Contains(t, dump, wsMapName)
	assert.Nil(t, s.partialReadSummary(), "a clean read must report no partial loss")
}

// TestDumpForImportCountsWholeMapReadFailure covers the coarsest silent loss:
// DumpForImport `continue`s past a map whose read fails outright, dropping the
// entire map with only a Warn. That must register as a partial read too.
func TestDumpForImportCountsWholeMapReadFailure(t *testing.T) {
	const (
		chunkID   = 6
		metaMapID = 1
		dataMapID = 9
		metaOff   = 2200
		layoutOff = 2700
	)
	// root.9 points into chunk 99, which does not exist: ReadMap errors out.
	pages := map[int][]byte{
		metaOff: encodeLeaf(metaMapID, [][2]string{
			{"name." + wsMapName, strconv.FormatInt(dataMapID, 16)},
		}, false),
		layoutOff: encodeLeaf(0, [][2]string{
			{"meta.id", strconv.FormatInt(metaMapID, 16)},
			{"root." + strconv.FormatInt(metaMapID, 16), strconv.FormatInt(pagePos(chunkID, metaOff), 16)},
			{"root." + strconv.FormatInt(dataMapID, 16), strconv.FormatInt(pagePos(99, 2048), 16)},
		}, false),
	}
	c := synthChunk{
		id: chunkID, block: 2, blocks: 1, version: 4,
		layoutRoot: pagePos(chunkID, layoutOff),
		pages:      pages,
	}

	s, err := OpenMVStore(writeSynthStore(t, []synthChunk{c}, c, nil))
	require.NoError(t, err)
	defer s.Close()

	_, err = s.DumpForImport()
	require.NoError(t, err)

	pr := s.partialReadSummary()
	require.NotNil(t, pr, "a map that could not be read at all must be recorded")
	assert.Equal(t, []string{wsMapName}, pr.Maps)
}

// TestMigratePartialUserMapReadRecordsDegradation is the end-to-end contract:
// the migration SUCCEEDS (most data came through), but it is marked degraded
// and a durable record naming the affected map is persisted.
func TestMigratePartialUserMapReadRecordsDegradation(t *testing.T) {
	homeDir, store := newStoreForSynth(t, buildWorkspaceStore(t, true))

	res, err := Migrate(homeDir, store)
	require.NoError(t, err, "a partial user-map read must not fail the migration")
	assert.Equal(t, 1, res.Workspaces, "the readable workspace must still be migrated")
	assert.True(t, res.Degraded, "a partial read must mark the result degraded")
	assert.Contains(t, res.PartialReason, "partial read")
	assert.Empty(t, res.FallbackReason, "the filesystem fallback did NOT run")

	ws, err := store.GetWorkspace("enterprise")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "https://git.example.com/e.git", ws.RepoURL)

	rec, err := LoadDegradedMigration(store)
	require.NoError(t, err)
	require.NotNil(t, rec, "a partial read must persist a durable warning")
	assert.True(t, rec.Partial,
		"the record must say the store WAS readable — distinct from the filesystem fallback")
	assert.Contains(t, rec.Reason, "partial read")
	assert.NotContains(t, rec.Reason, "cannot open storage.db")
	assert.Contains(t, rec.LostFields, "map."+wsMapName,
		"lostFields must name the affected map, not just carry a count")
	assert.Positive(t, rec.LostSubtrees)
	assert.Equal(t, 1, rec.Workspaces)
}

// TestDegradedMigrationJSONIsAdditive pins the wire contract the web UI reads:
// the new fields are omitempty, so a filesystem-fallback record serializes
// exactly as before (no `partial`, no `lostEntries`, no `lostSubtrees`), and a
// partial-read record adds them.
func TestDegradedMigrationJSONIsAdditive(t *testing.T) {
	marshal := func(rec DegradedMigration) map[string]any {
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		var out map[string]any
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}

	fallback := marshal(DegradedMigration{Reason: "cannot open storage.db: boom", OccurredAt: "t"})
	assert.NotContains(t, fallback, "partial", "the fallback record's shape must not change")
	assert.NotContains(t, fallback, "lostEntries")
	assert.NotContains(t, fallback, "lostSubtrees")
	for _, k := range []string{"reason", "occurredAt", "workspaces", "namespaces", "secrets", "recoveredRepoUrls"} {
		assert.Contains(t, fallback, k, "pre-existing field %q must keep its meaning", k)
	}

	partial := marshal(DegradedMigration{
		Reason: "partial read of storage.db: …", OccurredAt: "t",
		Partial: true, LostEntries: 3, LostSubtrees: 1,
	})
	assert.Equal(t, true, partial["partial"])
	assert.InEpsilon(t, float64(3), partial["lostEntries"], 0)
	assert.InEpsilon(t, float64(1), partial["lostSubtrees"], 0)
}
