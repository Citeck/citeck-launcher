package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

const rabbit43 = "rabbitmq:4.3.5-management"

// execScript answers a command line run inside a container with canned
// output — rabbitmqctl, rabbitmq-diagnostics, curl and zkCli alike. A key is
// "<container>|<substring of the command line>" and the LONGEST matching key
// wins, so a per-vhost or per-endpoint answer can override a default without
// the test spelling every command line out.
type execScript struct {
	out   map[string]string
	fail  map[string]string // same keys → stderr of a non-zero exit
	calls []string
}

func (s *execScript) exec(container, cmdline string) (stdout, stderr string, exitCode int, err error) {
	s.calls = append(s.calls, container+"|"+cmdline)
	if failed, key := s.match(s.fail, container, cmdline); key != "" {
		return "", failed, 69, nil
	}
	out, _ := s.match(s.out, container, cmdline)
	return out, "", 0, nil
}

// match returns the value of the longest key that matches, so that
// "list_queues --vhost /probe" beats "list_queues".
func (s *execScript) match(m map[string]string, container, cmdline string) (value, matched string) {
	for k, v := range m {
		name, sub, ok := strings.Cut(k, "|")
		if !ok || name != container || !strings.Contains(cmdline, sub) {
			continue
		}
		if len(k) > len(matched) {
			value, matched = v, k
		}
	}
	return value, matched
}

// calledIn reports the command lines run in one container, in order.
func (s *execScript) calledIn(container string) []string {
	var out []string
	for _, c := range s.calls {
		if name, line, _ := strings.Cut(c, "|"); name == container {
			out = append(out, line)
		}
	}
	return out
}

// rabbitPlanEnv is a stopped namespace pinned at generation 1 whose broker
// answers every inventory command with nothing — enough for the plan-shape
// tests, which are about WHAT is run and where, not about the data.
func rabbitPlanEnv(t *testing.T, s *execScript) *guardEnv {
	t.Helper()
	env := rabbitEnv(t)
	env.ExecFn = s.exec
	return env
}

func runRabbitPlan(t *testing.T, env *guardEnv, from, to string) error {
	t.Helper()
	plan, j, err := (RabbitMigrator{}).Plan(context.Background(), env, from, to, PlanOptions{})
	require.NoError(t, err)
	return Run(context.Background(), j2store(), j, plan, nil)
}

// The vendor's matrix, not ours: 4.1 → 4.2 is one hop, 4.1 → 4.3 is not, and
// the refusal for a hop the VENDOR forbids must never tell the operator to
// update the launcher — updating it would change nothing.
func TestRabbitSupportsPairFollowsTheVendorMatrix(t *testing.T) {
	v := func(major, minor, patch int) deps.Version {
		return deps.Version{Major: major, Minor: minor, Patch: patch}
	}
	t.Run("a hop the vendor publishes", func(t *testing.T) {
		ok, problem := (RabbitMigrator{}).SupportsPair(v(4, 1, 2), v(4, 2, 9))
		assert.True(t, ok)
		assert.Empty(t, problem)
	})
	t.Run("a hop with an intermediate names it", func(t *testing.T) {
		ok, problem := (RabbitMigrator{}).SupportsPair(v(4, 1, 2), v(4, 3, 5))
		require.False(t, ok)
		assert.Contains(t, problem, "upgrade to 4.2 first")
		assert.Contains(t, problem, "citeck edit rabbitmq")
		assert.NotContains(t, problem, "update the launcher")
	})
	t.Run("an old series is routed through 3.13", func(t *testing.T) {
		ok, problem := (RabbitMigrator{}).SupportsPair(v(3, 12, 12), v(4, 2, 9))
		require.False(t, ok)
		assert.Contains(t, problem, "upgrade to 3.13 first")
	})
	t.Run("a series with no path says so and invents nothing", func(t *testing.T) {
		ok, problem := (RabbitMigrator{}).SupportsPair(v(4, 2, 9), v(9, 0, 0))
		require.False(t, ok)
		assert.Contains(t, problem, "no upgrade path")
		assert.NotContains(t, problem, "first")
	})
	// A downgrade is refused with an EMPTY reason on purpose: the shared
	// version checks word it ("the launcher does not migrate data backwards")
	// and a second sentence here would overwrite the accurate one.
	t.Run("a downgrade is left to the shared checks", func(t *testing.T) {
		ok, problem := (RabbitMigrator{}).SupportsPair(v(4, 2, 9), v(4, 1, 2))
		require.False(t, ok)
		assert.Empty(t, problem)
	})
}

