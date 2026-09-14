package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
)

// Captured verbatim from real containers on 2026-09-15: a v1.14.1 seeded with
// two collections, three points and an alias, and the v1.15.5 that was started
// on a copy of its volume.
//
// The "docs" pair is the load-bearing one. Its status is "green" on 1.14.1 and
// "grey" on 1.15.5 for the SAME data — grey means "optimizations have not run
// yet" — and its config gained strict_mode_config across the two releases.
// Both are why the inventory reads neither field: a verify built on them would
// fail every real migration over work the upgrade itself did.
const (
	realQdrantCollectionsOut = `{"result":{"collections":[{"name":"notes"},{"name":"docs"}]},"status":"ok","time":8.496e-6}`
	realQdrantDocsOut        = `{"result":{"status":"green","optimizer_status":"ok","indexed_vectors_count":0,` +
		`"points_count":3,"segments_count":8,"config":{"params":{"vectors":{"size":4,"distance":"Cosine"},` +
		`"shard_number":1,"replication_factor":1,"write_consistency_factor":1,"on_disk_payload":true}},` +
		`"payload_schema":{}},"status":"ok","time":0.000290107}`
	realQdrantDocsAfterOut = `{"result":{"status":"grey","optimizer_status":"ok","indexed_vectors_count":0,` +
		`"points_count":3,"segments_count":8,"config":{"params":{"vectors":{"size":4,"distance":"Cosine"},` +
		`"shard_number":1,"replication_factor":1,"write_consistency_factor":1,"on_disk_payload":true},` +
		`"strict_mode_config":{"enabled":false}},"payload_schema":{}},"status":"ok","time":0.00029}`
	realQdrantNotesOut = `{"result":{"status":"green","optimizer_status":"ok","points_count":0,` +
		`"segments_count":8,"config":{"params":{"vectors":{"size":8,"distance":"Dot"}}}},"status":"ok","time":1e-5}`
	realQdrantAliasesOut = `{"result":{"aliases":[{"alias_name":"documents","collection_name":"docs"}]},` +
		`"status":"ok","time":8.727e-6}`
)

func qdrantInventoryEnv(t *testing.T, s *execScript) *migratetest.FakeEnv {
	t.Helper()
	f := migratetest.New()
	f.Containers[SrcContainer] = appdef.ApplicationDef{Name: appdef.AppQdrant, Image: qdrantFrom}
	f.Containers[DstContainer] = appdef.ApplicationDef{Name: appdef.AppQdrant, Image: qdrantTo}
	f.ExecFn = s.exec
	return f
}

// realQdrantScript answers the source with the 1.14.1 capture and the target
// with the 1.15.5 one — what an upgrade that lost nothing looks like.
func realQdrantScript() *execScript {
	out := map[string]string{
		SrcContainer + "|/collections":       realQdrantCollectionsOut,
		SrcContainer + "|/collections/docs":  realQdrantDocsOut,
		SrcContainer + "|/collections/notes": realQdrantNotesOut,
		SrcContainer + "|/aliases":           realQdrantAliasesOut,
		DstContainer + "|/collections":       realQdrantCollectionsOut,
		DstContainer + "|/collections/docs":  realQdrantDocsAfterOut,
		DstContainer + "|/collections/notes": realQdrantNotesOut,
		DstContainer + "|/aliases":           realQdrantAliasesOut,
	}
	return &execScript{out: out}
}

func readQdrant(t *testing.T, s *execScript, container string) qdrantInventory {
	t.Helper()
	inv, err := readQdrantInventory(context.Background(), qdrantInventoryEnv(t, s), container)
	require.NoError(t, err)
	q, ok := inv.(qdrantInventory)
	require.True(t, ok)
	return q
}

func TestQdrantInventoryParsesARealServersOutput(t *testing.T) {
	inv := readQdrant(t, realQdrantScript(), SrcContainer)
	assert.Equal(t, map[string]int64{"docs": 3, "notes": 0}, inv.Points)
	assert.Equal(t, map[string]string{"documents": "docs"}, inv.Aliases)
}

// The whole reason the real captures are in this file: across 1.14.1 → 1.15.5
// the SAME data changes its reported status (green → grey) and gains a config
// field. An upgrade of the real pair must verify clean.
func TestQdrantInventoryToleratesAStatusAndConfigThatMoved(t *testing.T) {
	s := realQdrantScript()
	before := readQdrant(t, s, SrcContainer)
	after := readQdrant(t, s, DstContainer)
	problems, notes := before.Diff(after)
	assert.Empty(t, problems, "the real 1.14.1 → 1.15.5 pair loses nothing")
	assert.Empty(t, notes)
}

// A collection that is gone is the failure this plan exists to catch: the RAG
// index is what every search reads.
func TestQdrantInventoryDiffFailsOnALostCollection(t *testing.T) {
	before := qdrantInventory{Points: map[string]int64{"docs": 42, "notes": 1}, Aliases: map[string]string{}}
	after := qdrantInventory{Points: map[string]int64{"notes": 1}, Aliases: map[string]string{}}
	problems, _ := before.Diff(after)
	assert.Contains(t, strings.Join(problems, "\n"), "docs")
}

