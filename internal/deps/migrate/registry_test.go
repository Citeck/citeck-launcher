package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// TestEveryMigratableDependencyHasAMigratorAndARollback closes the wiring-bug
// class this feature is most exposed to: a descriptor says Migratable() and
// nothing is wired behind it.
//
// It matters because Migratable() is not an internal detail — it is what the
// dashboard banner and the dependency list split on, so a descriptor that
// claims it without a plan advertises "the launcher can migrate this", and the
// operator learns otherwise only after pressing the button, where the route
// can do nothing but report an internal error. The rollback half is worse
// still: it is reached at BOOT, with an open journal and leftovers on the
// host, and a missing entry there means the namespace is refused every start
// until a human intervenes.
func TestEveryMigratableDependencyHasAMigratorAndARollback(t *testing.T) {
	migratable := 0
	for _, d := range deps.All() {
		if !d.Migratable() {
			_, hasM := MigratorFor(d.ID())
			assert.False(t, hasM, "%s does not claim Migratable() but a migrator is wired for it", d.ID())
			continue
		}
		migratable++
		_, hasM := MigratorFor(d.ID())
		assert.True(t, hasM, "%s claims Migratable() but no migrator is wired for it", d.ID())
		_, hasR := RollbackFor(d.ID())
		assert.True(t, hasR, "%s claims Migratable() but no rollback is wired for it", d.ID())
	}
	assert.Equal(t, 4, migratable,
		"postgres, rabbitmq, zookeeper and qdrant are the four built-ins this release migrates — "+
			"adding or removing one is a deliberate act, not a side effect (a workspace-declared "+
			"cluster is answered by kind: TestADeclaredDependencyGetsTheMigratorAndTheRollbackOfItsKind)")
}

// A dependency that is not registered at all has no plan and no undo. The two
// answers must be the same shape as "registered but not migratable", because
// the caller of RollbackFor cannot tell an unknown id from an un-undoable one
// and must leave the journal alone either way.
func TestAnUnknownDependencyHasNeitherAMigratorNorARollback(t *testing.T) {
	_, ok := MigratorFor(deps.ID("redis"))
	assert.False(t, ok)
	_, ok = RollbackFor(deps.ID("redis"))
	assert.False(t, ok)
}

// A cluster or store a WORKSPACE declares is the same kind of thing as the
// built-in one — the registry advertises it as Migratable() because its
// descriptor is the PostgreSQL / Qdrant descriptor — so it must get the same
// plan and the same undo, keyed to ITS id. Without this `citeck deps upgrade`
// on a declared cluster was offered and then failed as an internal error.
func TestADeclaredDependencyGetsTheMigratorAndTheRollbackOfItsKind(t *testing.T) {
	deps.SetExtraDependencies([]deps.Descriptor{
		deps.NewPostgresDescriptor("billing-postgres", "billing-postgres", "billing_pg"),
		deps.NewQdrantDescriptor("search-qdrant", "search-qdrant", "search_qdrant"),
	})
	defer deps.ResetExtraDependencies()

	m, ok := MigratorFor("billing-postgres")
	require.True(t, ok)
	assert.Equal(t, PostgresMigrator{ID: "billing-postgres"}, m, "the plan must address THIS cluster")
	_, ok = RollbackFor("billing-postgres")
	assert.True(t, ok)

	m, ok = MigratorFor("search-qdrant")
	require.True(t, ok)
	assert.Equal(t, QdrantMigrator{ID: "search-qdrant"}, m, "the plan must address THIS store")
	_, ok = RollbackFor("search-qdrant")
	assert.True(t, ok)

	for _, d := range deps.All() {
		if d.Migratable() {
			_, hasM := MigratorFor(d.ID())
			_, hasR := RollbackFor(d.ID())
			assert.True(t, hasM && hasR, "%s claims Migratable() but has no plan or no undo", d.ID())
		}
	}
}