// The node-identity pin, measured (F1/F6). RabbitMQ's node name is
// rabbit@$HOSTNAME and its data path CONTAINS that node name, so a temp
// container under an override name boots a fresh EMPTY node inside the volume
// and reports healthy. The fix needs BOTH halves:
//
//	RABBITMQ_NODENAME=rabbit@rabbitmq alone →
//	    "ERROR: epmd error for host rabbitmq: nxdomain (non-existing domain)"
//	    and the broker never boots;
//	the /etc/hosts alias alone → the node name is still rabbit@depsmig-src.
//
// And the DNS identity must stay the temp container's: moby registers a
// container's hostname as a DNS name on a user-defined network, so the temp
// broker must never be reachable as "rabbitmq" there.
func TestATempRabbitKeepsTheAppsNodeNameAndNotItsDnsName(t *testing.T) {
	s := &execScript{}
	env := rabbitPlanEnv(t, s)
	require.NoError(t, runRabbitPlan(t, env, rabbitFrom, rabbitTo))

	for _, name := range []string{SrcContainer, DstContainer} {
		rec, ok := env.RunOpts(name)
		require.True(t, ok, "%s never ran", name)
		assert.Equal(t, "rabbit@"+appdef.AppRabbitmq, rec.Env["RABBITMQ_NODENAME"],
			"%s must adopt the app's node, not boot a new one inside the copy", name)
		assert.Equal(t, "127.0.0.1", rec.HostAlias[appdef.AppRabbitmq],
			"%s: without the /etc/hosts entry the node name does not resolve and the broker refuses to boot", name)
		assert.NotEqual(t, appdef.AppRabbitmq, name, "the container name stays the temp override")
	}
}

// Ruling 1: `enable_feature_flag all` runs TWICE — once on the old image
// before the upgrade (a required flag left disabled makes the new node refuse
// to start) and once on the new one after it (which is what enables khepri_db
// on a 4.2 target, i.e. what the upgrade was done for). Either failing fails
// the step, and with it the whole migration.
func TestRabbitEnablesFeatureFlagsOnBothImages(t *testing.T) {
	t.Run("once per image", func(t *testing.T) {
		s := &execScript{}
		env := rabbitPlanEnv(t, s)
		require.NoError(t, runRabbitPlan(t, env, rabbitFrom, rabbitTo))
		for _, name := range []string{SrcContainer, DstContainer} {
			var flags int
			for _, line := range s.calledIn(name) {
				if strings.Contains(line, "enable_feature_flag all") {
					flags++
				}
			}
			assert.Equal(t, 1, flags, "%s ran enable_feature_flag all %d time(s)", name, flags)
		}
	})
	for _, tc := range []struct{ name, container string }{
		{"the pre-upgrade half", SrcContainer},
		{"the post-upgrade half", DstContainer},
	} {
		t.Run(tc.name+" fails the migration", func(t *testing.T) {
			s := &execScript{fail: map[string]string{
				tc.container + "|enable_feature_flag all": "Error: unable to enable feature flag",
			}}
			env := rabbitPlanEnv(t, s)
			err := runRabbitPlan(t, env, rabbitFrom, rabbitTo)
			require.ErrorContains(t, err, "unable to enable feature flag")
			assert.NotContains(t, env.Volumes, rabbitGen2, "the rollback removed the copy")
		})
	}
}

// Readiness is `check_running` plus `check_port_connectivity`, never a port
// probe: `ping` (and an open 5672) succeeds long before the node has booted,
// so a plan that trusted it would run its first command against a broker that
// is not there yet.
func TestRabbitReadinessIsTwoDiagnosticsAndNeverAPortProbe(t *testing.T) {
	s := &execScript{}
	env := rabbitPlanEnv(t, s)
	require.NoError(t, runRabbitPlan(t, env, rabbitFrom, rabbitTo))
	first := s.calledIn(SrcContainer)
	// [0] is the cookie test that must precede any Erlang VM — see
	// TestRabbitReadinessWaitsForTheCookieBeforeItStartsAnErlangVM.
	require.GreaterOrEqual(t, len(first), 3)
	assert.Contains(t, first[1], "check_running")
	assert.Contains(t, first[2], "check_port_connectivity")
	joined := strings.Join(s.calls, "\n")
	assert.NotContains(t, joined, "ping", "ping answers before the node has booted")
}

