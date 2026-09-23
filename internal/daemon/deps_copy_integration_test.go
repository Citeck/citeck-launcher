//go:build integration

package daemon

// Real-Docker tests for the COPY-upgrade plan — the shape RabbitMQ and
// ZooKeeper use, where there is no logical dump that reproduces a node and the
// migration therefore copies the data volume and upgrades the COPY.
//
// They run the same production pieces the PostgreSQL tests do (the daemon's own
// depsEnv over a real *docker.Client, a real namespace.Runtime as the journal
// store, the migrator's plan through migrate.Run), and they prove the four
// things no fake can:
//
//   - a REAL RabbitMQ 4.1 → 4.2 upgrade carries the messages. The vendor's
//     logical export carries the topology and no messages at all, which is
//     exactly why this plan copies bytes — and nothing measured before this
//     test had ever published one;
//   - the copy preserves OWNERSHIP. The data is owned by the image's uid, and a
//     root-owned copy is not a slower migration, it is a broker that will not
//     start (.erlang.cookie is mode 0400);
//   - the SOURCE volume is byte-identical afterwards. That is the whole safety
//     story of the plan, and it is asserted as a manifest taken with nothing
//     running, before and after (ruling on OPEN QUESTION 4);
//   - a temp container with a pinned node identity does NOT answer to the app's
//     name on the namespace network. Both halves at once: /etc/hosts inside,
//     nothing outside.
//
// ROOTLESS DOCKER: same requirement as the PostgreSQL tests, and stated in the
// same words — see the header of deps_integration_test.go and
// requirePrivilegeOverContainerFiles, which fails early and with the fix.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

const (
	// itRabbitFrom / itRabbitTo are the pair ruling 2 is about: 4.2 is where
	// `enable_feature_flag all` starts the irreversible Mnesia → Khepri
	// migration, which on 4.1 the same command leaves alone.
	itRabbitFrom = "rabbitmq:4.1.2-management"
	itRabbitTo   = "rabbitmq:4.2.9-management"
	// itZkFrom / itZkTo are the pair zk-experiment.md measured. 3.9.5 is also
	// the generator's own fallback, so this is a genuine forward move onto the
	// version a namespace would otherwise be held back from.
	itZkFrom = "zookeeper:3.8.6"
	itZkTo   = "zookeeper:3.9.5"

	// itDNSProbeContainer is a container of this namespace that is NOT the
	// migration's — what "does the temp container answer on the namespace
	// network?" has to be asked from.
	itDNSProbeContainer = "depsit-dnsprobe"

	// itRabbitClassicMsgs / itRabbitQuorumMsgs / itRabbitVhostMsgs are how many
	// messages each seeded queue holds. Different numbers on purpose: a
	// migration that moved a count from one queue to another would compare
	// equal if they were the same.
	itRabbitClassicMsgs = 5
	itRabbitQuorumMsgs  = 3
	itRabbitVhostMsgs   = 1
)

// itRabbitNodeName is the node the namespace's own RabbitMQ container runs as,
// derived from the SAME constant the generator and the migrator derive it from
// — a literal "rabbit@rabbitmq" here would keep passing while the app was
// renamed and the plan pinned something else.
var itRabbitNodeName = "rabbit@" + appdef.AppRabbitmq

// itRabbitTempOpts is the node-identity pin every RabbitMQ container this file
// starts under an override name needs. BOTH halves are mandatory: the
// environment names the node (and with it the data directory inside the
// volume), the /etc/hosts entry resolves its host part, and without the latter
// Erlang refuses to boot at all ("epmd error for host rabbitmq: nxdomain").
//
// The SEED needs it as much as the plan's temp containers do: without it the
// seed would write its data under rabbit@depsit-seed, and a migration of that
// volume would faithfully copy a node the namespace's own container never
// reads.
func itRabbitTempOpts(name string) deps.TempContainerOpts {
	return deps.TempContainerOpts{
		Name:      name,
		Env:       map[string]string{"RABBITMQ_NODENAME": itRabbitNodeName},
		HostAlias: map[string]string{appdef.AppRabbitmq: "127.0.0.1"},
	}
}

// TestIntegration_TargetImagesAreWhatTheGeneratorOffers is the guard on every
// image constant in this build tag, and it needs no Docker: run it alone with
// `go test -tags integration ./internal/daemon/ -run TestIntegration_TargetImagesAreWhatTheGeneratorOffers`.
//
// GenerateDefFor refuses a def whose image is not the one it was asked for, and
// the pin gate emits a requested version verbatim only while the move is
// BREAKING. So any image in this build tag that sits inside the bundle's own
// series — i.e. one the gate would resolve to the candidate rather than emit as
// asked — must be exactly the image the generator offers, or the guard rejects
// it and every temp container with it. That is a real failure and it took a
// five-minute Docker run to see; here it is a millisecond, and the message
// names the string to change.
//
// Which END of a pair that is follows from where the launcher's own default
// sits, not from habit: the resolvable end is the one whose version is NOT a
// format break away from the default. PostgreSQL's SOURCE is the checked one —
// 17.5 IS the default, while its target 18.6 is a different major and is
// therefore emitted verbatim. RabbitMQ has the same shape (4.1.2 is the
// default; 4.1 → 4.2 is a format break, so the target owes the bundle nothing).
// ZooKeeper is the mirror image: its TARGET 3.9.5 is the default and its source
// 3.8.6 is a minor break away, so there it is the target that has to match.
func TestIntegration_TargetImagesAreWhatTheGeneratorOffers(t *testing.T) {
	genResp, err := namespace.Generate(
		&namespace.Config{ID: "imgcheck"},
		&bundle.Def{Applications: map[string]bundle.AppDef{}},
		&bundle.WorkspaceConfig{},
		namespace.SystemSecrets{},
		namespace.GenerateOpts{},
	)
	require.NoError(t, err)
	for _, c := range []struct {
		id       deps.ID
		image    string
		constant string
	}{
		{deps.Postgres, itFromImage, "itFromImage"},
		{deps.Zookeeper, itZkTo, "itZkTo"},
		{deps.RabbitMQ, itRabbitFrom, "itRabbitFrom"},
	} {
		assert.Equalf(t, genResp.Dependencies[c.id].Candidate, c.image,
			"%s must be the image the generator offers for %s, or GenerateDefFor refuses every "+
				"container these tests start", c.constant, c.id)
	}
}

// waitContainer polls until ready answers true, failing with the container's
// name and the wait budget rather than hanging until the test's own deadline.
// The container is re-checked on every pass: an image that EXITS is a failure
// to report now, not in three minutes.
func (e *itEnv) waitContainer(ctx context.Context, t *testing.T, container, what string,
	ready func(context.Context) bool,
) {
	t.Helper()
	started := time.Now()
	deadline := started.Add(itReadyWait)
	for {
		running, err := e.env.ContainerRunning(ctx, container)
		require.NoError(t, err)
		if !running {
			t.Fatalf("container %s exited before %s became ready\n%s", container, what, e.containerLog(ctx, container))
		}
		if ready(ctx) {
			t.Logf("%s in %s ready in %s", what, container, time.Since(started).Round(time.Millisecond))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s in container %s did not become ready within %s\n%s",
				what, container, itReadyWait, e.containerLog(ctx, container))
		}
		time.Sleep(itReadyPoll)
	}
}

// itLogTail is how much of a container's log a failure shows. A RabbitMQ boot
// failure ends in an Erlang crash report of well over a hundred lines, and the
// line that says WHY is the first of them.
const itLogTail = 400

// containerLog is what a wait that timed out has to show to be diagnosable at
// all: the test's own cleanup purges the namespace, so a container that failed
// to boot is gone by the time anyone could look at it by hand.
func (e *itEnv) containerLog(ctx context.Context, container string) string {
	out, err := e.dc.ContainerLogs(ctx, e.dc.ContainerName(container), itLogTail)
	if err != nil {
		return fmt.Sprintf("(logs of %s unavailable: %v)", container, err)
	}
	return fmt.Sprintf("--- last %d log lines of %s ---\n%s", itLogTail, container, out)
}

// containerLogHas reports whether the container's log tail contains needle.
// Reading a log is the only way to watch a container that must not be TOUCHED
// while it boots — see waitRabbit.
func (e *itEnv) containerLogHas(ctx context.Context, container, needle string) bool {
	out, err := e.dc.ContainerLogs(ctx, e.dc.ContainerName(container), itLogTail)
	return err == nil && strings.Contains(out, needle)
}

// execOK runs a command and reports whether it ran and exited 0 — the shape a
// readiness probe needs, where "it failed" and "it could not run yet" are the
// same answer.
func (e *itEnv) execOK(ctx context.Context, container string, cmd ...string) bool {
	_, _, code, err := e.env.Exec(ctx, container, cmd)
	return err == nil && code == 0
}

// mustExec runs a command and fails the test on anything but a clean exit,
// returning its trimmed stdout.
func (e *itEnv) mustExec(ctx context.Context, t *testing.T, container string, cmd ...string) string {
	t.Helper()
	stdout, stderr, code, err := e.env.Exec(ctx, container, cmd)
	require.NoErrorf(t, err, "%v", cmd)
	require.Zerof(t, code, "%v: exit %d\nstdout: %s\nstderr: %s", cmd, code, stdout, stderr)
	return strings.TrimSpace(stdout)
}

// --- RabbitMQ ---------------------------------------------------------------

