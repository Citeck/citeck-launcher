package migrate

import (
	"context"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// RabbitMigrator moves a namespace's RabbitMQ data onto a new release series
// by upgrading a COPY of the data volume (see copy_upgrade.go).
//
// It is a copy upgrade and not a dump-and-restore because RabbitMQ has no
// logical dump that reproduces a node: export_definitions carries the topology
// but no MESSAGES, and the vendor's only supported restore is a byte copy of a
// STOPPED node's data directory onto a node with exactly the same node name.
// So the plan copies the volume, runs the old image on the copy to enable the
// feature flags the new node needs, then the new image to complete the
// upgrade — and the namespace's own volume is only ever read.
type RabbitMigrator struct{}

const (
	// rabbitNodeName is the node the namespace's own container runs as.
	// RabbitMQ derives it from the container hostname, which for the app's own
	// container is the app name, and the data path contains it
	// (mnesia/rabbit@rabbitmq). A temp container must therefore be told this
	// name explicitly or it boots a fresh, EMPTY node inside the copy and
	// reports healthy — measured on a seeded volume, where `list_users` then
	// showed `guest` alone and a second `rabbit@depsmig-src` directory
	// appeared beside the real one.
	rabbitNodeName = "rabbit@" + appdef.AppRabbitmq

	// rabbitCookiePath is $HOME/.erlang.cookie inside the official image
	// (HOME=/var/lib/rabbitmq, which is also the data volume). Every Erlang
	// tool reads it, and CREATES it when it is absent — see waitForRabbit.
	rabbitCookiePath = "/var/lib/rabbitmq/.erlang.cookie"

	// deprecatedRemovedMajor/Minor is the series that REMOVES the features
	// 4.2 only deprecates, so a stand still using one comes up broken after
	// the upgrade rather than during it.
	deprecatedRemovedMajor, deprecatedRemovedMinor = 4, 3
)

// SupportsPair asks the registry's vendor matrix and words its refusal.
//
// The launcher's own plan can carry ANY forward pair — it copies a volume and
// runs two containers on it — so everything this refuses is refused by
// RabbitMQ itself, and the message must say so: "update the launcher" would
// send the operator after a fix that does not exist. A downgrade is refused
// with an EMPTY reason, which is the contract the shared preflight relies on:
// it has already worded that case accurately, and a second sentence here would
// overwrite it.
func (RabbitMigrator) SupportsPair(from, to deps.Version) (ok bool, problem string) {
	d, found := deps.Lookup(deps.RabbitMQ)
	if !found { // unreachable: RabbitMQ is in the fixed registry
		return false, ""
	}
	if support := d.UpgradeSupport(from, to); support.Allowed {
		return true, ""
	} else if support.Via != "" {
		return false, VendorPathProblem(string(deps.RabbitMQ), from.String(), to.String(), support.Via)
	}
	if deps.MovesBackwards(from, to) {
		return false, ""
	}
	return false, VendorNoPathProblem(string(deps.RabbitMQ), from.String(), to.String())
}

// Preflight runs the shared copy-upgrade checks and then RabbitMQ's own
// concern: the deprecated features a 4.3 target removes. It never mutates.
func (m RabbitMigrator) Preflight(ctx context.Context, env Env, from, to string) PreflightResult {
	res, _, ok := CopyPreflight(ctx, env, deps.RabbitMQ, from, to, m.SupportsPair)
	if !ok {
		return res
	}
	d, _ := deps.Lookup(deps.RabbitMQ) // registered: CopyPreflight just looked it up
	toV, _ := d.ParseVersion(to)       // parseable: CopyPreflight refuses a tag it cannot read
	res.checkRabbitDeprecatedFeatures(ctx, env, toV)
	res.OK = len(res.Problems) == 0
	return res
}

// Plan builds the copy-upgrade plan for this pair.
func (m RabbitMigrator) Plan(ctx context.Context, env Env, from, to string, opts PlanOptions) (*Plan, deps.MigrationJournal, error) {
	pre := m.Preflight(ctx, env, from, to)
	d, found := deps.Lookup(deps.RabbitMQ)
	if !found { // unreachable: RabbitMQ is in the fixed registry
		return nil, deps.MigrationJournal{}, fmt.Errorf("%s is not a registered dependency", deps.RabbitMQ)
	}
	toV, _ := d.ParseVersion(to)
	return BuildCopyUpgrade(env, rabbitCopySpec(toV), from, to, opts, pre)
}

// rabbitCopySpec is everything the shared plan does not know about RabbitMQ.
//
// toV decides only whether the deprecated-features diagnostic runs on the
// copy: it is a question about the TARGET series (4.3 removes what 4.2 merely
// deprecates), and running it for a 4.2 target would refuse a migration over
// features that still work.
func rabbitCopySpec(toV deps.Version) CopySpec {
	return CopySpec{
		ID: deps.RabbitMQ,
		// Both halves of the node-identity pin, and both are mandatory. The
		// environment names the node (and with it the data directory the node
		// reads); the /etc/hosts entry resolves that name's host part, without
		// which Erlang refuses to boot with "epmd error for host rabbitmq:
		// nxdomain (non-existing domain)" — measured. The container's own
		// hostname stays the temp override, so it never answers as the app on
		// the namespace network.
		TempEnv:   map[string]string{"RABBITMQ_NODENAME": rabbitNodeName},
		HostAlias: map[string]string{appdef.AppRabbitmq: "127.0.0.1"},
		WaitReady: waitForRabbit,
		PreUpgrade: func(ctx context.Context, env Env, container string, _ StepProgress) error {
			return rabbitPreUpgrade(ctx, env, container, toV)
		},
		PostUpgrade: func(ctx context.Context, env Env, container string, _ StepProgress) error {
			// This is the step the upgrade was done for on a 4.2+ target: on
			// the new image `all` includes khepri_db, so this is where the
			// Mnesia → Khepri migration happens — on the copy, with the
			// original volume untouched beside it.
			return enableAllFeatureFlags(ctx, env, container)
		},
		Inventory: readRabbitInventory,
	}
}

// rabbitPreUpgrade runs on the OLD image, on the copy.
//
// The deprecated-features diagnostic runs BEFORE the feature flags are
// enabled: a refusal then has changed nothing at all, while the same refusal
// afterwards would leave a copy that has already been written to (harmless,
// but it makes the failure look like it did something).
func rabbitPreUpgrade(ctx context.Context, env Env, container string, toV deps.Version) error {
	if atLeastSeries(toV, deprecatedRemovedMajor, deprecatedRemovedMinor) {
		if err := rabbitDeprecatedFeaturesInUse(ctx, env, container, toV); err != nil {
			return err
		}
	}
	// Ruling 1: a REQUIRED feature flag left disabled makes the new node
	// refuse to start, so the flags of the old series are enabled first. On
	// 4.1 this deliberately does NOT enable khepri_db — it is experimental
	// there and `all` skips it (measured).
	return enableAllFeatureFlags(ctx, env, container)
}

// enableAllFeatureFlags runs `rabbitmqctl enable_feature_flag all` and fails
// the step when it does not exit 0. rabbitmqctl inherits RABBITMQ_NODENAME
// from the container's environment, so it always talks to the node the plan
// pinned rather than to one derived from the temp container's hostname.
func enableAllFeatureFlags(ctx context.Context, env Env, container string) error {
	stdout, stderr, code, err := env.Exec(ctx, container,
		[]string{"rabbitmqctl", "-q", "enable_feature_flag", "all"})
	if err != nil {
		return fmt.Errorf("enable_feature_flag all in %s: %w", container, err)
	}
	if code != 0 {
		return fmt.Errorf("enable_feature_flag all in %s: exit %d: %s",
			container, code, tail(stderr+"\n"+stdout))
	}
	return nil
}

// rabbitDeprecatedFeaturesInUse asks the broker in container whether it still
// uses a feature the target series removes. A non-zero exit means it does, and
// the output names which.
func rabbitDeprecatedFeaturesInUse(ctx context.Context, env Env, container string, toV deps.Version) error {
	stdout, stderr, code, err := env.Exec(ctx, container,
		[]string{"rabbitmq-diagnostics", "-q", "check_if_any_deprecated_features_are_used"})
	if err != nil {
		return fmt.Errorf("check_if_any_deprecated_features_are_used in %s: %w", container, err)
	}
	if code != 0 {
		return fmt.Errorf(
			"RabbitMQ still uses deprecated features, and %s removes them: %s",
			toV.String(), tail(stderr+"\n"+stdout))
	}
	return nil
}

// checkRabbitDeprecatedFeatures is the preflight half of the same question.
//
// The diagnostic needs a RUNNING broker and the preflight has not started
// anything, so it asks the namespace's OWN container when that is running —
// a read-only diagnostic — and otherwise says the check is deferred to the
// copy, where a refusal costs a rollback of a copy and nothing else. Refusing
// outright would make an upgrade impossible on a stopped namespace, which is
// most of them.
func (res *PreflightResult) checkRabbitDeprecatedFeatures(ctx context.Context, env Env, toV deps.Version) {
	if !atLeastSeries(toV, deprecatedRemovedMajor, deprecatedRemovedMinor) {
		return
	}
	running, err := env.ContainerRunning(ctx, appdef.AppRabbitmq)
	if err != nil || !running {
		reason := "the namespace is not running"
		if err != nil {
			reason = err.Error()
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"cannot check yet whether deprecated features are in use (%s); "+
				"it is checked on the copy before the upgrade, and a failure there costs only the copy", reason))
		return
	}
	if derr := rabbitDeprecatedFeaturesInUse(ctx, env, appdef.AppRabbitmq, toV); derr != nil {
		res.Problems = append(res.Problems, derr.Error())
	}
}