// A node that never answers is a failed step, not a plan that carries on
// against a broker that is not there.
func TestRabbitReadinessGivesUpAndFailsTheStep(t *testing.T) {
	s := &execScript{fail: map[string]string{
		SrcContainer + "|check_running": "Error: RabbitMQ on node rabbit@rabbitmq is not running",
	}}
	env := rabbitPlanEnv(t, s)
	spec := rabbitCopySpec(deps.Version{Major: 4, Minor: 2, Patch: 9})
	_, err := env.RunAppDef(context.Background(),
		appdef.ApplicationDef{Name: appdef.AppRabbitmq, Image: rabbitFrom},
		deps.TempContainerOpts{Name: SrcContainer})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the wait must honor the context rather than poll for minutes
	err = spec.WaitReady(ctx, env, SrcContainer, func(float64, string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), SrcContainer)
}

// The Khepri transition is irreversible ON THE DATA IT IS APPLIED TO, and it
// is applied to the COPY — so it is a WARNING that names the volume the
// operator still has, not a checkbox and not a refusal.
func TestRabbitPreflightNamesTheKhepriTransitionForA42Target(t *testing.T) {
	env := rabbitPlanEnv(t, &execScript{})
	res := (RabbitMigrator{}).Preflight(context.Background(), env, rabbitFrom, rabbitTo)
	require.True(t, res.OK, res.Problems)
	joined := strings.Join(res.Warnings, "\n")
	assert.Contains(t, joined, "Khepri")
	assert.Contains(t, joined, rabbitGen1, "the warning names the volume that is left untouched")
	assert.Contains(t, joined, rabbitFrom, "and the image the namespace can be put back on")

	t.Run("and says nothing for a target below 4.2", func(t *testing.T) {
		env := rabbitPlanEnv(t, &execScript{})
		env.States[deps.RabbitMQ] = deps.DependencyState{Image: "rabbitmq:4.0.9-management"}
		res := (RabbitMigrator{}).Preflight(context.Background(), env,
			"rabbitmq:4.0.9-management", "rabbitmq:4.1.2-management")
		require.True(t, res.OK, res.Problems)
		assert.NotContains(t, strings.Join(res.Warnings, "\n"), "Khepri")
	})
}

// 4.3 removes the deprecated features, so a stand still using one would come
// up broken. The check needs a RUNNING broker, which the preflight may not
// have — so it asks the namespace's own container when that is running, and
// otherwise says it will be checked on the copy.
func TestRabbitPreflightChecksDeprecatedFeaturesForA43Target(t *testing.T) {
	t.Run("a running broker in use is a problem", func(t *testing.T) {
		s := &execScript{fail: map[string]string{
			appdef.AppRabbitmq + "|check_if_any_deprecated_features_are_used": "Deprecated features in use: classic_queue_mirroring",
		}}
		env := rabbitPlanEnv(t, s)
		env.Containers[appdef.AppRabbitmq] = appdef.ApplicationDef{Name: appdef.AppRabbitmq, Image: rabbitTo}
		env.States[deps.RabbitMQ] = deps.DependencyState{Image: rabbitTo}
		res := (RabbitMigrator{}).Preflight(context.Background(), env, rabbitTo, rabbit43)
		require.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "classic_queue_mirroring")
	})
	t.Run("a stopped namespace is a warning, not a refusal", func(t *testing.T) {
		env := rabbitPlanEnv(t, &execScript{})
		env.States[deps.RabbitMQ] = deps.DependencyState{Image: rabbitTo}
		res := (RabbitMigrator{}).Preflight(context.Background(), env, rabbitTo, rabbit43)
		require.True(t, res.OK, res.Problems)
		assert.Contains(t, strings.Join(res.Warnings, "\n"), "deprecated features")
	})
	t.Run("a target below 4.3 is not asked at all", func(t *testing.T) {
		s := &execScript{}
		env := rabbitPlanEnv(t, s)
		env.Containers[appdef.AppRabbitmq] = appdef.ApplicationDef{Name: appdef.AppRabbitmq, Image: rabbitTo}
		res := (RabbitMigrator{}).Preflight(context.Background(), env, rabbitFrom, rabbitTo)
		require.True(t, res.OK, res.Problems)
		assert.NotContains(t, strings.Join(s.calls, "\n"), "deprecated")
	})
}

