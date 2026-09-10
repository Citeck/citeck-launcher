package migrate

import (
	"context"

	"github.com/citeck/citeck-launcher/internal/deps"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// ZookeeperMigrator moves a namespace's ZooKeeper data onto a new minor by
// upgrading a COPY of the data volume (see copy_upgrade.go).
//
// The pair this ships for — 3.8 ↔ 3.9 — is measured to be a plain container
// swap: the on-disk format has not changed since 3.5, and both versions read
// each other's snapshots and transaction logs. The migrator exists anyway
// because what is IN there is not disposable — ecos/patches/<app>/results/*
// records that a local patch already ran, and eproc's mongo-disabled marker is
// permanent — so a copy that costs under a second on this data buys insurance
// against a future format change, and the copy is what makes "put the old tag
// back" a real rollback rather than a hope.
type ZookeeperMigrator struct{}

const (
	// zkAdminCommands is the AdminServer's command endpoint. The generator
	// already enables it on 8080 and the app's own probes use it; a temp
	// container publishes no ports, so everything here is run from INSIDE the
	// container against the loopback address.
	zkAdminCommands = "http://127.0.0.1:8080/commands/"
	// zkClientServer is the client port zkCli connects to, likewise loopback.
	zkClientServer = "127.0.0.1:2181"
	// zkDataDir/zkDataLogDir are ZOO_DATA_DIR and ZOO_DATA_LOG_DIR relative to
	// the volume root — the two directories the generated def's INIT CONTAINER
	// makes (generator_infra.go mounts the volume at /citeck/zookeeper and
	// points the two env vars at data/ and datalog/ under it). A temp
	// container runs no init container, so the plan makes them itself.
	zkDataDir    = "data"
	zkDataLogDir = "datalog"
)

// SupportsPair asks the registry and words its one refusal.
//
// This launcher's plan can carry any forward pair — it copies a volume and
// starts a container on it — so the only refusal is about the DATA: below 3.5
// it may predate zookeeper.snapshot.trust.empty, where a newer server can take
// an empty snapshot for valid state. A downgrade is refused with an EMPTY
// reason, which the shared preflight has already worded better.
func (ZookeeperMigrator) SupportsPair(from, to deps.Version) (ok bool, problem msg.Message) {
	d, found := deps.Lookup(deps.Zookeeper)
	if !found { // unreachable: ZooKeeper is in the fixed registry
		return false, msg.Message{}
	}
	if d.UpgradeSupport(from, to).Allowed {
		return true, msg.Message{}
	}
	if deps.MovesBackwards(from, to) {
		return false, msg.Message{}
	}
	// The registry owns the floor, so it is asked rather than restated here:
	// a move from this version to ITSELF isolates the "the data is too old"
	// half of its refusal from the direction half.
	if !d.UpgradeSupport(from, from).Allowed {
		return false, zkDataTooOldProblem(from)
	}
	// Unreachable today — the descriptor has no other reason to refuse a
	// forward move — and deliberately not left to the generic fallback: if a
	// future rule appears, this says a vendor refused rather than blaming the
	// launcher's age.
	return false, VendorNoPathProblem(string(deps.Zookeeper), from.String(), to.String())
}

// zkDataTooOldProblem explains the pre-3.5 refusal. It names the property
// rather than the version alone, because the operator's search for
// "zookeeper.snapshot.trust.empty" is what leads to the vendor's own
// explanation — and it must not suggest updating the launcher, which would
// refuse it just the same.
func zkDataTooOldProblem(from deps.Version) msg.Message {
	return msg.New("deps.msg.zk.dataTooOld", "from", from.String())
}

// Preflight runs the shared copy-upgrade checks, and nothing else.
//
// It must NOT assert that a snapshot file exists. A clean SIGTERM writes no
// snapshot at all — ZooKeeper snapshots right after LOADING, so the durable
// state after a graceful stop is the transaction log (measured) — and a check
// phrased as "confirm there is a snapshot" would refuse every ordinary stopped
// namespace while the data is perfectly intact.
func (m ZookeeperMigrator) Preflight(ctx context.Context, env Env, from, to string) PreflightResult {
	res, _, ok := CopyPreflight(ctx, env, deps.Zookeeper, from, to, m.SupportsPair)
	if !ok {
		return res
	}
	res.OK = len(res.Problems) == 0
	return res
}

// Plan builds the copy-upgrade plan for this pair.
func (m ZookeeperMigrator) Plan(ctx context.Context, env Env, from, to string, opts PlanOptions) (*Plan, deps.MigrationJournal, error) {
	pre := m.Preflight(ctx, env, from, to)
	return BuildCopyUpgrade(env, zkCopySpec(), from, to, opts, pre)
}

// zkCopySpec is everything the shared plan does not know about ZooKeeper.
//
// There are no pre- or post-upgrade hooks: 3.8 and 3.9 share one on-disk
// format, so starting the new image on the copy IS the upgrade, and a hook
// here would be a progress line for work that does not exist.
//
// There is no node identity to pin either — the contrast with RabbitMQ is
// worth stating, because the two specs sit side by side: ZooKeeper's data
// path is fixed by ZOO_DATA_DIR and carries no hostname, so a temp container
// running under an override name reads exactly the same directory.
func zkCopySpec() CopySpec {
	return CopySpec{
		ID:         deps.Zookeeper,
		WaitReady:  waitForZookeeper,
		Inventory:  readZkInventory,
		EnsureDirs: []string{zkDataDir, zkDataLogDir},
	}
}

// waitForZookeeper blocks until the AdminServer answers /commands/ruok.
//
// The admin server is the right probe and the client port is not: the ZK
// process opens 2181 within a few seconds while the embedded Jetty lags 10-30 s
// behind on a cold start (which is why the generated app's own startup probe
// polls the same endpoint), and the verify reads its /commands/monitor.
func waitForZookeeper(ctx context.Context, env Env, container string, p StepProgress) error {
	return waitForReady(ctx, env, container, "ZooKeeper", p, func(ctx context.Context) bool {
		return execOK(ctx, env, container, zkAdminCmd("ruok"))
	})
}

// zkAdminCmd is a curl of one AdminServer command. -f makes a non-2xx status
// a non-zero exit (an admin server that is up but refusing is not readiness),
// -sS keeps the output clean but still prints the reason on stderr.
func zkAdminCmd(command string) []string {
	return []string{"curl", "-fsS", zkAdminCommands + command}
}
