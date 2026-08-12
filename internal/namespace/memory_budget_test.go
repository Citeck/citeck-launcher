package namespace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryBudgetInvariant is the reason the budget is computed in the launcher
// instead of being delegated to the JVM: it can be CHECKED. Whatever the limit,
// the pools plus the reserve must fit inside it — with no container, no stand
// and no measurement, in microseconds.
//
// The failure this prevents is not theoretical: eproc ran with one bounded pool
// out of five (heap), an unset MaxDirectMemorySize defaulting to the heap size
// again, and 5760 MiB of RSS against a 6144 MiB limit.
func TestMemoryBudgetInvariant(t *testing.T) {
	for _, limitMiB := range []int64{1024, 1536, 2048, 3072, 4096, 6144, 8192, 12288, 16384, 32768} {
		t.Run(fmt.Sprintf("%dMiB", limitMiB), func(t *testing.T) {
			limit := limitMiB * mib
			b, ok := ComputeMemoryBudget(limit)
			require.True(t, ok, "a %d MiB limit must be budgetable", limitMiB)

			sum := b.Reserve + b.Heap + b.Direct + b.Metaspace + b.CodeCache + b.Remainder
			assert.Equal(t, limit, sum, "the pools must account for the whole limit: %s", b)
			assert.LessOrEqual(t, b.Reserve+b.Heap+b.Direct+b.Metaspace+b.CodeCache, limit,
				"flagged pools plus reserve must fit: %s", b)

			// Every pool must be usable, and the unflagged remainder must cover
			// thread stacks and G1 side structures (the mark bitmap alone is
			// heap/64) rather than being a rounding artifact.
			assert.Positive(t, b.Heap, "%s", b)
			assert.Positive(t, b.Direct, "%s", b)
			assert.Positive(t, b.Metaspace, "%s", b)
			assert.Positive(t, b.CodeCache, "%s", b)
			assert.GreaterOrEqual(t, b.Remainder, b.Heap/64+budgetMinRemainder,
				"remainder must cover G1's mark bitmap (heap/64) plus thread stacks: %s", b)
		})
	}
}

// A budget that does not fit its own box is worse than no budget: it promises
// headroom that is not there. Below ~1 GiB the absolute floors (a usable
// metaspace and code cache) leave nothing for the heap, so the JVM's own
// defaults are the better answer and ok must be false.
func TestMemoryBudgetRefusesSmallLimits(t *testing.T) {
	for _, limitMiB := range []int64{0, 128, 256, 512, 768} {
		_, ok := ComputeMemoryBudget(limitMiB * mib)
		assert.Falsef(t, ok, "a %d MiB limit must not be budgeted", limitMiB)
	}
	// 1 GiB is the default webapp limit, so it has to be on the budgeted side of
	// the line — otherwise the feature would do nothing for most apps.
	_, ok := ComputeMemoryBudget(1 * gib)
	assert.True(t, ok, "the default 1 GiB webapp limit must be budgetable")
}

// TestMemoryBudgetGolden pins the numbers for the container that motivated this,
// so a change to the percentages is a deliberate act with a visible diff rather
// than a silent drift in what every app gets.
func TestMemoryBudgetGolden(t *testing.T) {
	b, ok := ComputeMemoryBudget(6144 * mib)
	require.True(t, ok)

	assert.Equal(t, int64(491), b.Reserve/mib)
	assert.Equal(t, int64(3278), b.Heap/mib)
	assert.Equal(t, int64(734), b.Direct/mib)
	assert.Equal(t, int64(512), b.Metaspace/mib) // clamped
	assert.Equal(t, int64(240), b.CodeCache/mib) // clamped
	assert.Equal(t, int64(887), b.Remainder/mib)

	assert.Equal(t,
		"-Xmx3278m -XX:MaxDirectMemorySize=734m -XX:MaxMetaspaceSize=512m -XX:ReservedCodeCacheSize=240m",
		b.JavaOpts())

	// The old configuration for the same container: -Xmx4g and nothing else,
	// with direct memory defaulting to the heap size. That cannot satisfy the
	// inequality at any value of the unbounded terms — which is the whole point.
	assert.Less(t, b.Heap, int64(4)*gib)
}

// Bigger limit, bigger pools — monotonicity is what makes the budget predictable
// for someone who bumps a memoryLimit and expects the app to actually get more.
func TestMemoryBudgetIsMonotonic(t *testing.T) {
	var prev MemoryBudget
	for _, limitMiB := range []int64{1024, 2048, 4096, 8192, 16384} {
		b, ok := ComputeMemoryBudget(limitMiB * mib)
		require.True(t, ok)
		if prev.Limit != 0 {
			assert.Greater(t, b.Heap, prev.Heap, "heap must grow with the limit")
			assert.GreaterOrEqual(t, b.Direct, prev.Direct)
			assert.GreaterOrEqual(t, b.Metaspace, prev.Metaspace)
			assert.GreaterOrEqual(t, b.CodeCache, prev.CodeCache)
		}
		prev = b
	}
}

// -Xms is deliberately absent: the images pre-touch, and an enterprise namespace
// is 24 apps on a 16 GB box — pinning -Xms to -Xmx would have all of them claim
// their whole heap during a cold start.
func TestMemoryBudgetDoesNotPinInitialHeap(t *testing.T) {
	b, ok := ComputeMemoryBudget(4 * gib)
	require.True(t, ok)
	assert.NotContains(t, b.JavaOpts(), "-Xms")
}