// waitRabbit waits for the readiness the plan itself waits for — the node is
// running AND its listeners accept, since `ping` and an open 5672 are both true
// long before either — but it watches the LOG until the boot is complete before
// it runs the first command inside the container.
//
// That order is not caution, it is a measured hazard of a FRESH data directory.
// The image runs its Erlang tools with HOME=/var/lib/rabbitmq, and an Erlang VM
// that finds no .erlang.cookie there CREATES one, mode 0400, owned by whoever
// ran it — and `docker exec` runs as ROOT. A readiness probe fired into the
// second or two before the entrypoint has finished its own chown therefore
// leaves the node's cookie unreadable to the uid the server drops to, and the
// boot dies with `Error when reading /var/lib/rabbitmq/.erlang.cookie: eacces`
// → "Kernel pid terminated" (measured on this box, reproducibly, with a
// one-second exec loop against an empty bind directory).
//
// The MIGRATION is covered from the other side: its readiness wait
// (migrate.waitForRabbit) will not run an Erlang tool until the cookie exists.
// A copy USUALLY carries one already — it is a copy of a volume a broker has
// run on — but "usually" is not the guarantee it reads as: the preflight
// admits a source volume that merely EXISTS, and the pin seeding never looks
// inside a RabbitMQ volume, so an empty one reaches the plan and the copy of
// it has no cookie either. A test that seeds a fresh volume meets the window
// head-on, so the seed must watch rather than poke.
func (e *itEnv) waitRabbit(ctx context.Context, t *testing.T, container string) {
	t.Helper()
	e.waitContainer(ctx, t, container, "the RabbitMQ boot", func(ctx context.Context) bool {
		return e.containerLogHas(ctx, container, "Server startup complete")
	})
	e.waitContainer(ctx, t, container, "RabbitMQ", func(ctx context.Context) bool {
		return e.execOK(ctx, container, "rabbitmq-diagnostics", "-q", "check_running") &&
			e.execOK(ctx, container, "rabbitmq-diagnostics", "-q", "check_port_connectivity")
	})
}

// rabbitRows runs a rabbitmqctl listing exactly as the production inventory
// does and returns its raw TAB-separated lines.
//
// It is a SECOND reader of the same broker, on purpose. The plan's own verify
// compares the copy against the copy, so a copy that came up empty would
// compare equal to itself and pass; what makes this test worth its minutes is
// that the numbers below are compared against what the SEED put there.
func (e *itEnv) rabbitRows(ctx context.Context, t *testing.T, container string, args ...string) []string {
	t.Helper()
	out := e.mustExec(ctx, t, container, append([]string{"rabbitmqctl", "-q", "--no-table-headers"}, args...)...)
	var rows []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) != "" {
			rows = append(rows, strings.TrimRight(line, "\r"))
		}
	}
	sort.Strings(rows)
	return rows
}

// rabbitQueues lists one virtual host's queues with the two fields a migration
// can lose: what kind of queue it is, and how many messages are in it.
func (e *itEnv) rabbitQueues(ctx context.Context, t *testing.T, container, vhost string) []string {
	t.Helper()
	return e.rabbitRows(ctx, t, container, "list_queues", "--vhost", vhost, "name", "durable", "type", "messages")
}

// rabbitadmin runs the management CLI the image ships, which is how a message
// gets published without a client library.
func (e *itEnv) rabbitadmin(ctx context.Context, t *testing.T, container, pass string, args ...string) string {
	t.Helper()
	return e.mustExec(ctx, t, container, append([]string{"rabbitmqadmin", "-u", "admin", "-p", pass}, args...)...)
}

// seedRabbit brings up the FROM image on the namespace's own rabbitmq2 bind
// directory and fills it with what a real stand has: a second virtual host, the
// citeck service account, a topic exchange, a durable classic queue and a
// QUORUM queue, a binding, a policy — and persistent messages in all three
// queues. The container is removed afterwards: the migration must start from
// data alone.
//
// The messages are the point. RabbitMQ's own logical export carries none, so
// "the topology came across" is not what a user means by "my data survived".
func (e *itEnv) seedRabbit(ctx context.Context, t *testing.T) {
	t.Helper()
	started := time.Now()
	require.NoError(t, e.env.PullImage(ctx, itRabbitFrom, func(float64) {}))

	def, err := e.env.GenerateDefFor(deps.RabbitMQ, deps.DependencyState{Image: itRabbitFrom})
	require.NoError(t, err)
	pass, ok := def.Environments.Get("RABBITMQ_DEFAULT_PASS")
	require.True(t, ok, "the generated def must carry the admin password the management API needs")

	_, err = e.env.RunAppDef(ctx, def, itRabbitTempOpts(itSeedContainer))
	require.NoError(t, err)
	e.waitRabbit(ctx, t, itSeedContainer)

	ctl := func(args ...string) {
		e.mustExec(ctx, t, itSeedContainer, append([]string{"rabbitmqctl", "-q"}, args...)...)
	}
	ctl("add_vhost", itRabbitVhost)
	ctl("add_user", "citeck", "citeckpass")
	ctl("set_user_tags", "citeck", "monitoring")
	ctl("set_permissions", "-p", "/", "citeck", ".*", ".*", ".*")
	// admin needs them too, or the management API refuses every call scoped to
	// the second virtual host ("Access refused: /api/queues/citeck/...").
	ctl("set_permissions", "-p", itRabbitVhost, "admin", ".*", ".*", ".*")
	ctl("set_policy", "-p", "/", "ecos-ha", "^ecos\\.", `{"max-length":10000}`, "--apply-to", "queues")

	// The virtual host is a GLOBAL option of rabbitmqadmin and goes before the
	// subcommand: its option parser is not documented to accept an interspersed
	// one, and a `-V` the parser swallowed as part of `declare queue` would
	// silently declare the queue in the DEFAULT virtual host — the seed would
	// then be missing exactly the thing the second vhost is there to prove.
	adm := func(args ...string) { e.rabbitadmin(ctx, t, itSeedContainer, pass, args...) }
	admIn := func(vhost string, args ...string) { adm(append([]string{"-V", vhost}, args...)...) }
	adm("declare", "exchange", "name=ecos-topic", "type=topic", "durable=true")
	adm("declare", "queue", "name="+itRabbitClassicQueue, "durable=true")
	adm("declare", "queue", "name="+itRabbitQuorumQueue, "durable=true", `arguments={"x-queue-type":"quorum"}`)
	adm("declare", "binding", "source=ecos-topic", "destination="+itRabbitClassicQueue,
		"destination_type=queue", "routing_key=ecos.records.#")
	admIn(itRabbitVhost, "declare", "queue", "name="+itRabbitVhostQueue, "durable=true")

	// delivery_mode 2 on purpose: a transient message would be perfectly
	// entitled to vanish across the copy, and the test would then be asserting
	// nothing about the migration.
	publish := func(vhost, queue string, n int) {
		for i := 1; i <= n; i++ {
			admIn(vhost, "publish", "routing_key="+queue,
				fmt.Sprintf("payload=%s-%d", queue, i), `properties={"delivery_mode":2}`)
		}
	}
	publish("/", itRabbitClassicQueue, itRabbitClassicMsgs)
	publish("/", itRabbitQuorumQueue, itRabbitQuorumMsgs)
	publish(itRabbitVhost, itRabbitVhostQueue, itRabbitVhostMsgs)

	// A quorum queue's counter lags its own writes by a moment (measured: 0 for
	// about a second after the last publish), so the seed is not finished until
	// the broker AGREES with what was published. Stopping before that would
	// bake a race into every later assertion.
	e.waitContainer(ctx, t, itSeedContainer, "the published message counts", func(ctx context.Context) bool {
		return sameStrings(e.rabbitQueues(ctx, t, itSeedContainer, "/"), itRabbitDefaultVhostQueues())
	})

	require.NoError(t, e.env.StopRemove(ctx, itSeedContainer))
	t.Logf("seed: %s node %s ready in %s", itRabbitFrom, itRabbitNodeName, time.Since(started).Round(time.Millisecond))
}

const (
	itRabbitVhost        = "citeck"
	itRabbitClassicQueue = "ecos.classic"
	itRabbitQuorumQueue  = "ecos.quorum"
	itRabbitVhostQueue   = "ecos.vhostq"
)

