package migrate

import (
	"context"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// Migrator is one dependency's migration plan, as the daemon sees it.
//
// It lives here rather than in the daemon because three callers need it and
// one of them has no Env to give: the dependency LIST answers the status of
// every dependency on every request, and asking "can this launcher move that
// pair?" must not require building an environment. That is also why
// SupportsPair takes versions and not an Env — it is a statement about this
// release, decided before anything is probed.
type Migrator interface {
	// SupportsPair answers whether THIS LAUNCHER's plan can move the data from
	// one version to the other, and — when it cannot — the operator-facing
	// reason.
	//
	// An empty problem with ok == false means "refused for a reason the
	// PREFLIGHT words better": a downgrade, an unparsable tag. That is not a
	// nicety. It is what keeps "the launcher does not migrate data backwards"
	// reachable instead of being overwritten by "update the launcher", which
	// is the wrong advice for a policy that will never change.
	SupportsPair(from, to deps.Version) (ok bool, problem string)
	// Preflight measures and reports everything that would refuse or endanger
	// the migration, without touching the data.
	Preflight(ctx context.Context, env Env, from, to string) PreflightResult
	// Plan runs the preflight again and, if it passes, builds the executable
	// plan plus the journal that will be written ahead of its first step.
	Plan(ctx context.Context, env Env, from, to string, opts PlanOptions) (*Plan, deps.MigrationJournal, error)
}

// migrators is the single wiring point between a registry descriptor that
// claims Migratable() and the plan that makes the claim true. Nothing else may
// hold a second copy of this mapping: a descriptor whose Migratable() and
// whose plan disagree is a "the launcher offered an upgrade it cannot perform"
// bug that only surfaces after the operator has pressed the button, which is
// why TestEveryMigratableDependencyHasAMigratorAndARollback pins the two
// against each other.
var migrators = map[deps.ID]Migrator{
	deps.Postgres:  PostgresMigrator{},
	deps.RabbitMQ:  RabbitMigrator{},
	deps.Zookeeper: ZookeeperMigrator{},
}

// rollbacks is the same wiring for a journal found at boot.
//
// It is a SEPARATE table on purpose. A rollback has to be reachable for a
// journal whose migrator this launcher would refuse to build a plan for —
// a journal is written by whichever launcher started the migration, and the
// one that finds it may be an older or a newer build. Folding the undo into
// Migrator would tie "can I start this" to "can I finish undoing that", which
// are not the same question and must not fail together.
var rollbacks = map[deps.ID]func(context.Context, Env, *deps.MigrationJournal) error{
	deps.Postgres:  RollbackPostgres,
	deps.RabbitMQ:  RollbackCopyUpgrade,
	deps.Zookeeper: RollbackCopyUpgrade,
}

// MigratorFor answers the migrator for a dependency. ok == false means this
// launcher ships no plan for it — which for a descriptor that claims
// Migratable() is a wiring bug, not a user error, and the caller reports it as
// an internal failure rather than as "update the launcher".
func MigratorFor(id deps.ID) (Migrator, bool) {
	m, ok := migrators[id]
	return m, ok
}

// RollbackFor answers the undo for an interrupted migration of a dependency.
// ok == false means this launcher cannot undo it, and the caller must then
// leave the journal alone: it is the only record of what was left behind, and
// its id is what tells the operator which launcher version could finish the
// job.
func RollbackFor(id deps.ID) (func(context.Context, Env, *deps.MigrationJournal) error, bool) {
	fn, ok := rollbacks[id]
	return fn, ok
}
