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

// Captured from a real zookeeper:3.9.5 (2026-09-09), trimmed to the fields
// that are read. The log noise in the listing is verbatim and load-bearing:
// zkCli writes it to STDOUT, mixed in with its results, and one of the lines
// CONTAINS a path ("Client environment:java.class.path=/apache-zookeeper-…")
// without beginning with one.
const (
	realZkMonitorOut = `{
  "version" : "3.9.5-293c895a8d966a3ecb92872be4a1daf87d725da2, built on 2026-02-11 20:18 UTC",
  "ephemerals_count" : 1,
  "znode_count" : 9,
  "watch_count" : 0,
  "approximate_data_size" : 102,
  "command" : "monitor",
  "error" : null
}`
	realZkDumpOut = `{
  "expiry_time_to_session_ids" : {
    "1877408000" : [ ]
  },
  "session_id_to_ephemeral_paths" : {
    "0x1006fe6766f0000" : [ "/ephemeral" ]
  },
  "command" : "dump",
  "error" : null
}`
	realZkListOut = `Connecting to 127.0.0.1:2181
2026-09-09 05:42:00,059 [myid:] - INFO  [main:o.a.z.Environment@98] - Client environment:java.class.path=/apache-zookeeper-3.9.5-bin/lib/zookeeper-3.9.5.jar:/conf:

WATCHER::

WatchedEvent state:SyncConnected type:None path:null zxid: -1
/
/ecos
/ephemeral
/zkx
/zookeeper
/ecos/patches
/ecos/patches/emodel
/ecos/patches/emodel/results
2026-09-09 05:42:00,339 [myid:] - INFO  [main:o.a.z.u.ServiceUtils@45] - Exiting JVM with code 0
`
)

func zkInventoryEnv(t *testing.T, s *execScript) *migratetest.FakeEnv {
	t.Helper()
	f := migratetest.New()
	f.Containers[SrcContainer] = appdef.ApplicationDef{Name: appdef.AppZookeeper, Image: zkFrom}
	f.ExecFn = s.exec
	return f
}

// realZkScript answers both temp containers with the same real capture, which
// is what an upgrade that lost nothing looks like.
func realZkScript() *execScript {
	out := map[string]string{}
	for _, c := range []string{SrcContainer, DstContainer} {
		out[c+"|commands/monitor"] = realZkMonitorOut
		out[c+"|commands/dump"] = realZkDumpOut
		out[c+"|ls -R"] = realZkListOut
	}
	return &execScript{out: out}
}

func readZk(t *testing.T, s *execScript) zkInventory {
	t.Helper()
	inv, err := readZkInventory(context.Background(), zkInventoryEnv(t, s), SrcContainer)
	require.NoError(t, err)
	z, ok := inv.(zkInventory)
	require.True(t, ok)
	return z
}

// The parser is pinned against a real server's output. Two shapes matter: the
// AdminServer's JSON (whose keys are session_id_to_ephemeral_paths, NOT the
// "ephemeral_nodes" one might guess) and zkCli's listing, whose own log lines
// share stdout with the paths.
func TestZkInventoryParsesARealServersOutput(t *testing.T) {
	inv := readZk(t, realZkScript())
	assert.Equal(t, []string{
		"/", "/ecos", "/ecos/patches", "/ecos/patches/emodel", "/ecos/patches/emodel/results",
		"/ephemeral", "/zkx", "/zookeeper",
	}, inv.Paths, "only the lines that BEGIN with a slash are znodes")
	assert.Equal(t, []string{"/ephemeral"}, inv.Ephemerals)
	assert.Equal(t, 9, inv.ZnodeCount)
	assert.Equal(t, int64(102), inv.DataSize)
}