// waitForRabbit blocks until the node in container is really up.
//
// Two diagnostics, never a port probe: `ping` succeeds (and 5672 is open) long
// before the node has booted, so a plan that trusted either would run its
// first command against a broker that is not there. check_running is the node
// itself; check_port_connectivity proves the listeners are accepting, which is
// what the inventory and rabbitmqctl need.
//
// The cookie test in front of them starts no Erlang VM, and that is the whole
// point. A `docker exec` lands as ROOT (the image declares no USER; it drops
// privileges inside its own entrypoint) with HOME=/var/lib/rabbitmq, and every
// Erlang tool CREATES $HOME/.erlang.cookie mode 0400 when it is absent. The
// entrypoint's repair — `find /var/lib/rabbitmq ! -user rabbitmq -exec chown
// rabbitmq {} +` — runs ONCE, before the server, so a cookie that appears
// after it is never chowned: the broker, which runs as 999, then dies with
// `Error when reading /var/lib/rabbitmq/.erlang.cookie: eacces` and
// `Kernel pid terminated`. Measured on 4.1.2-management and 4.2.9-management
// alike: the broker writes that cookie ~1.3 s after container start on an idle
// host, and an exec inside that gap kills it deterministically (7/7 at <= 0.45 s).
// startTemp calls this the instant RunAppDef returns, ~0.2 s in — and the copy
// has no cookie whenever the SOURCE volume has none, which nothing upstream
// excludes (the preflight admits a source volume that merely exists, and the
// pin seeding never reads inside a RabbitMQ volume).
//
// It is a gate rather than a delay because the question is not "has enough
// time passed" but "does the file exist": waiting is unbounded on a starved
// host, where the same cookie has been measured appearing 7.3 s in. A broker
// that never writes one is not a hang either — waitForReady fails the step as
// soon as the container is no longer running.
func waitForRabbit(ctx context.Context, env Env, container string, p StepProgress) error {
	return waitForReady(ctx, env, container, "RabbitMQ", p, func(ctx context.Context) bool {
		if !execOK(ctx, env, container, []string{"test", "-e", rabbitCookiePath}) {
			return false
		}
		if !execOK(ctx, env, container, []string{"rabbitmq-diagnostics", "-q", "check_running"}) {
			return false
		}
		return execOK(ctx, env, container, []string{"rabbitmq-diagnostics", "-q", "check_port_connectivity"})
	})
}

// atLeastSeries reports whether v is in the given release series or a later
// one. Patch versions never decide any of these questions: RabbitMQ documents
// its upgrade rules, its feature flags and its removals per SERIES.
func atLeastSeries(v deps.Version, major, minor int) bool {
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}