func TestWebappGetsComputedMemoryBudget(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "4g"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, ok := app.Environments.Get("JAVA_OPTS")
	require.True(t, ok)
	budget, _ := ComputeMemoryBudget(4 * gib)
	assert.Contains(t, opts, budget.JavaOpts())
	// The heap-dump flags still land after the budget.
	assert.Contains(t, opts, "-XX:+HeapDumpOnOutOfMemoryError")

	// glibc arenas are the one unbounded native term with no JVM flag at all.
	arenas, ok := app.Environments.Get("MALLOC_ARENA_MAX")
	assert.True(t, ok)
	assert.Equal(t, "2", arenas)
}

// A hand-configured heap size wins outright. Silently overriding someone's
// deliberate -Xmx — or worse, adding a second one — is not a service.
func TestExplicitHeapSizeDisablesTheBudget(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "4g", HeapSize: "3g"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-Xmx3g")
	assert.Equal(t, 1, strings.Count(opts, "-Xmx"), "no second heap size: %s", opts)
	assert.NotContains(t, opts, "MaxDirectMemorySize")
	assert.NotContains(t, opts, "MaxMetaspaceSize")
	_, hasArenas := app.Environments.Get("MALLOC_ARENA_MAX")
	assert.False(t, hasArenas, "the budget stepped aside, so its env must not appear either")
}

// MaxRAMPercentage sizes the heap from the limit as well, so mixing it with the
// budget would produce arithmetic that no longer describes the container.
func TestMaxRAMPercentageDisablesTheBudget(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "4g", JavaOpts: "-XX:MaxRAMPercentage=70"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-XX:MaxRAMPercentage=70")
	assert.NotContains(t, opts, "-Xmx")
}

// The default webapp limit is 1g; anything below the floor must be left to the
// JVM rather than given a budget that does not fit.
func TestTinyLimitLeavesJvmDefaults(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "512m"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.NotContains(t, opts, "-Xmx")
	assert.NotContains(t, opts, "MaxMetaspaceSize")
}

func generateWebappWith(t *testing.T, props WebappProps) *GenResp {
	t.Helper()
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps:        map[string]WebappProps{"emodel": props},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {Image: "nexus.citeck.ru/emodel:1.0"}}}
	wsCfg := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}}}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	return resp
}

// TestEditedAppPatchOverridesTheBudget answers the question every operator will
// ask: does the automatic budget trample a JAVA_OPTS I set by hand?
//
// It cannot. There are two independent layers of manual control and this pins
// the second one — `citeck edit <app>` (and the gear icon, same endpoint), whose
// patch is applied AFTER generation. ApplyAppDefPatch is a shallow top-level
// merge, so a patched `environments` replaces the generated one wholesale: the
// operator sees the effective def, edits it, and their value is final. The
// first layer is heapSize/javaOpts config, covered by the tests above.
func TestEditedAppPatchOverridesTheBudget(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps:        map[string]WebappProps{"emodel": {MemoryLimit: "4g"}},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {Image: "nexus.citeck.ru/emodel:1.0"}}}
	wsCfg := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}}}

	// What the operator typed into the editor, having seen the generated def.
	patch := json.RawMessage(`{"environments":{"JAVA_OPTS":"-Xmx1g -XX:MaxDirectMemorySize=128m"}}`)

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{EditedAppPatches: map[string]json.RawMessage{"emodel": patch}})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Equal(t, "-Xmx1g -XX:MaxDirectMemorySize=128m", opts, "the operator's edit is final")

	// And the untouched baseline still carries the computed budget, so the
	// editor's change gutter shows what was overridden.
	baseline := findGeneratedAppIn(resp.BaselineApplications, "emodel")
	require.NotNil(t, baseline)
	baseOpts, _ := baseline.Environments.Get("JAVA_OPTS")
	budget, _ := ComputeMemoryBudget(4 * gib)
	assert.Contains(t, baseOpts, budget.JavaOpts())
}

func findGeneratedAppIn(apps []appdef.ApplicationDef, name string) *appdef.ApplicationDef {
	for i := range apps {
		if apps[i].Name == name {
			return &apps[i]
		}
	}
	return nil
}

// A JAVA_OPTS set through the config's `environments` map (rather than through
// javaOpts/heapSize) is the third way in, and it is checked the same way: the
// budget reads the env it is about to append to, whoever wrote it.
func TestEnvironmentsJavaOptsDisablesTheBudget(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{
		MemoryLimit:  "4g",
		Environments: map[string]string{"JAVA_OPTS": "-Xmx2500m -Dfoo=bar"},
	})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-Xmx2500m")
	assert.Equal(t, 1, strings.Count(opts, "-Xmx"))
	assert.NotContains(t, opts, "MaxDirectMemorySize")
}

// Manual opts that do NOT size a pool are the one case where our flags join
// theirs — which is the intent: the budget is about pools nobody configured.
func TestNonMemoryJavaOptsAreKeptAlongsideTheBudget(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "4g", JavaOpts: "-Dspring.jmx.enabled=true"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-Dspring.jmx.enabled=true")
	budget, _ := ComputeMemoryBudget(4 * gib)
	assert.Contains(t, opts, budget.JavaOpts())
}