// itRabbitDefaultVhostQueues is what `list_queues` must print for the default
// virtual host, before and after the migration. Sorted, because rabbitmqctl's
// row order is not stable across versions.
func itRabbitDefaultVhostQueues() []string {
	rows := []string{
		fmt.Sprintf("%s\ttrue\tclassic\t%d", itRabbitClassicQueue, itRabbitClassicMsgs),
		fmt.Sprintf("%s\ttrue\tquorum\t%d", itRabbitQuorumQueue, itRabbitQuorumMsgs),
	}
	sort.Strings(rows)
	return rows
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestIntegration_Rabbit41To42 is the whole point of the copy-upgrade plan: a
// real 4.1 → 4.2 upgrade, on a copy, with the messages intact and the original
// volume untouched.
func TestIntegration_Rabbit41To42(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.RabbitMQ, itRabbitFrom)
	e.requirePrivilegeOverContainerFiles(ctx, t)
	e.seedRabbit(ctx, t)

	src, dst := itVolumeOf(deps.RabbitMQ, 1), itVolumeOf(deps.RabbitMQ, 2)
	before := e.volumeManifest(ctx, t, src)
	require.NotEmpty(t, before, "a seeded RabbitMQ volume is not empty; an empty manifest means the walk failed")

	pre := migrate.RabbitMigrator{}.Preflight(ctx, e.env, migrate.Path{itRabbitFrom, itRabbitTo})
	require.True(t, pre.OK, "preflight problems: %v", pre.Problems)
	t.Logf("preflight: data %d B, free volume %d B, warnings %v", pre.DataSizeBytes, pre.FreeVolumeBytes, pre.Warnings)

	plan, journal, err := migrate.RabbitMigrator{}.Plan(ctx, e.env, migrate.Path{itRabbitFrom, itRabbitTo}, migrate.PlanOptions{})
	require.NoError(t, err)

	timer := newStepTimer()
	started := time.Now()
	runErr := migrate.Run(ctx, e.rt, journal, plan, timer.progress)
	total := time.Since(started)
	steps := timer.report(t)
	require.NoError(t, runErr)
	t.Logf("migration %s → %s took %s", itRabbitFrom, itRabbitTo, total.Round(time.Millisecond))
	// The plan's own exported list, never a copy: a step renamed there must
	// fail HERE rather than quietly stop being asserted.
	assert.Equal(t, migrate.CopyStepIDs(), steps)

	// --- the pin, the journal and the verdict --------------------------------
	st := e.rt.DependencyStates()[deps.RabbitMQ]
	assert.Equal(t, itRabbitTo, st.Image)
	assert.Equal(t, 2, st.Gen(), "a completed migration advances the volume generation by exactly one")
	prev, has := st.Previous()
	require.True(t, has, "the commit records where the namespace came from")
	assert.Equal(t, itRabbitFrom, prev.Image)
	assert.Equal(t, 1, prev.Gen())
	assert.Nil(t, e.rt.MigrationJournal(), "a committed migration clears the journal")
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, src, last.OldVolume)
	assert.Equal(t, 1, e.reloads.get(), "finalize reloads the namespace exactly once")

	// --- the source volume is byte-identical ---------------------------------
	// The invariant the whole design rests on, and the reason a copy upgrade is
	// worth its disk: nothing in the plan opens the source for writing, so
	// every entry's size, owner and mode must be exactly what it was.
	assert.Equal(t, before, e.volumeManifest(ctx, t, src), "the source volume was written to")

	// --- the copy carries the node's identity --------------------------------
	// A temp container that boots a fresh node inside the copy reports healthy
	// and passes a verify that compares two empty nodes, so the DIRECTORY is
	// checked directly: the node's own name, and no node named after a temp
	// container.
	after := e.volumeManifest(ctx, t, dst)
	_, hasNode := itManifestEntry(after, "./mnesia/"+itRabbitNodeName)
	assert.True(t, hasNode, "the copy holds no %s directory: %v", itRabbitNodeName, after)
	for _, line := range after {
		assert.NotContains(t, line, migrate.SrcContainer, "a temp container's node was written into the copy")
		assert.NotContains(t, line, migrate.DstContainer, "a temp container's node was written into the copy")
	}

	// --- what the migrated node actually holds -------------------------------
	def, err := e.env.GenerateDefFor(deps.RabbitMQ, deps.DependencyState{Image: itRabbitTo, VolumeGen: 2})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, itRabbitTempOpts(itCheckContainer))
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitRabbit(ctx, t, itCheckContainer)

	assert.Contains(t, e.mustExec(ctx, t, itCheckContainer, "rabbitmq-diagnostics", "-q", "check_running"),
		itRabbitNodeName, "the migrated node must answer as the namespace's own node")
	// Ruling 2's deliberate consequence, and the reason a 4.2 target is
	// breaking at all: on the NEW image `all` includes khepri_db, so the
	// Mnesia → Khepri migration happens here — on the copy.
	assert.Contains(t, e.rabbitRows(ctx, t, itCheckContainer, "list_feature_flags", "name", "state"),
		"khepri_db\tenabled", "4.2's post-upgrade step must enable the Khepri metadata store")

	assert.Equal(t, []string{"admin\t[administrator]", "citeck\t[monitoring]"},
		e.rabbitRows(ctx, t, itCheckContainer, "list_users"))
	assert.Equal(t, []string{"/", itRabbitVhost}, e.rabbitRows(ctx, t, itCheckContainer, "list_vhosts", "name"))
	assert.Equal(t, itRabbitDefaultVhostQueues(), e.rabbitQueues(ctx, t, itCheckContainer, "/"),
		"the durable classic queue, the quorum queue and their MESSAGES")
	assert.Equal(t, []string{fmt.Sprintf("%s\ttrue\tclassic\t%d", itRabbitVhostQueue, itRabbitVhostMsgs)},
		e.rabbitQueues(ctx, t, itCheckContainer, itRabbitVhost), "a second virtual host's data comes across too")
	assert.Contains(t, e.rabbitRows(ctx, t, itCheckContainer, "list_exchanges", "--vhost", "/", "name", "type", "durable"),
		"ecos-topic\ttopic\ttrue")
	assert.Contains(t, e.rabbitRows(ctx, t, itCheckContainer, "list_bindings", "--vhost", "/",
		"source_name", "destination_name", "destination_kind", "routing_key"),
		"ecos-topic\t"+itRabbitClassicQueue+"\tqueue\tecos.records.#")
	policies := e.rabbitRows(ctx, t, itCheckContainer, "list_policies", "--vhost", "/")
	require.Len(t, policies, 1)
	assert.Contains(t, policies[0], "ecos-ha")
	assert.Contains(t, policies[0], `{"max-length":10000}`)
}

// TestIntegration_CopyPreservesOwnership is the one property no fake can check:
// the copy is owned by the image's uid, entry for entry.
//
// It is not a detail. A copy that lands root-owned is not a slower migration,
// it is a broker that will not start — .erlang.cookie is mode 0400, and a
// RabbitMQ that cannot read it fails its boot with "eacces" → "Kernel pid
// terminated" (measured).
func TestIntegration_CopyPreservesOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.RabbitMQ, itRabbitFrom)
	e.requirePrivilegeOverContainerFiles(ctx, t)

	src, dst := itVolumeOf(deps.RabbitMQ, 1), itVolumeOf(deps.RabbitMQ, 2)
	// A booted node is all this needs — the cookie, the node directory and the
	// feature-flag file are written before anything is declared — so it does
	// not pay for the full topology the migration test seeds.
	require.NoError(t, e.env.PullImage(ctx, itRabbitFrom, func(float64) {}))
	def, err := e.env.GenerateDefFor(deps.RabbitMQ, deps.DependencyState{Image: itRabbitFrom})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, itRabbitTempOpts(itSeedContainer))
	require.NoError(t, err)
	e.waitRabbit(ctx, t, itSeedContainer)
	require.NoError(t, e.env.StopRemove(ctx, itSeedContainer))

	require.NoError(t, e.env.CreateVolume(ctx, dst))
	defer func() { assert.NoError(t, e.env.RemoveVolume(context.Background(), dst)) }()
	require.NoError(t, e.env.CopyVolume(ctx, src, dst))

	srcManifest := e.volumeManifest(ctx, t, src)
	require.NotEmpty(t, srcManifest)
	assert.Equal(t, itOwnershipOf(srcManifest), itOwnershipOf(e.volumeManifest(ctx, t, dst)),
		"every entry of the copy must carry the source's owner, mode and size")

	// …and the assertion above is only worth anything if the source is NOT
	// root-owned to begin with: on a copy made by a root `cp` every entry would
	// be uid 0 on both sides and the comparison would pass while the broker
	// died on its cookie.
	cookie, ok := itManifestEntry(srcManifest, "./.erlang.cookie")
	require.Truef(t, ok, "no .erlang.cookie in %v", srcManifest)
	assert.NotContains(t, cookie, "|0|0|", "the cookie must be owned by the image's uid, not by root: %s", cookie)
	assert.True(t, strings.HasSuffix(cookie, "|400"), "the cookie's mode is what makes ownership load-bearing: %s", cookie)
}

// --- ZooKeeper --------------------------------------------------------------

// itZkDataset is the shape zk-experiment.md used, trimmed to what each entry
// proves: a Citeck-shaped patch-result marker (the data whose silent loss costs
// the most — a missing marker re-runs a local patch on every webapp), a
// non-ASCII value, and a deep path.
var itZkDataset = []struct{ path, value string }{
	{"/zkx", "root"},
	{"/zkx/n00", "value-00"},
	{"/zkx/n01", "value-01"},
	{"/zkx/unicode", "Привет☃мир-Ω-æøå"},
	{"/citeck", "c"},
	{"/citeck/deep", "d"},
	{"/citeck/deep/a", "a"},
	{"/citeck/deep/a/b", "b"},
	{"/citeck/deep/a/b/c", "deep-leaf-значение"},
	{"/ecos", "e"},
	{"/ecos/patches", "p"},
	{"/ecos/patches/emodel", "m"},
	{"/ecos/patches/emodel/results", "r"},
	{"/ecos/patches/emodel/results/patch-2024-01-fix-refs", `{"status":"APPLIED","zxid":1}`},
}

// itZkEphemeral is created by a session that is still OPEN when the seed
// container is stopped. Its fate across the migration is deliberately not
// pinned: an ephemeral belongs to a client session, the copy is booted with no
// clients, and whether the session outlives the temp containers is a matter of
// timing. What IS pinned is that whichever way it goes, it does not fail the
// migration — see the assertion at the end of the ZooKeeper test.
const itZkEphemeral = "/zkx/ephemeral"

// zkCli runs one zkCli command against the loopback client port and returns its
// STDOUT — where `ls` and `get` put their results (measured).
func (e *itEnv) zkCli(ctx context.Context, t *testing.T, container string, args ...string) string {
	t.Helper()
	return e.mustExec(ctx, t, container, append([]string{"zkCli.sh", "-server", "127.0.0.1:2181"}, args...)...)
}

