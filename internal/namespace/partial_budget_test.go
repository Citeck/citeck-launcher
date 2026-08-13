package namespace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The container this whole partial path exists for: the 1 GiB webapp that the
// full budget refuses. Measured on the local stack with NMT enabled
// (2026-08-13, gateway, 45 000 requests at concurrency 100):
//
//	Java Heap  256.0 MiB   fixed by -Xmx256m, resident from the start
//	Code        56.5 -> 74.3 MiB   against a 240 MiB ergonomic reservation
//	Thread      17.4 -> 23.9 MiB
//	Other        4.6 ->  4.9 MiB   <- direct ByteBuffers live here
//	NMT total  510.7 -> 536.7 MiB  against cgroup anon 596.0 -> 658.4 MiB
//
// So the code cache is the pool that actually grows and the one worth a ceiling;
// direct memory is 4.9 MiB and capping it is hygiene, not protection.
func TestPartialBudgetCapsOnlyWhatIsSafeOnAOneGiBWebapp(t *testing.T) {
	p := ComputePartialBudget(1*gib, ManualPools{Heap: 256 * mib})
	opts := p.JavaOpts()

	assert.Contains(t, opts, "-XX:MaxDirectMemorySize=166m")
	assert.Contains(t, opts, "-XX:ReservedCodeCacheSize=128m")
	assert.NotContains(t, opts, "-Xmx", "the heap is the operator's; a partial budget never sizes it")
	assert.NotContains(t, opts, "MaxMetaspaceSize",
		"capping metaspace below the 384 MiB floor is what killed eapps and integrations")
}

// The two pools a partial budget must never touch. Metaspace demand is set by
// how much code the app loads (114-275 MiB measured across the stand), not by
// the container limit, and a cap below that kills the app outright — which is
// precisely why the full budget refused this container in the first place.
func TestPartialBudgetNeverCapsMetaspaceOrHeap(t *testing.T) {
	for _, limitMiB := range []int64{256, 512, 768, 1024, 1300, 1536, 2048, 2176} {
		for _, heapMiB := range []int64{0, 128, 256, 500, 1024} {
			p := ComputePartialBudget(limitMiB*mib, ManualPools{Heap: heapMiB * mib})
			opts := p.JavaOpts()
			assert.NotContainsf(t, opts, "MaxMetaspaceSize", "limit=%dm heap=%dm", limitMiB, heapMiB)
			assert.NotContainsf(t, opts, "-Xmx", "limit=%dm heap=%dm", limitMiB, heapMiB)
		}
	}
}

// An unset MaxDirectMemorySize already defaults to Runtime.maxMemory(), i.e. the
// heap size. So a cap above the heap would RAISE the ceiling the JVM already
// applies — the opposite of the point. Nothing this path emits may ever be
// larger than what the JVM would do on its own.
func TestPartialBudgetKeepsDirectBelowTheImplicitDefault(t *testing.T) {
	for _, limitMiB := range []int64{512, 1024, 1300, 2048} {
		for _, heapMiB := range []int64{64, 128, 256, 500, 1024} {
			p := ComputePartialBudget(limitMiB*mib, ManualPools{Heap: heapMiB * mib})
			if p.Direct == 0 {
				continue
			}
			assert.LessOrEqualf(t, p.Direct, heapMiB*mib,
				"limit=%dm heap=%dm: a cap above the implicit default raises it", limitMiB, heapMiB)
			assert.LessOrEqualf(t, p.CodeCache, budgetCodeCacheMax,
				"limit=%dm: the code cache ceiling must not exceed the ergonomic default", limitMiB)
		}
	}
}

// Without a hand-set heap the JVM sizes it from the container itself, so the
// implicit direct-memory default is unknown at generation time — and a number we
// cannot compare against is a number we must not emit.
func TestPartialBudgetSkipsDirectWhenTheHeapIsUnknown(t *testing.T) {
	p := ComputePartialBudget(1*gib, ManualPools{})
	assert.Zero(t, p.Direct)
	assert.NotContains(t, p.JavaOpts(), "MaxDirectMemorySize")
	assert.NotZero(t, p.CodeCache, "the code cache ceiling does not depend on the heap")
}

// A pool the operator sized by hand is theirs. Emitting a second flag for it
// would either fight their number or duplicate it.
func TestPartialBudgetLeavesHandSetPoolsAlone(t *testing.T) {
	p := ComputePartialBudget(1*gib, ManualPools{
		Heap: 256 * mib, Direct: 128 * mib, CodeCache: 96 * mib,
	})
	assert.Empty(t, p.JavaOpts())

	p = ComputePartialBudget(1*gib, ManualPools{Heap: 256 * mib, CodeCache: 96 * mib})
	opts := p.JavaOpts()
	assert.Contains(t, opts, "MaxDirectMemorySize")
	assert.NotContains(t, opts, "ReservedCodeCacheSize")
}

// A heap that already fills the container leaves nothing to reason about: the
// app is misconfigured, and inventing ceilings would only add a second way for
// it to fail.
func TestPartialBudgetEmitsNoCapsWhenTheHeapFillsTheContainer(t *testing.T) {
	p := ComputePartialBudget(1*gib, ManualPools{Heap: 1 * gib})
	assert.Empty(t, p.JavaOpts())
	assert.Zero(t, p.Direct)
	assert.Zero(t, p.CodeCache)
}

