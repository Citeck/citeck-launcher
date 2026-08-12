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

// TestConfiguredHeapKeepsTheBudgetForTheOtherPools is the case that actually
// exists in the field: namespaces set heapSize and nothing else, so the heap is
// deliberate while direct memory, metaspace and the code cache are unbounded —
// precisely the shape that put eproc at 94% of its limit with one bounded pool
// out of five.
//
// Stepping aside there would leave the real problem in place, and overriding
// their -Xmx would be worse. So the heap stays theirs and the REST of the
// container is divided over the pools nobody sized.
func TestConfiguredHeapKeepsTheBudgetForTheOtherPools(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "4g", HeapSize: "3g"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-Xmx3g", "the operator's heap is untouched")
	assert.Equal(t, 1, strings.Count(opts, "-Xmx"), "no second heap size: %s", opts)
	assert.Contains(t, opts, "-XX:MaxDirectMemorySize=")
	assert.Contains(t, opts, "-XX:MaxMetaspaceSize=")
	assert.Contains(t, opts, "-XX:ReservedCodeCacheSize=")

	arenas, ok := app.Environments.Get("MALLOC_ARENA_MAX")
	assert.True(t, ok, "the budget ran, so its env belongs with it")
	assert.Equal(t, "2", arenas)

	// And the whole point: the numbers still fit the box, with the fixed heap
	// counted as-is rather than as a wish.
	b, ok := ComputeMemoryBudgetWith(4*gib, ManualPools{Heap: 3 * gib})
	require.True(t, ok)
	assert.Equal(t, int64(3)*gib, b.Heap)
	assert.LessOrEqual(t, b.Reserve+b.Heap+b.Direct+b.Metaspace+b.CodeCache, int64(4)*gib, "%s", b)
}

// The pools that are left shrink as the operator's heap grows — the budget
// divides what is actually free, not what would have been free.
func TestFixedHeapShrinksTheOtherPools(t *testing.T) {
	small, ok := ComputeMemoryBudgetWith(6*gib, ManualPools{Heap: 2 * gib})
	require.True(t, ok)
	large, ok := ComputeMemoryBudgetWith(6*gib, ManualPools{Heap: 4 * gib})
	require.True(t, ok)

	assert.Greater(t, small.Direct, large.Direct)
	assert.Greater(t, small.Metaspace, large.Metaspace)
	assert.GreaterOrEqual(t, small.CodeCache, large.CodeCache)
	assert.Greater(t, small.Remainder, large.Remainder)
}

// A heap that already fills the container cannot be budgeted around. Emitting
// caps anyway would promise headroom that is not there; the app is
// misconfigured and the warning in applyMemoryBudget is the useful output.
func TestFixedHeapWithNoRoomLeftIsRefused(t *testing.T) {
	_, ok := ComputeMemoryBudgetWith(4*gib, ManualPools{Heap: 4 * gib})
	assert.False(t, ok)
	_, ok = ComputeMemoryBudgetWith(4*gib, ManualPools{Heap: 3800 * mib})
	assert.False(t, ok, "a heap leaving no room for metaspace must be refused too")
}

// Every pool set by hand means nothing to add — and, importantly, no duplicate
// flags.
func TestAllPoolsFixedEmitsNothing(t *testing.T) {
	b, ok := ComputeMemoryBudgetWith(6*gib, ManualPools{
		Heap: 3 * gib, Direct: 512 * mib, Metaspace: 256 * mib, CodeCache: 128 * mib,
	})
	require.True(t, ok)
	assert.Empty(t, b.JavaOpts())
}

// MaxRAMPercentage sizes the heap from the same container limit the JVM would
// use, so it is read as the heap rather than treated as an opaque flag: the
// other three pools are then budgeted around it.
func TestMaxRAMPercentageIsReadAsTheHeap(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "4g", JavaOpts: "-XX:MaxRAMPercentage=70"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-XX:MaxRAMPercentage=70")
	assert.NotContains(t, opts, "-Xmx", "the JVM computes the heap; a second source would fight it")
	assert.Contains(t, opts, "-XX:MaxDirectMemorySize=")

	limit := int64(4) * gib
	pools, ok := parseManualPools("-XX:MaxRAMPercentage=70", limit)
	require.True(t, ok)
	assert.Equal(t, int64(float64(limit)*70/100), pools.Heap)
}

func TestParseManualPools(t *testing.T) {
	pools, ok := parseManualPools(
		"-Xmx3g -XX:MaxDirectMemorySize=512m -XX:MaxMetaspaceSize=262144k -XX:ReservedCodeCacheSize=134217728 -Dx=y",
		4*gib)
	require.True(t, ok)
	assert.Equal(t, int64(3)*gib, pools.Heap)
	assert.Equal(t, int64(512)*mib, pools.Direct)
	assert.Equal(t, int64(256)*mib, pools.Metaspace, "k suffix")
	assert.Equal(t, int64(128)*mib, pools.CodeCache, "bare bytes")

	// An explicit -Xmx wins over a percentage, exactly as it does in the JVM.
	pools, ok = parseManualPools("-XX:MaxRAMPercentage=50 -Xmx1g", 4*gib)
	require.True(t, ok)
	assert.Equal(t, int64(1)*gib, pools.Heap)

	// Nothing configured is not an error — it is the ordinary case.
	pools, ok = parseManualPools("-Dspring.jmx.enabled=true", 4*gib)
	require.True(t, ok)
	assert.True(t, pools.empty())

	// A value we cannot read must NOT be guessed at: reasoning from a number we
	// do not understand is how a budget ends up describing a different container.
	for _, opts := range []string{"-Xmx3gb", "-Xmx", "-XX:MaxMetaspaceSize=lots", "-XX:MaxRAMPercentage=abc"} {
		_, ok := parseManualPools(opts, 4*gib)
		assert.Falsef(t, ok, "%q must not parse", opts)
	}
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
// javaOpts/heapSize) is the third way in, and it is read the same way: the
// budget parses the env it is about to append to, whoever wrote it.
func TestEnvironmentsJavaOptsHeapIsRespected(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{
		MemoryLimit:  "4g",
		Environments: map[string]string{"JAVA_OPTS": "-Xmx2500m -Dfoo=bar"},
	})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-Xmx2500m")
	assert.Equal(t, 1, strings.Count(opts, "-Xmx"))
	// …and the pools they did not set are budgeted around it.
	assert.Contains(t, opts, "-XX:MaxDirectMemorySize=")
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