// zkCreate writes one znode and requires the server to say it created it.
//
// The confirmation is looked for on BOTH streams because zkCli prints
// `Created /path` on **stderr** while `ls` and `get` print their results on
// stdout — measured on 3.8.6, and not a distinction anything documents. A
// create is otherwise indistinguishable from a no-op: zkCli exits 0 either
// way, so a seed that only checked the exit code would migrate an empty tree
// and every assertion after it would be vacuously true.
func (e *itEnv) zkCreate(ctx context.Context, t *testing.T, container, path, value string) {
	t.Helper()
	cmd := []string{"zkCli.sh", "-server", "127.0.0.1:2181", "create", path, value}
	stdout, stderr, code, err := e.env.Exec(ctx, container, cmd)
	require.NoErrorf(t, err, "creating %s", path)
	require.Zerof(t, code, "creating %s: exit %d\n%s\n%s", path, code, stdout, stderr)
	require.Containsf(t, stdout+"\n"+stderr, "Created "+path, "creating %s said nothing about it", path)
}

// zkPaths is an INDEPENDENT reader of the tree, for the same reason
// rabbitRows is: the plan's verify compares the copy against the copy.
//
// zkCli writes its own log lines to stdout mixed in with the results, and some
// of them contain a path ("Client environment:java.class.path=/apache-…"), so
// a result is recognized by its PREFIX and never by containment.
func (e *itEnv) zkPaths(ctx context.Context, t *testing.T, container string) []string {
	t.Helper()
	out := e.zkCli(ctx, t, container, "ls", "-R", "/")
	seen := map[string]bool{}
	var paths []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if !strings.HasPrefix(line, "/") || seen[line] {
			continue
		}
		seen[line] = true
		paths = append(paths, line)
	}
	sort.Strings(paths)
	return paths
}

// waitZk waits for the AdminServer, not the client port: the ZK process opens
// 2181 within seconds while the embedded Jetty lags 10-30 s behind on a cold
// start, and everything the plan reads goes through /commands/.
func (e *itEnv) waitZk(ctx context.Context, t *testing.T, container string) {
	t.Helper()
	e.waitContainer(ctx, t, container, "ZooKeeper", func(ctx context.Context) bool {
		return e.execOK(ctx, container, "curl", "-fsS", "http://127.0.0.1:8080/commands/ruok")
	})
}

// seedZookeeper brings up the FROM image on the namespace's own zookeeper2 bind
// directory and writes the dataset above plus an ephemeral node whose session
// stays open.
//
// It creates the two data directories itself because a temp container runs the
// CONTAINER and nothing around it — ZooKeeper's generated def has an INIT
// CONTAINER whose only job is that mkdir, and without it the server dies with
// "chown: cannot access '/citeck/zookeeper/data'" (measured).
func (e *itEnv) seedZookeeper(ctx context.Context, t *testing.T) []string {
	t.Helper()
	started := time.Now()
	vol := itVolumeOf(deps.Zookeeper, 1)
	require.NoError(t, e.env.CreateVolume(ctx, vol))
	require.NoError(t, e.env.EnsureVolumeDirs(ctx, vol, []string{"data", "datalog"}))
	require.NoError(t, e.env.PullImage(ctx, itZkFrom, func(float64) {}))

	def, err := e.env.GenerateDefFor(deps.Zookeeper, deps.DependencyState{Image: itZkFrom})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itSeedContainer})
	require.NoError(t, err)
	e.waitZk(ctx, t, itSeedContainer)

	for _, node := range itZkDataset {
		e.zkCreate(ctx, t, itSeedContainer, node.path, node.value)
	}
	// An ephemeral needs a session that is still open, and zkCli closes its
	// session cleanly on exit — which DELETES the node. So the session is left
	// running in the background, holding the pipe open, and dies with the
	// container.
	e.mustExec(ctx, t, itSeedContainer, "sh", "-c",
		fmt.Sprintf(`(echo "create -e %s live"; sleep 3600) | zkCli.sh -server 127.0.0.1:2181 > /tmp/eph.log 2>&1 & `+
			`sleep 10; grep -q "Created %s" /tmp/eph.log`, itZkEphemeral, itZkEphemeral))
	paths := e.zkPaths(ctx, t, itSeedContainer)
	require.Contains(t, paths, itZkEphemeral, "the ephemeral node must exist while its session is open")

	require.NoError(t, e.env.StopRemove(ctx, itSeedContainer))
	t.Logf("seed: %s with %d znodes ready in %s", itZkFrom, len(paths), time.Since(started).Round(time.Millisecond))
	return paths
}

// TestIntegration_Zookeeper38To39 migrates a real ZooKeeper tree onto the
// version the generator would otherwise hold a namespace back from.
func TestIntegration_Zookeeper38To39(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.Zookeeper, itZkFrom)
	e.requirePrivilegeOverContainerFiles(ctx, t)
	seeded := e.seedZookeeper(ctx, t)

	src, dst := itVolumeOf(deps.Zookeeper, 1), itVolumeOf(deps.Zookeeper, 2)
	before := e.volumeManifest(ctx, t, src)
	require.NotEmpty(t, before)

	pre := migrate.ZookeeperMigrator{}.Preflight(ctx, e.env, migrate.Path{itZkFrom, itZkTo})
	require.True(t, pre.OK, "preflight problems: %v", pre.Problems)
	t.Logf("preflight: data %d B, required on the volume filesystem %d B, free %d B",
		pre.DataSizeBytes, pre.RequiredVolumeBytes, pre.FreeVolumeBytes)
	// …and it passed on data that has NO snapshot of its own, which is the
	// state E.1 is about rather than an accident of this seed. A clean SIGTERM
	// writes no snapshot — ZooKeeper snapshots right after LOADING, so the
	// durable state after a graceful stop is the transaction log — and the one
	// snapshot on disk here is the EMPTY tree the seed container wrote when it
	// first started, before a single znode existed. A preflight phrased as
	// "confirm there is a snapshot" would refuse every ordinary stopped
	// namespace while its data was perfectly intact.
	snapshots, txnlogs := 0, 0
	for _, entry := range itManifestPaths(before) {
		switch {
		case strings.HasPrefix(entry, "./data/version-2/snapshot."):
			snapshots++
		case strings.HasPrefix(entry, "./datalog/version-2/log."):
			txnlogs++
		}
	}
	assert.Positive(t, txnlogs, "the seeded znodes live in a transaction log: %v", before)
	assert.Equal(t, 1, snapshots,
		"a graceful stop wrote a second snapshot after all; this test no longer covers the txnlog-only case: %v", before)

	plan, journal, err := migrate.ZookeeperMigrator{}.Plan(ctx, e.env, migrate.Path{itZkFrom, itZkTo}, migrate.PlanOptions{})
	require.NoError(t, err)
	timer := newStepTimer()
	started := time.Now()
	runErr := migrate.Run(ctx, e.rt, journal, plan, timer.progress)
	total := time.Since(started)
	steps := timer.report(t)
	require.NoError(t, runErr)
	t.Logf("migration %s → %s took %s", itZkFrom, itZkTo, total.Round(time.Millisecond))
	assert.Equal(t, migrate.CopyStepIDs(), steps)

	st := e.rt.DependencyStates()[deps.Zookeeper]
	assert.Equal(t, itZkTo, st.Image)
	assert.Equal(t, 2, st.Gen())
	assert.Nil(t, e.rt.MigrationJournal())
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, src, last.OldVolume)

	// The invariant, again: nothing in the plan opens the source for writing.
	assert.Equal(t, before, e.volumeManifest(ctx, t, src), "the source volume was written to")

	// What the copy ACTUALLY cost on the destination, against what the
	// preflight demanded for it.
	//
	// ZooKeeper preallocates its transaction log to 64 MiB, so its data volume
	// is the case where "how big is this?" has two very different answers: the
	// blocks it occupies and the bytes it contains. The preflight measures the
	// SOURCE and requires that much (plus a margin) free; if a copy writes a
	// sparse file's holes out as zeros, the destination costs more than the
	// source ever did — and the failure would be an ENOSPC in the middle of a
	// migration on an already-stopped namespace, which is the exact thing the
	// space check exists to prevent. So it is measured rather than reasoned
	// about, and the numbers go in the log whichever way they come out.
	srcKB, srcBytes := e.volumeUsage(ctx, t, src)
	dstKB, dstBytes := e.volumeUsage(ctx, t, dst)
	t.Logf("volume cost: source %d KiB allocated / %d B apparent, copy %d KiB allocated / %d B apparent",
		srcKB, srcBytes, dstKB, dstBytes)
	assert.LessOrEqual(t, dstKB*1024, pre.RequiredVolumeBytes,
		"the copy occupies more than the preflight required for it, so the space check can under-require")

	// The copy plan's EnsureDirs hook, end to end. A temp container runs no
	// init container, so without it the new volume's server dies before it
	// starts — and the copy of a volume that HAS run already has them, which is
	// why only the hook can prove it for the general case.
	afterManifest := e.volumeManifest(ctx, t, dst)
	for _, dir := range []string{"./data", "./datalog"} {
		entry, ok := itManifestEntry(afterManifest, dir)
		assert.Truef(t, ok, "the copy has no %s directory: %v", dir, afterManifest)
		assert.Contains(t, entry, "|directory|")
	}

	// --- what the migrated server actually holds -----------------------------
	def, err := e.env.GenerateDefFor(deps.Zookeeper, deps.DependencyState{Image: itZkTo, VolumeGen: 2})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itCheckContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitZk(ctx, t, itCheckContainer)

	assert.Contains(t, e.mustExec(ctx, t, itCheckContainer,
		"curl", "-fsS", "http://127.0.0.1:8080/commands/srvr"), "3.9.5", "the check container serves the new minor")

	got := e.zkPaths(ctx, t, itCheckContainer)
	for _, node := range itZkDataset {
		assert.Containsf(t, got, node.path, "persistent znode %s did not survive the migration", node.path)
		assert.Equalf(t, node.value, e.zkGet(ctx, t, itCheckContainer, node.path),
			"the value of %s changed", node.path)
	}
	// The tree is the seeded one, give or take the ephemeral: a znode that
	// appeared out of nowhere would mean the copy is not this namespace's data.
	assert.Subset(t, seeded, got, "the migrated tree holds a znode the seed never created: %v", got)
	if !slices.Contains(got, itZkEphemeral) {
		// Its session expired somewhere between the two temp containers, which
		// is exactly the case E.2's split exists for: it is a NOTE, and a
		// migration that failed over it would fail on every real stand.
		t.Logf("the ephemeral %s expired during the migration and did not fail the verify", itZkEphemeral)
		assert.Len(t, got, len(seeded)-1, "nothing but the ephemeral may be missing")
	}
}