// Whatever the preflight could not check on a stopped namespace is checked on
// the COPY before the upgrade, where a refusal costs a rollback of a copy and
// nothing else. It runs BEFORE the feature flags are enabled: a failure then
// has changed nothing at all.
func TestRabbitPreUpgradeChecksDeprecatedFeaturesOnTheCopy(t *testing.T) {
	s := &execScript{fail: map[string]string{
		SrcContainer + "|check_if_any_deprecated_features_are_used": "Deprecated features in use: ram_node_type",
	}}
	env := rabbitPlanEnv(t, s)
	env.States[deps.RabbitMQ] = deps.DependencyState{Image: rabbitTo}
	env.Volumes[deps.VolumeName(rabbitDescriptor(), 1)] = map[string]string{"x": "y"}
	err := runRabbitPlan(t, env, rabbitTo, rabbit43)
	require.ErrorContains(t, err, "ram_node_type")
	assert.NotContains(t, strings.Join(s.calledIn(SrcContainer), "\n"), "enable_feature_flag",
		"the check runs BEFORE anything is enabled, so a refusal has changed nothing at all")

	t.Run("and precedes the feature flags when it passes", func(t *testing.T) {
		s := &execScript{}
		env := rabbitPlanEnv(t, s)
		env.States[deps.RabbitMQ] = deps.DependencyState{Image: rabbitTo}
		require.NoError(t, runRabbitPlan(t, env, rabbitTo, rabbit43))
		src := strings.Join(s.calledIn(SrcContainer), "\n")
		require.Contains(t, src, "deprecated")
		assert.Less(t, strings.Index(src, "deprecated"), strings.Index(src, "enable_feature_flag"))
	})
}

// A 4.2 target never asks about deprecated features on the copy either: the
// features are still there, and a diagnostic that cannot fail the migration is
// a diagnostic nobody reads.
func TestRabbitPreUpgradeSkipsTheDeprecatedCheckBelow43(t *testing.T) {
	s := &execScript{}
	env := rabbitPlanEnv(t, s)
	require.NoError(t, runRabbitPlan(t, env, rabbitFrom, rabbitTo))
	assert.NotContains(t, strings.Join(s.calls, "\n"), "deprecated")
}

// A root exec into a RabbitMQ that has not yet written its .erlang.cookie
// KILLS the broker, and the readiness wait is the first thing that runs
// against a temp container.
//
// Measured (rootless Docker 29.3.1, rabbitmq 4.1.2-management and
// 4.2.9-management alike, fresh volume, launcher shape): the entrypoint's
// `find ! -user rabbitmq -exec chown` runs once, before the server; the
// server's own Erlang VM creates $HOME/.erlang.cookie mode 0400 owned by 999
// about 1.3 s later. A `rabbitmq-diagnostics` exec landing in that gap runs as
// root with the same HOME, wins the race, and creates the cookie as ROOT —
// after the chown that would have repaired it. The server then dies with
// `Error when reading /var/lib/rabbitmq/.erlang.cookie: eacces` /
// `Kernel pid terminated`. 7/7 dead at an exec launched <= 0.45 s.
//
// startTemp calls WaitReady the instant RunAppDef returns, i.e. ~0.2 s after
// the container started — inside the deterministic half of that window — and
// the copy has no cookie whenever the SOURCE volume has none. Nothing upstream
// excludes that: the preflight admits a source volume that merely EXISTS, and
// the pin seeding never looks inside a RabbitMQ volume at all. So the first
// command must be one that starts no Erlang VM.
func TestRabbitReadinessWaitsForTheCookieBeforeItStartsAnErlangVM(t *testing.T) {
	s := &execScript{}
	env := rabbitPlanEnv(t, s)
	require.NoError(t, runRabbitPlan(t, env, rabbitFrom, rabbitTo))
	for _, container := range []string{SrcContainer, DstContainer} {
		calls := s.calledIn(container)
		require.NotEmpty(t, calls)
		assert.Contains(t, calls[0], rabbitCookiePath,
			"%s: the cookie test must come before any Erlang tool", container)
		assert.NotContains(t, calls[0], "rabbitmq-",
			"%s: the first command must start no Erlang VM", container)
	}
}

// The gate is a GATE, not an ordering: while the cookie is missing the wait
// must run no Erlang tool at all, however many times it polls.
func TestRabbitReadinessRunsNoErlangToolWhileTheCookieIsMissing(t *testing.T) {
	s := &execScript{fail: map[string]string{SrcContainer + "|" + rabbitCookiePath: ""}}
	env := rabbitPlanEnv(t, s)
	_, err := env.RunAppDef(context.Background(),
		appdef.ApplicationDef{Name: appdef.AppRabbitmq, Image: rabbitFrom},
		deps.TempContainerOpts{Name: SrcContainer})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // one poll iteration, then the wait honors the context
	err = rabbitCopySpec(deps.Version{Major: 4, Minor: 2, Patch: 9}).
		WaitReady(ctx, env, SrcContainer, func(float64, string) {})
	require.Error(t, err)
	joined := strings.Join(s.calledIn(SrcContainer), "\n")
	require.NotEmpty(t, joined, "the wait must at least have asked about the cookie")
	assert.NotContains(t, joined, "rabbitmq-diagnostics")
	assert.NotContains(t, joined, "rabbitmqctl")
}