// The log has to say what is still unbounded, or "budget skipped" reads as "this
// app is fine".
func TestPartialBudgetNamesWhatStaysUnbounded(t *testing.T) {
	p := ComputePartialBudget(1*gib, ManualPools{Heap: 256 * mib})
	assert.Contains(t, p.Unbounded(), "metaspace")

	p = ComputePartialBudget(1*gib, ManualPools{})
	assert.Contains(t, p.Unbounded(), "metaspace")
	assert.Contains(t, p.Unbounded(), "direct")
}

// The measured headline: ~120 MiB of the gateway's anon memory is invisible to
// NMT entirely (658.4 MiB anon against 536.7 MiB NMT-committed) — glibc arenas,
// native libraries, fragmentation. That term is 25x direct memory, and until now
// MALLOC_ARENA_MAX was emitted ONLY when the full budget applied, i.e. never for
// the refused class that has no other protection at all.
func TestRefusedBudgetStillBoundsGlibcArenas(t *testing.T) {
	resp := generateWebappWith(t, WebappProps{MemoryLimit: "1g", JavaOpts: "-Xmx256m -Xms256m"})
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	arenas, ok := app.Environments.Get("MALLOC_ARENA_MAX")
	require.True(t, ok, "a refused budget must still bound the largest native term")
	assert.Equal(t, "2", arenas)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-Xmx256m", "the operator's heap survives untouched")
	assert.Contains(t, opts, "-XX:ReservedCodeCacheSize=")
	assert.NotContains(t, opts, "MaxMetaspaceSize")
}

// Not a JVM app: no flags, no env var, whatever the limit. The proxy is
// KindCiteckCore running nginx, so the gate has to be IsJVM rather than Kind.
func TestPartialBudgetIsOnlyForJVMApps(t *testing.T) {
	def := &appdef.ApplicationDef{
		Name:      "proxy",
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}},
	}
	applyJVMRuntimeDefaults(def)
	_, hasArenas := def.Environments.Get("MALLOC_ARENA_MAX")
	assert.False(t, hasArenas)
}

// An operator who set MALLOC_ARENA_MAX meant it. The old code only ever wrote
// this on the budgeted path, so "always set it" must not become "always
// overwrite it".
func TestOperatorMallocArenaMaxIsNotOverridden(t *testing.T) {
	for _, limit := range []string{"1g", "6g"} {
		def := jvmDefWithLimit(t, limit, "-Xmx256m")
		def.Environments.Set("MALLOC_ARENA_MAX", "8")
		applyMemoryBudget(def)

		arenas, ok := def.Environments.Get("MALLOC_ARENA_MAX")
		require.True(t, ok)
		assert.Equalf(t, "8", arenas, "limit=%s", limit)
	}
}

// The full budget still wins wherever it fits: eproc's 6 GiB container with a
// hand-pinned 4 GiB heap gets the complete set, not the partial one.
func TestFullBudgetStillAppliesWhereItFits(t *testing.T) {
	def := jvmDefWithLimit(t, "6g", "-Xmx4g -Xms1g")
	applyMemoryBudget(def)

	opts, _ := def.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-XX:MaxMetaspaceSize=", "a container that fits keeps its metaspace cap")
	assert.Contains(t, opts, "-XX:MaxDirectMemorySize=")
	assert.Contains(t, opts, "-XX:ReservedCodeCacheSize=")
	assert.Contains(t, opts, "-Xmx4g", "the operator's heap is never rewritten")
	assert.Equal(t, 1, strings.Count(opts, "-Xmx"), "exactly one heap flag")
}

func jvmDefWithLimit(t *testing.T, limit, javaOpts string) *appdef.ApplicationDef {
	t.Helper()
	def := &appdef.ApplicationDef{
		Name:      "emodel",
		IsJVM:     true,
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: limit}},
	}
	def.Environments.Set("JAVA_OPTS", javaOpts)
	return def
}

// A ceiling that is never reached costs address space, not RSS — but a ceiling
// BELOW what the app has been observed to need converts a working app into a
// failing one. 128 MiB is ~1.7x the 76 MiB peak measured under load, and the
// failure mode when it is reached is JIT-off plus a log line, not death.
// The margin is the point, not the comparison: at a 1 GiB limit the weighted
// share alone comes to 76.8 MiB — half a megabyte above the measured peak, which
// is not headroom, it is a coincidence. partialCodeCacheMin is what turns that
// into a ceiling worth having, so the test demands 1.5x the peak.
func TestPartialCodeCacheCeilingClearsTheMeasuredPeak(t *testing.T) {
	const measuredPeak = 76 * mib
	for _, limitMiB := range []int64{512, 1024, 1300, 2048} {
		p := ComputePartialBudget(limitMiB*mib, ManualPools{Heap: 256 * mib})
		if p.CodeCache == 0 {
			continue
		}
		assert.GreaterOrEqualf(t, p.CodeCache, measuredPeak*3/2,
			"limit=%dm: a code cache ceiling this close to the measured peak disables the JIT", limitMiB)
	}
}

func TestPartialBudgetString(t *testing.T) {
	p := ComputePartialBudget(1*gib, ManualPools{Heap: 256 * mib})
	assert.Equal(t, fmt.Sprintf("limit=1024m direct=%dm codecache=%dm", p.Direct/mib, p.CodeCache/mib), p.String())
}