// zkGet reads one znode's value.
//
// zkCli prints its own log lines to STDOUT, mixed in with the result, and the
// LAST line of a `get` is a log line ("Exiting JVM with code 0") — so the value
// is the last line that is not one, which is why the log shapes are recognized
// rather than the value being taken off the end.
func (e *itEnv) zkGet(ctx context.Context, t *testing.T, container, path string) string {
	t.Helper()
	out := e.zkCli(ctx, t, container, "get", path)
	value := ""
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimRight(line, " \t\r"); !zkLogLine(line) {
			value = line
		}
	}
	return value
}

// zkLogLine reports whether a line of zkCli's output is its own noise rather
// than a result: a timestamped log record, the connect banner, or the watcher
// notice it prints before every command.
func zkLogLine(line string) bool {
	if strings.TrimSpace(line) == "" || line == "WATCHER::" ||
		strings.HasPrefix(line, "Connecting to ") || strings.HasPrefix(line, "WatchedEvent ") {
		return true
	}
	// "2026-09-09 08:46:03,651 [myid:] - INFO  [main:…" — a record starts with
	// a date, which no value this test writes does.
	if len(line) < 10 {
		return false
	}
	for i, r := range line[:10] {
		if i == 4 || i == 7 {
			if r != '-' {
				return false
			}
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// TestIntegration_CopyRollbackOnBadTarget sabotages the NEW image after the
// copy exists — the state a rollback has to undo — and asserts that the source
// volume, the pin and the namespace are exactly as they were.
//
// It runs on ZooKeeper because the rollback is the shared one
// (RollbackCopyUpgrade) and the dependency only decides how long the seed
// takes.
func TestIntegration_CopyRollbackOnBadTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.Zookeeper, itZkFrom)
	e.requirePrivilegeOverContainerFiles(ctx, t)
	seeded := e.seedZookeeper(ctx, t)

	src, dst := itVolumeOf(deps.Zookeeper, 1), itVolumeOf(deps.Zookeeper, 2)
	before := e.volumeManifest(ctx, t, src)
	// The sabotage image has to be present: a pull failure would fail the run
	// at a step EARLIER than the one this test is about.
	require.NoError(t, e.env.PullImage(ctx, itBadImage, func(float64) {}))

	plan, journal, err := migrate.ZookeeperMigrator{}.Plan(ctx, e.env, migrate.Path{itZkFrom, itZkTo}, migrate.PlanOptions{})
	require.NoError(t, err)

	// What the world looked like at the moment of failure, so the assertions
	// below cannot pass vacuously.
	var (
		copyExisted     bool
		copyManifest    []string
		journaledVolume string
		dstRunning      bool
	)
	sabotaged := false
	for i := range plan.Steps {
		if plan.Steps[i].ID != "start-new" {
			continue
		}
		sabotaged = true
		plan.Steps[i].Run = func(ctx context.Context, j *migrate.Journal, _ migrate.StepProgress) error {
			copyExisted, _ = e.env.VolumeExists(ctx, dst)
			copyManifest = e.volumeManifest(ctx, t, dst)
			journaledVolume = j.CreatedVolume

			def, defErr := e.env.GenerateDefFor(deps.Zookeeper, deps.DependencyState{Image: itZkTo, VolumeGen: 2})
			if defErr != nil {
				return fmt.Errorf("generate the sabotaged target def: %w", defErr)
			}
			// Pulls, starts, and is not a ZooKeeper — so the target container
			// EXISTS and runs when the step fails, which is what the rollback
			// has to clean up. alpine:3 is a PROP, and this closure
			// deliberately short-circuits the real step's readiness wait (which
			// would poll an image that never answers for its full budget), so
			// the wording below is the TEST's and must never be mistaken for
			// the plan's.
			def.Image = itBadImage
			def.Cmd = []string{"sleep", "600"}
			if _, runErr := e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: migrate.DstContainer}); runErr != nil {
				return fmt.Errorf("start the sabotaged target: %w", runErr)
			}
			dstRunning, _ = e.env.ContainerRunning(ctx, migrate.DstContainer)
			return fmt.Errorf("test sabotage: %s runs %s, which is not a ZooKeeper server",
				migrate.DstContainer, itBadImage)
		}
	}
	require.True(t, sabotaged, "the plan has no start-new step to sabotage")

	runErr := migrate.Run(ctx, e.rt, journal, plan, nil)
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "step start-new")
	var finalizeErr *migrate.FinalizeError
	assert.NotErrorAs(t, runErr, &finalizeErr, "the migration failed; it did not commit")

	// The failure was LATE: a real copy of the data existed by then.
	//
	// It is compared by ENTRY and not byte for byte, because by `start-new` the
	// plan has already run the OLD image on the copy — and a ZooKeeper that
	// boots snapshots right after loading and preallocates a fresh transaction
	// log (measured: `snapshot.1f` and `log.20` appear beside the seeded
	// `snapshot.0` and `log.1`). What must be true is that the seed's own files
	// are in there: a copy of an empty volume would have none of them, and the
	// rollback assertions below would then be about nothing.
	assert.True(t, copyExisted, "the copy must exist before the sabotage, or this proves nothing")
	assert.Subset(t, itManifestPaths(copyManifest), itManifestPaths(before),
		"the sabotaged run must have copied the real data, not an empty volume")
	assert.Equal(t, dst, journaledVolume, "the volume is journaled before it is created")
	assert.True(t, dstRunning, "the sabotaged target container must be running when the step fails")

	// --- nothing moved -------------------------------------------------------
	st := e.rt.DependencyStates()[deps.Zookeeper]
	assert.Equal(t, itZkFrom, st.Image, "the pin never moves on failure")
	assert.Equal(t, 1, st.Gen(), "and neither does the generation")
	_, hasPrev := st.Previous()
	assert.False(t, hasPrev, "a failed migration records no rollback target")
	assert.Equal(t, before, e.volumeManifest(ctx, t, src), "the source volume was written to")

	// --- nothing was left behind ---------------------------------------------
	assert.NoDirExists(t, e.volumeDir(dst), "the rollback deletes the volume the plan created")
	for _, c := range []string{migrate.SrcContainer, migrate.DstContainer} {
		running, cErr := e.env.ContainerRunning(ctx, c)
		require.NoError(t, cErr)
		assert.False(t, running, "temp container %s survived the rollback", c)
	}
	assert.Zero(t, e.reloads.get(), "a namespace that was not running is not started by a rollback")

	// --- the verdict ---------------------------------------------------------
	assert.Nil(t, e.rt.MigrationJournal(), "a SUCCESSFUL rollback clears the journal")
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.False(t, last.OK())
	assert.Contains(t, last.Error, "start-new")
	assert.NotContains(t, last.Error, "rollback failed")

	// The source is not merely intact on disk, it still SERVES the same tree.
	def, err := e.env.GenerateDefFor(deps.Zookeeper, deps.DependencyState{Image: itZkFrom})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itCheckContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitZk(ctx, t, itCheckContainer)
	got := e.zkPaths(ctx, t, itCheckContainer)
	for _, node := range itZkDataset {
		assert.Containsf(t, got, node.path, "the failed migration cost the source %s", node.path)
	}
	assert.Subset(t, seeded, got)
}

// --- the temp container's node identity -------------------------------------