// A point count that moved in EITHER direction is a problem: no client is
// connected to either container, so neither an insert nor a delete has any
// legitimate source. A dropped count is silent data loss; a raised one means
// something wrote to a container the plan believes nobody can reach.
func TestQdrantInventoryDiffFailsOnAPointCountThatMovedEitherWay(t *testing.T) {
	before := qdrantInventory{Points: map[string]int64{"docs": 42}, Aliases: map[string]string{}}
	for _, after := range []int64{41, 43} {
		problems, _ := before.Diff(qdrantInventory{
			Points: map[string]int64{"docs": after}, Aliases: map[string]string{}})
		assert.NotEmpty(t, problems, "42 → %d", after)
		assert.Contains(t, strings.Join(problems, "\n"), "42")
	}
}

// An alias is how the RAG service addresses a collection, so a lost or
// repointed one is a search that silently returns nothing.
func TestQdrantInventoryDiffFailsOnALostOrMovedAlias(t *testing.T) {
	before := qdrantInventory{
		Points:  map[string]int64{"docs": 1, "old": 1},
		Aliases: map[string]string{"documents": "docs", "archive": "old"},
	}
	after := qdrantInventory{
		Points:  map[string]int64{"docs": 1, "old": 1},
		Aliases: map[string]string{"documents": "old"},
	}
	problems, _ := before.Diff(after)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, "archive", "a lost alias is a problem")
	assert.Contains(t, joined, "documents", "an alias now naming a different collection is a problem")
}

// Anything that APPEARED is a note, never a failure: nothing writes to the
// copy but the upgrade itself, so a collection or alias the new version made
// for its own bookkeeping is reportable and not a loss.
func TestQdrantInventoryDiffReportsWhatAppeared(t *testing.T) {
	before := qdrantInventory{Points: map[string]int64{"docs": 1}, Aliases: map[string]string{}}
	after := qdrantInventory{
		Points:  map[string]int64{"docs": 1, "_internal": 0},
		Aliases: map[string]string{"fresh": "docs"},
	}
	problems, notes := before.Diff(after)
	assert.Empty(t, problems)
	joined := strings.Join(notes, "\n")
	assert.Contains(t, joined, "_internal")
	assert.Contains(t, joined, "fresh")
}

// A request that fails is an inventory that could not be read: the step has to
// fail rather than compare against a picture with holes in it.
func TestQdrantInventoryFailsWhenARequestFails(t *testing.T) {
	s := realQdrantScript()
	s.fail = map[string]string{SrcContainer + "|/collections/docs": "HTTP/1.0 503 Service Unavailable"}
	_, err := readQdrantInventory(context.Background(), qdrantInventoryEnv(t, s), SrcContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

// A body that is not JSON must fail rather than parse as "no collections" —
// an error page read as an empty server is the one verdict that must never be
// reached by accident.
func TestQdrantInventoryFailsOnUnreadableJSON(t *testing.T) {
	s := realQdrantScript()
	s.out[SrcContainer+"|/collections"] = "<html>404</html>"
	_, err := readQdrantInventory(context.Background(), qdrantInventoryEnv(t, s), SrcContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/collections")
}

// points_count is NULLABLE — a collection whose shards are still loading
// answers null — and a null read as 0 would report every point as gone, which
// is the loudest possible way to be wrong. It is an error, not a zero.
func TestQdrantInventoryRefusesACollectionWithNoPointCount(t *testing.T) {
	s := realQdrantScript()
	s.out[SrcContainer+"|/collections/docs"] = `{"result":{"status":"yellow","points_count":null},"status":"ok"}`
	_, err := readQdrantInventory(context.Background(), qdrantInventoryEnv(t, s), SrcContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docs")
}

// The inventory of something that is not a qdrant inventory cannot be
// compared, and saying so is a PROBLEM rather than a silent pass.
func TestQdrantInventoryDiffRefusesAForeignInventory(t *testing.T) {
	problems, notes := qdrantInventory{}.Diff(zkInventory{})
	assert.NotEmpty(t, problems)
	assert.Empty(t, notes)
}

// The readiness probe and every inventory request go to /readyz and the REST
// API over LOOPBACK — a temp container publishes no ports — and they are run
// by bash, because the official image ships neither curl nor wget.
func TestQdrantRequestsGoOverLoopbackWithBash(t *testing.T) {
	cmd := qdrantGetCmd(qdrantReadyPath)
	require.Equal(t, "bash", cmd[0])
	joined := strings.Join(cmd, " ")
	assert.Contains(t, joined, "/dev/tcp/127.0.0.1/"+qdrantHTTPPort)
	assert.Contains(t, joined, qdrantReadyPath)
	assert.NotContains(t, joined, "curl")
	assert.NotContains(t, joined, "wget")
	// HTTP/1.0 is what forbids a chunked response: a chunked body arrives with
	// its frame sizes interleaved and does not parse as JSON, and a server may
	// not send one to an HTTP/1.0 client.
	assert.Contains(t, joined, "HTTP/1.0")
	// /readyz and not /healthz: healthz answers while the shards are still
	// loading, and an inventory read then is a server with no points in it.
	assert.Equal(t, "/readyz", qdrantReadyPath)
	_ = deps.Qdrant
}
