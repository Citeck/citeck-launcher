package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
	assert.Equal(t, 3, migratable,
		"postgres, rabbitmq and zookeeper are the three this release migrates — "+
			"adding or removing one is a deliberate act, not a side effect")
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