// TestIntegration_TempRabbitDoesNotAnswerOnTheNamespaceNetwork is the other
// half of the node-identity decision (ruling 2, candidate A), end to end.
//
// Pinning RabbitMQ's node name needs the name to RESOLVE, and the rejected way
// to get that was a Hostname override — which moby registers as a DNS name on a
// user-defined network, so the temp container would answer to "rabbitmq" there
// and take a share of the real container's traffic, one connection at a time
// and with no error anywhere. /etc/hosts is container-local, and this test is
// what says so about the real thing rather than about the intention.
func TestIntegration_TempRabbitDoesNotAnswerOnTheNamespaceNetwork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.RabbitMQ, itRabbitFrom)

	vol := itVolumeOf(deps.RabbitMQ, 2)
	require.NoError(t, e.env.CreateVolume(ctx, vol))
	require.NoError(t, e.env.PullImage(ctx, itRabbitFrom, func(float64) {}))
	def, err := e.env.GenerateDefFor(deps.RabbitMQ, deps.DependencyState{Image: itRabbitFrom, VolumeGen: 2})
	require.NoError(t, err)
	// Exactly as the plan starts it: the temp name, the node-identity pin, and
	// no hostname override.
	_, err = e.env.RunAppDef(ctx, def, itRabbitTempOpts(migrate.SrcContainer))
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), migrate.SrcContainer)) }()
	e.waitRabbit(ctx, t, migrate.SrcContainer)

	// Inside the temp container the pinned node name resolves — the half
	// without which Erlang refuses to boot ("epmd error for host rabbitmq:
	// nxdomain").
	assert.Contains(t, e.mustExec(ctx, t, migrate.SrcContainer, "getent", "hosts", appdef.AppRabbitmq),
		"127.0.0.1", "the /etc/hosts alias is what makes the pinned node name resolvable")

	// …and from ANOTHER container on the same namespace network it does not.
	probe := appdef.ApplicationDef{
		Name: "dnsprobe", Image: config.UtilsImage(), Cmd: []string{"sleep", "600"},
	}
	_, err = e.env.RunAppDef(ctx, probe, deps.TempContainerOpts{Name: itDNSProbeContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itDNSProbeContainer)) }()

	// The positive control first: without it a broken resolver would make the
	// assertion below pass while proving nothing. Docker registers a
	// container's NAME on a user-defined network, so the temp container IS
	// reachable — under its own name.
	tempName := e.dc.ContainerName(migrate.SrcContainer)
	assert.NotEmpty(t, e.mustExec(ctx, t, itDNSProbeContainer, "getent", "hosts", tempName),
		"the namespace network's DNS must resolve the temp container's own name")

	stdout, _, code, err := e.env.Exec(ctx, itDNSProbeContainer, []string{"getent", "hosts", appdef.AppRabbitmq})
	require.NoError(t, err)
	assert.NotZerof(t, code, "%q resolved to %q on the namespace network: the temp container answers as the app",
		appdef.AppRabbitmq, strings.TrimSpace(stdout))
	assert.Empty(t, strings.TrimSpace(stdout))
}

// --- Qdrant --------------------------------------------------------------

const (
	// itQdrantFrom / itQdrantTo are the pair measured on 2026-09-15: one minor
	// apart, which is exactly as far as Qdrant's storage compatibility reaches.
	itQdrantFrom = "qdrant/qdrant:v1.14.1"
	itQdrantTo   = "qdrant/qdrant:v1.15.5"
	// itRagImage is never pulled or started — the RAG webapp only has to EXIST
	// in the bundle for the generator to emit a qdrant at all.
	itRagImage = "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"
)

// itQdrantCollections is what the seed writes, and each column is read back
// after the migration: two collections with different vector shapes, one of
// them non-empty, plus an alias.
var itQdrantCollections = []struct {
	name   string
	size   int
	points int
}{
	{name: "docs", size: 4, points: 3},
	{name: "notes", size: 8, points: 0},
}

const itQdrantAlias = "documents"

// TestIntegration_Qdrant114To115 migrates a real vector store one minor
// forward, which is the whole reason the dependency is registered: Qdrant
// guarantees its storage across ONE minor, so a stand two releases behind
// cannot simply be handed the new image, and re-building the index means
// re-embedding every document at the provider's price.
//
// It is also the only test that runs the plan's HTTP transport for real. Every
// other dependency's plan shells out to a tool the image ships (rabbitmqctl,
// zkCli.sh, psql); the Qdrant image has neither curl nor wget, so its
// readiness probe and its whole inventory are spoken by bash over /dev/tcp —
// and a unit test with a scripted Exec cannot tell whether that script works.
func TestIntegration_Qdrant114To115(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.Qdrant, itQdrantFrom)
	e.seedQdrant(ctx, t)

	src, dst := itVolumeOf(deps.Qdrant, 1), itVolumeOf(deps.Qdrant, 2)
	before := e.volumeManifest(ctx, t, src)
	require.NotEmpty(t, before)

	pre := migrate.QdrantMigrator{ID: deps.Qdrant}.Preflight(ctx, e.env, migrate.Path{itQdrantFrom, itQdrantTo})
	require.True(t, pre.OK, "preflight problems: %v", pre.Problems)
	t.Logf("preflight: data %d B, required on the volume filesystem %d B, free %d B",
		pre.DataSizeBytes, pre.RequiredVolumeBytes, pre.FreeVolumeBytes)

	plan, journal, err := migrate.QdrantMigrator{ID: deps.Qdrant}.Plan(ctx, e.env, migrate.Path{itQdrantFrom, itQdrantTo}, migrate.PlanOptions{})
	require.NoError(t, err)
	timer := newStepTimer()
	started := time.Now()
	runErr := migrate.Run(ctx, e.rt, journal, plan, timer.progress)
	total := time.Since(started)
	steps := timer.report(t)
	require.NoError(t, runErr)
	t.Logf("migration %s → %s took %s", itQdrantFrom, itQdrantTo, total.Round(time.Millisecond))
	assert.Equal(t, migrate.CopyStepIDs(), steps)

	st := e.rt.DependencyStates()[deps.Qdrant]
	assert.Equal(t, itQdrantTo, st.Image)
	assert.Equal(t, 2, st.Gen())
	assert.Nil(t, e.rt.MigrationJournal())
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, src, last.OldVolume)

	// The invariant: nothing in the plan opens the source for writing.
	assert.Equal(t, before, e.volumeManifest(ctx, t, src), "the source volume was written to")

	// Qdrant preallocates a 32 MiB write-ahead log per collection and writes it
	// as a HOLE: measured, a store with three points is 739 MB apparent against
	// 1.6 MB allocated — a ratio of 455, far past ZooKeeper's. So this is the
	// sharpest test of `tar -S` there is: without it the copy writes those
	// holes out as zeros and costs hundreds of megabytes the preflight never
	// required, and the failure is an ENOSPC in the middle of a migration on an
	// already-stopped namespace.
	srcKB, srcBytes := e.volumeUsage(ctx, t, src)
	dstKB, dstBytes := e.volumeUsage(ctx, t, dst)
	t.Logf("volume cost: source %d KiB allocated / %d B apparent, copy %d KiB allocated / %d B apparent",
		srcKB, srcBytes, dstKB, dstBytes)
	assert.LessOrEqual(t, dstKB*1024, pre.RequiredVolumeBytes,
		"the copy occupies more than the preflight required for it, so the space check can under-require")

	// --- what the migrated server actually holds -----------------------------
	def, err := e.env.GenerateDefFor(deps.Qdrant, deps.DependencyState{Image: itQdrantTo, VolumeGen: 2})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itCheckContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitQdrant(ctx, t, itCheckContainer)

	assert.Contains(t, e.qdrantGET(ctx, t, itCheckContainer, "/"), `"version":"1.15.5"`,
		"the check container serves the new minor")

	for _, c := range itQdrantCollections {
		body := e.qdrantGET(ctx, t, itCheckContainer, "/collections/"+c.name)
		assert.Containsf(t, body, fmt.Sprintf(`"points_count":%d`, c.points),
			"collection %s lost points: %s", c.name, body)
		assert.Containsf(t, body, fmt.Sprintf(`"size":%d`, c.size),
			"collection %s changed shape: %s", c.name, body)
	}
	assert.Contains(t, e.qdrantGET(ctx, t, itCheckContainer, "/aliases"),
		`"alias_name":"`+itQdrantAlias+`"`, "the alias the RAG service addresses by did not survive")
}

// seedQdrant builds a real v1.14.1 store on generation 1 of the volume and
// stops it, which is the state a stopped namespace is in.
func (e *itEnv) seedQdrant(ctx context.Context, t *testing.T) {
	t.Helper()
	started := time.Now()
	vol := itVolumeOf(deps.Qdrant, 1)
	require.NoError(t, e.env.CreateVolume(ctx, vol))
	require.NoError(t, e.env.PullImage(ctx, itQdrantFrom, func(float64) {}))

	def, err := e.env.GenerateDefFor(deps.Qdrant, deps.DependencyState{Image: itQdrantFrom})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itSeedContainer})
	require.NoError(t, err)
	e.waitQdrant(ctx, t, itSeedContainer)

	for _, c := range itQdrantCollections {
		e.qdrantSend(ctx, t, itSeedContainer, "PUT", "/collections/"+c.name,
			fmt.Sprintf(`{"vectors":{"size":%d,"distance":"Cosine"}}`, c.size))
		if c.points == 0 {
			continue
		}
		points := make([]string, 0, c.points)
		for i := 1; i <= c.points; i++ {
			vec := make([]string, c.size)
			for k := range vec {
				vec[k] = fmt.Sprintf("0.%d", (i+k)%9+1)
			}
			points = append(points, fmt.Sprintf(`{"id":%d,"vector":[%s],"payload":{"n":%d}}`,
				i, strings.Join(vec, ","), i))
		}
		e.qdrantSend(ctx, t, itSeedContainer, "PUT", "/collections/"+c.name+"/points?wait=true",
			`{"points":[`+strings.Join(points, ",")+`]}`)
	}
	e.qdrantSend(ctx, t, itSeedContainer, "POST", "/collections/aliases",
		fmt.Sprintf(`{"actions":[{"create_alias":{"collection_name":%q,"alias_name":%q}}]}`,
			itQdrantCollections[0].name, itQdrantAlias))

	require.NoError(t, e.env.StopRemove(ctx, itSeedContainer))
	t.Logf("seed: %s with %d collections ready in %s",
		itQdrantFrom, len(itQdrantCollections), time.Since(started).Round(time.Millisecond))
}