// A command that fails is an inventory that could not be read: the step has to
// fail rather than compare against a picture with holes in it.
func TestZkInventoryFailsWhenACommandFails(t *testing.T) {
	s := realZkScript()
	s.fail = map[string]string{SrcContainer + "|ls -R": "KeeperErrorCode = ConnectionLoss"}
	_, err := readZkInventory(context.Background(), zkInventoryEnv(t, s), SrcContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ConnectionLoss")
}

// Output that is not JSON at all (an admin server answering an error page)
// must fail rather than parse as "no znodes".
func TestZkInventoryFailsOnUnreadableJSON(t *testing.T) {
	s := realZkScript()
	s.out[SrcContainer+"|commands/monitor"] = "<html>404</html>"
	_, err := readZkInventory(context.Background(), zkInventoryEnv(t, s), SrcContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor")
}

// The rule from E.2: a PERSISTENT path that disappeared is the failure that
// matters — that is where ecos/patches/<app>/results/* lives, and its absence
// silently re-runs a patch on every webapp.
func TestZkInventoryDiffFailsOnALostPersistentPath(t *testing.T) {
	before := zkInventory{
		Paths:      []string{"/", "/ecos/patches/emodel/results", "/ephemeral"},
		Ephemerals: []string{"/ephemeral"},
		ZnodeCount: 3, DataSize: 100,
	}
	after := zkInventory{Paths: []string{"/"}, ZnodeCount: 1, DataSize: 10}
	problems, _ := before.Diff(after)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, "/ecos/patches/emodel/results")
	assert.NotContains(t, joined, "/ephemeral", "an ephemeral is not a loss")
}

// Ephemerals belong to client sessions, the copy is booted with NO clients, so
// their sessions expire while the migration runs. A comparison that treated
// that as a failure would fail every real migration; one that treated nothing
// as a failure would not notice a lost patch marker.
func TestZkInventoryDiffToleratesEphemeralsAndReportsThem(t *testing.T) {
	before := zkInventory{
		Paths:      []string{"/", "/locks/emodel-0001", "/ecos"},
		Ephemerals: []string{"/locks/emodel-0001"},
		ZnodeCount: 3, DataSize: 100,
	}
	after := zkInventory{Paths: []string{"/", "/ecos"}, ZnodeCount: 2, DataSize: 80}
	problems, notes := before.Diff(after)
	assert.Empty(t, problems)
	joined := strings.Join(notes, "\n")
	assert.Contains(t, joined, "/locks/emodel-0001")
	assert.Contains(t, joined, "znode count", "the counts are reported, never enforced")
	assert.Contains(t, joined, "data size")
}

// A path that APPEARED is a note: nothing is connected to the copy, so it is
// the new version's own bookkeeping, and failing on it would refuse a
// migration that lost nothing.
func TestZkInventoryDiffReportsNewPathsWithoutFailing(t *testing.T) {
	before := zkInventory{Paths: []string{"/"}, ZnodeCount: 1}
	after := zkInventory{Paths: []string{"/", "/zookeeper/config"}, ZnodeCount: 2}
	problems, notes := before.Diff(after)
	assert.Empty(t, problems)
	assert.Contains(t, strings.Join(notes, "\n"), "/zookeeper/config")
}

// The verify compares two pictures of the same kind; anything else is a bug in
// the plan and must not read as "nothing changed".
func TestZkInventoryDiffRefusesAForeignInventory(t *testing.T) {
	problems, _ := zkInventory{}.Diff(countInventory{})
	assert.NotEmpty(t, problems)
}

// The whole verify, through the plan: the copy loses a persistent znode and
// the migration is rolled back to the untouched original.
func TestZkVerifyRollsBackWhenTheCopyLostAZnode(t *testing.T) {
	s := realZkScript()
	s.out[DstContainer+"|ls -R"] = "/\n/zookeeper\n"
	env := zkEnv(t, s)
	err := runZkPlan(t, env, zkFrom, zkTo)
	require.ErrorContains(t, err, "/ecos/patches/emodel/results")
	assert.NotContains(t, env.Volumes, deps.VolumeName(zookeeperDescriptor(t), 2),
		"the rollback removed the copy")
	assert.Contains(t, env.Volumes, deps.VolumeName(zookeeperDescriptor(t), 1))
}