// waitQdrant blocks until the server in container answers /readyz.
func (e *itEnv) waitQdrant(ctx context.Context, t *testing.T, container string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		_, _, code, err := e.env.Exec(ctx, container, itQdrantBashGet("/readyz"))
		if err == nil && code == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("qdrant in %s never became ready (last exit %d, err %v)", container, code, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// qdrantGET reads one endpoint's body. It deliberately uses the SAME bash
// transport the plan does — the image has no curl and no wget — so a test that
// passes is also evidence the transport works.
func (e *itEnv) qdrantGET(ctx context.Context, t *testing.T, container, path string) string {
	t.Helper()
	stdout, stderr, code, err := e.env.Exec(ctx, container, itQdrantBashGet(path))
	require.NoError(t, err)
	require.Zerof(t, code, "GET %s: %s", path, stderr)
	return stdout
}

// qdrantSend performs one write request, for the seed only.
func (e *itEnv) qdrantSend(ctx context.Context, t *testing.T, container, method, path, body string) {
	t.Helper()
	script := `exec 3<>/dev/tcp/127.0.0.1/6333 || exit 1
printf '%s %s HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s' \
  "$1" "$2" "${#3}" "$3" >&3
IFS= read -r status <&3
printf '%s\n' "$status"
case "$status" in *' 200 '*) exit 0 ;; *) exit 1 ;; esac`
	stdout, stderr, code, err := e.env.Exec(ctx, container,
		[]string{"bash", "-c", script, "qdrant-send", method, path, body})
	require.NoError(t, err)
	require.Zerof(t, code, "%s %s: %s %s", method, path, stdout, stderr)
}

// itQdrantBashGet mirrors the plan's own request shape. It is spelled out here
// rather than exported from internal/deps/migrate on purpose: a test that
// reused the production string could not tell a working transport from one
// that is broken in the same way on both sides.
func itQdrantBashGet(path string) []string {
	script := `exec 3<>/dev/tcp/127.0.0.1/6333 || exit 1
printf 'GET %s HTTP/1.0\r\nHost: localhost\r\n\r\n' "$1" >&3
IFS= read -r status <&3 || exit 1
case "$status" in *' 200 '*) ;; *) printf '%s\n' "$status" >&2; exit 1 ;; esac
while IFS= read -r line <&3; do [ "${line%$'\r'}" = "" ] && break; done
cat <&3`
	return []string{"bash", "-c", script, "qdrant-get", path}
}

// TestIntegration_QdrantApiKeyFailsSafely answers the one question a known hole
// raises: the migration cannot read a Qdrant that an operator has put an API
// key on, so what does the FAILURE cost?
//
// The hole is real and deliberately not closed here — neither the generator nor
// the workspace `qdrant:` section sets `QDRANT__SERVICE__API_KEY`, so only a
// hand-written `citeck edit qdrant` produces it — but "the migration fails" is
// only an acceptable answer if failing is cheap. What this proves on real
// containers is that it is: the namespace's own volume still holds every
// collection and serves them, the pin never moved, nothing is left running, and
// the journal is closed so the next start does not go into crash recovery.
//
// It also pins WHERE it fails, which is not where one would guess: an API key
// does NOT close /readyz or /healthz (measured — they answer 200 with a key
// set), so readiness is reached and the run dies at `pre-upgrade`, the step
// that reads the "before" inventory off the old image. That is step 5 of 11,
// before the new image has been started at all.
func TestIntegration_QdrantApiKeyFailsSafely(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.Qdrant, itQdrantFrom)
	e.seedQdrant(ctx, t)

	src, dst := itVolumeOf(deps.Qdrant, 1), itVolumeOf(deps.Qdrant, 2)
	before := e.volumeManifest(ctx, t, src)
	require.NotEmpty(t, before)

	// What an operator's `citeck edit qdrant` would leave behind. It reaches the
	// temp containers the same way it reaches the app's own container: through
	// the stored def patch, which GenerateDefFor applies.
	e.rt.RestoreEditedState(map[string]json.RawMessage{
		appdef.AppQdrant: json.RawMessage(
			`{"environments":{"QDRANT__SERVICE__API_KEY":"an-operator-set-key"}}`),
	}, nil)

	plan, journal, err := migrate.QdrantMigrator{ID: deps.Qdrant}.Plan(ctx, e.env, migrate.Path{itQdrantFrom, itQdrantTo}, migrate.PlanOptions{})
	require.NoError(t, err)
	timer := newStepTimer()
	runErr := migrate.Run(ctx, e.rt, journal, plan, timer.progress)
	steps := timer.report(t)
	require.Error(t, runErr, "an API key the launcher does not send must not read as a healthy migration")

	// Step 5 of 11, and the readiness wait before it PASSED: /readyz is open
	// even with a key set, so the failure is the inventory read and nothing
	// earlier. If this ever moves to start-old, the health endpoints have
	// started requiring the key and the wait is what fails.
	assert.Contains(t, runErr.Error(), "step pre-upgrade")
	assert.Contains(t, steps, "start-old", "readiness was reached; an API key does not close /readyz")
	assert.NotContains(t, steps, "start-new", "the new image is never started")
	var finalizeErr *migrate.FinalizeError
	assert.NotErrorAs(t, runErr, &finalizeErr, "the migration failed; it did not commit")

	// --- the data is intact --------------------------------------------------
	assert.Equal(t, before, e.volumeManifest(ctx, t, src),
		"the namespace's own volume was written to by a migration that failed")
	st := e.rt.DependencyStates()[deps.Qdrant]
	assert.Equal(t, itQdrantFrom, st.Image, "the pin never moves on failure")
	assert.Equal(t, 1, st.Gen(), "and neither does the generation")
	_, hasPrev := st.Previous()
	assert.False(t, hasPrev, "a failed migration records no rollback target")

	// --- nothing was left behind ---------------------------------------------
	assert.NoDirExists(t, e.volumeDir(dst), "the rollback deletes the volume the plan created")
	for _, c := range []string{migrate.SrcContainer, migrate.DstContainer} {
		running, cErr := e.env.ContainerRunning(ctx, c)
		require.NoError(t, cErr)
		assert.False(t, running, "temp container %s survived the rollback", c)
	}
	// An OPEN journal is what makes the next launcher start refuse to start the
	// namespace until it has been cleared, so a cheap failure has to close it.
	assert.Nil(t, e.rt.MigrationJournal(), "a SUCCESSFUL rollback clears the journal")
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.False(t, last.OK())
	assert.NotContains(t, last.Error, "rollback failed")

	// --- and the store still SERVES what it held -----------------------------
	// Not merely intact on disk: the whole point of the question is whether the
	// stand comes back. The check container carries the operator's key too,
	// which is what a real one would.
	def, err := e.env.GenerateDefFor(deps.Qdrant, deps.DependencyState{Image: itQdrantFrom})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itCheckContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitQdrant(ctx, t, itCheckContainer)

	for _, c := range itQdrantCollections {
		body := e.qdrantGETWithKey(ctx, t, itCheckContainer, "/collections/"+c.name, "an-operator-set-key")
		assert.Containsf(t, body, fmt.Sprintf(`"points_count":%d`, c.points),
			"collection %s lost points to a migration that never touched its volume: %s", c.name, body)
	}
}

// qdrantGETWithKey is qdrantGET plus the `api-key` header — the one thing the
// production transport does not send, which is what this test exists about.
func (e *itEnv) qdrantGETWithKey(ctx context.Context, t *testing.T, container, path, key string) string {
	t.Helper()
	script := `exec 3<>/dev/tcp/127.0.0.1/6333 || exit 1
printf 'GET %s HTTP/1.0\r\nHost: localhost\r\napi-key: %s\r\n\r\n' "$1" "$2" >&3
IFS= read -r status <&3 || exit 1
case "$status" in *' 200 '*) ;; *) printf '%s\n' "$status" >&2; exit 1 ;; esac
while IFS= read -r line <&3; do [ "${line%$'\r'}" = "" ] && break; done
cat <&3`
	stdout, stderr, code, err := e.env.Exec(ctx, container,
		[]string{"bash", "-c", script, "qdrant-get-key", path, key})
	require.NoError(t, err)
	require.Zerof(t, code, "GET %s: %s", path, stderr)
	return stdout
}

// itQdrantLadderTop is the third rung of the ladder walk: v1.16.1, one minor
// past itQdrantTo (v1.15.5) — exactly as far as Qdrant's own storage
// compatibility reaches. v1.17.4 does NOT exist on Docker Hub (checked
// 2026-09-15), so it is not a candidate for a fourth rung.
const itQdrantLadderTop = "qdrant/qdrant:v1.16.1"

// itListVolumeDirs lists the data-volume directories on disk whose name has
// prefix, sorted. Server mode only (see itEnv.volumeDir) — the mode these
// tests run in — where a data volume IS a bind-mount directory under
// <base>/volumes, created by exactly one call to Env.CreateVolume. That is
// what makes it the right instrument for "exactly one volume was created":
// the plan's createVolume step makes ONE directory regardless of how many
// rungs the ladder climbs, so a regression that created one copy per rung
// would show up here even if the pin's generation counter did not.
func itListVolumeDirs(t *testing.T, e *itEnv, prefix string) []string {
	t.Helper()
	dir := filepath.Join(e.base, "volumes")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// itCountStep counts how many times id appears in steps — the executed step
// sequence a stepTimer recorded. It is a local copy of countID
// (internal/deps/migrate/copy_upgrade_ladder_test.go), which is unexported in
// that package: the idiom is the same (count occurrences of a step id in an
// ordered list) but there is no shared symbol to import across the package
// boundary, so it is spelled out here rather than reached for.
func itCountStep(steps []string, id string) int {
	n := 0
	for _, s := range steps {
		if s == id {
			n++
		}
	}
	return n
}

// TestIntegration_QdrantLadder114To116 is the multi-hop counterpart of
// TestIntegration_Qdrant114To115: the SAME real Qdrant store, walked through
// a bundle-named ladder of THREE images — v1.14.1 -> v1.15.5 -> v1.16.1 —
// where every adjacent pair is exactly one minor apart, which is what
// Qdrant's own vendor rule requires for each hop to be allowed at all.
//
// What only a real run can prove, and every fake so far has only asserted
// against a scripted double: that the shared copy-upgrade plan raises ONE
// copy of the volume through every rung rather than making one copy per hop
// (the generation counter advances by exactly one, and exactly one new
// volume directory appears on disk), that the data — two collections, their
// point counts, and an alias — survives all the way to the top of the
// ladder, that the SOURCE volume is untouched by any of it, and that both
// temp containers and the journal are cleaned up at the end.
func TestIntegration_QdrantLadder114To116(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnvFor(t, deps.Qdrant, itQdrantFrom)
	e.seedQdrant(ctx, t)

	src, dst := itVolumeOf(deps.Qdrant, 1), itVolumeOf(deps.Qdrant, 2)
	before := e.volumeManifest(ctx, t, src)
	require.NotEmpty(t, before)

	path := migrate.Path{itQdrantFrom, itQdrantTo, itQdrantLadderTop}
	pre := migrate.QdrantMigrator{ID: deps.Qdrant}.Preflight(ctx, e.env, path)
	require.True(t, pre.OK, "preflight problems: %v", pre.Problems)
	t.Logf("preflight: data %d B, required on the volume filesystem %d B, free %d B",
		pre.DataSizeBytes, pre.RequiredVolumeBytes, pre.FreeVolumeBytes)

	plan, journal, err := migrate.QdrantMigrator{ID: deps.Qdrant}.Plan(ctx, e.env, path, migrate.PlanOptions{})
	require.NoError(t, err)

	// --- observe the middle rung while it is actually running ---------------
	//
	// Every assertion below the run depends only on the path's two ENDPOINTS:
	// the final image, the final generation, the volume-dir list, the source
	// manifest, container cleanup, and the final version/collections/alias
	// would all read identically if the plan silently collapsed to a single
	// hop straight from the v1.14.1 data onto the v1.16.1 image — skipping
	// Qdrant's one-minor storage-compatibility guarantee on real production
	// data. This block is the one thing only a REAL run can give: proof that
	// a real container really booted the MIDDLE rung (v1.15.5) on the real
	// copy, and really served the data the bottom rung wrote to it.
	//
	// It WRAPS rather than replaces the plan's own "start-new" step for the
	// FIRST rung (startRung(path.Rungs()[0]) in copy_upgrade.go is the only
	// "start-new" that ever lands v1.15.5): the real Run still executes in
	// full, including the plan's own WaitReady, and only AFTER it succeeds
	// does the wrapper read the container it left running. Nothing about the
	// migration's own behavior is altered — the same override-a-step idiom
	// TestIntegration_CopyRollbackOnBadTarget uses to watch mid-migration
	// state, but wrapping instead of substituting, since this run has to
	// succeed rather than fail.
	var (
		midRunObserved bool
		midVersionBody string
		midDocsBody    string
	)
	startNewSeen := 0
	wrapped := false
	for i := range plan.Steps {
		if plan.Steps[i].ID != "start-new" {
			continue
		}
		startNewSeen++
		if startNewSeen != 1 {
			continue // the SECOND start-new lands v1.16.1, not the rung this proves
		}
		wrapped = true
		realRun := plan.Steps[i].Run
		plan.Steps[i].Run = func(ctx context.Context, j *migrate.Journal, p migrate.StepProgress) error {
			if err := realRun(ctx, j, p); err != nil {
				return err
			}
			midRunObserved = true
			midVersionBody = e.qdrantGET(ctx, t, migrate.DstContainer, "/")
			midDocsBody = e.qdrantGET(ctx, t, migrate.DstContainer, "/collections/"+itQdrantCollections[0].name)
			return nil
		}
	}
	require.True(t, wrapped, "the plan has no start-new step to observe the middle rung on")

	timer := newStepTimer()
	started := time.Now()
	runErr := migrate.Run(ctx, e.rt, journal, plan, timer.progress)
	total := time.Since(started)
	steps := timer.report(t)
	require.NoError(t, runErr)
	t.Logf("ladder migration %s -> %s -> %s took %s (%d step invocations)",
		itQdrantFrom, itQdrantTo, itQdrantLadderTop, total.Round(time.Millisecond), len(steps))

	// The middle rung genuinely ran and genuinely served the data the bottom
	// rung wrote — not v1.14.1 (the bottom), not v1.16.1 (the top skipped
	// ahead to).
	require.True(t, midRunObserved, "the wrapped start-new step never ran — the middle rung was skipped")
	assert.Contains(t, midVersionBody, `"version":"1.15.5"`,
		"the intermediate container must report the MIDDLE rung's version")
	assert.NotContains(t, midVersionBody, `"version":"1.14.1"`, "the intermediate container is not still the bottom rung")
	assert.NotContains(t, midVersionBody, `"version":"1.16.1"`, "the intermediate container has not skipped to the top rung")
	assert.Containsf(t, midDocsBody, fmt.Sprintf(`"points_count":%d`, itQdrantCollections[0].points),
		"the intermediate container must serve the data the bottom rung wrote, not an empty collection: %s", midDocsBody)

	// The shape of the REAL run — not a plan built from the same Path in
	// isolation, but what migrate.Run actually executed — must show TWO
	// rungs climbed, not one. If Path.Rungs() ever regressed to return only
	// the final element while Path.Hops() stayed correct, BuildCopyUpgrade
	// would silently produce the ordinary 11-step single-hop plan
	// (CopyStepIDs()) straight from the v1.14.1 data to the v1.16.1 image,
	// and every endpoint-only assertion in this test would still pass. This
	// is the one that would not: TestASingleHopPlanIsUnchanged and
	// TestAThreeRungPlanClimbsOneCopy (internal/deps/migrate/copy_upgrade_ladder_test.go)
	// already pin this shape against a fake plan; this pins it against the
	// REAL step sequence a real engine.Run just executed.
	assert.Equal(t, 1, itCountStep(steps, "stop-namespace"))
	assert.Equal(t, 1, itCountStep(steps, "pull-image"))
	assert.Equal(t, 1, itCountStep(steps, "create-volume"), "one generation, whatever the ladder's length")
	assert.Equal(t, 1, itCountStep(steps, "copy-volume"), "one copy, whatever the ladder's length")
	assert.Equal(t, 1, itCountStep(steps, "start-old"))
	assert.Equal(t, 1, itCountStep(steps, "stop-old"))
	assert.Equal(t, 2, itCountStep(steps, "start-new"), "one start per rung — TWO rungs were climbed, not one")
	assert.Equal(t, 2, itCountStep(steps, "post-upgrade"), "one per rung")
	assert.Equal(t, 2, itCountStep(steps, "pre-upgrade"), "before every rung, whatever the ladder's length")
	assert.Equal(t, 2, itCountStep(steps, "stop-new"), "one intermediate stop plus the final cleanup")
	assert.Equal(t, 1, itCountStep(steps, "verify"), "the inventory is compared once, at the top")
	assert.Len(t, steps, 15, "stop-namespace, pull-image, create-volume, copy-volume, start-old, "+
		"then pre-upgrade/stop/start-new/post-upgrade once per rung (x2), plus verify and the final stop-new")

	// One copy, one generation, whatever the ladder's length.
	st := e.rt.DependencyStates()[deps.Qdrant]
	assert.Equal(t, itQdrantLadderTop, st.Image)
	assert.Equal(t, 2, st.Gen())
	assert.Nil(t, e.rt.MigrationJournal())
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, src, last.OldVolume)

	// Exactly one volume was created — the source (generation 1) plus one
	// copy (generation 2), never one per rung.
	assert.Equal(t, []string{src, dst}, itListVolumeDirs(t, e, "qdrant"))

	// The invariant the whole design rests on: nothing in the walk opens the
	// source for writing, at any rung.
	assert.Equal(t, before, e.volumeManifest(ctx, t, src), "the source volume was written to")

	// Both temp containers are gone.
	for _, c := range []string{migrate.SrcContainer, migrate.DstContainer} {
		running, cErr := e.env.ContainerRunning(ctx, c)
		require.NoError(t, cErr)
		assert.False(t, running, "temp container %s survived the migration", c)
	}

	// --- what the migrated server holds, on the FINAL image of the ladder ---
	def, err := e.env.GenerateDefFor(deps.Qdrant, deps.DependencyState{Image: itQdrantLadderTop, VolumeGen: 2})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itCheckContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitQdrant(ctx, t, itCheckContainer)

	assert.Contains(t, e.qdrantGET(ctx, t, itCheckContainer, "/"), `"version":"1.16.1"`,
		"the check container serves the top of the ladder")

	for _, c := range itQdrantCollections {
		body := e.qdrantGET(ctx, t, itCheckContainer, "/collections/"+c.name)
		assert.Containsf(t, body, fmt.Sprintf(`"points_count":%d`, c.points),
			"collection %s lost points: %s", c.name, body)
		assert.Containsf(t, body, fmt.Sprintf(`"size":%d`, c.size),
			"collection %s changed shape: %s", c.name, body)
	}
	assert.Contains(t, e.qdrantGET(ctx, t, itCheckContainer, "/aliases"),
		`"alias_name":"`+itQdrantAlias+`"`, "the alias the RAG service addresses by did not survive")
}
