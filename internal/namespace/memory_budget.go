package namespace

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/citeck/citeck-launcher/internal/docker"
)

// JVM memory budgeting: derive EVERY pool from the container limit the launcher
// already owns, instead of sizing the heap and hoping the rest fits.
//
// The problem this solves, measured on a live stand (citeck_eproc, 2026-08-12):
// a 6144 MiB container sitting at 5760 MiB RSS with only 2690 MiB of live heap.
// Effective flags were `-Xmx4g -Xms1g -XX:+AlwaysPreTouch` and nothing else, so
// of the five pools that consume the budget exactly ONE was bounded:
//
//	metaspace                242 MiB committed, no cap
//	code cache                86 MiB resident
//	thread stacks             39 MiB over 225 threads, bounded only by thread count
//	G1 side structures       ~90 MiB
//	unattributed native    ~1333 MiB — NIO direct buffers + glibc arenas
//
// MaxDirectMemorySize is the sharpest edge: unset, the JVM defaults it to
// Runtime.maxMemory(), i.e. the whole heap size again — so a Netty leak reaches
// the cgroup ceiling (and a silent kernel OOM-kill, which produces no Java
// error, no heap dump, nothing) long before it reaches a Java OutOfMemoryError.
//
// Why compute it here rather than delegate to the JVM: -XX:MaxRAMPercentage
// sizes the heap and ONLY the heap; every other pool takes absolute bytes. And
// if the JVM computes the numbers, nothing can check them — whereas
//
//	heap + direct + metaspace + code cache + reserve <= limit
//
// computed in the launcher is a pure function of the limit, unit-testable in
// microseconds with no containers involved. That invariant is the point; see
// TestMemoryBudgetInvariant.
//
// Origin: proposal from the citeck-forge session, adapted (their percentages
// assumed a large container; the clamps here also have to behave at the 1 GiB
// default that most webapps run with).
const (
	mib = int64(1) << 20
	gib = int64(1) << 30

	// budgetMinRemainder is the floor for the pools that take no flag at all:
	// thread stacks (~150 KiB resident each — a webapp runs 130-225 threads) and
	// glibc bookkeeping. G1's side structures are checked separately against the
	// heap, since the mark bitmap alone is heap/64.
	//
	// This is also what defines the SMALLEST budgetable container: below roughly
	// 1 GiB the absolute floors (a usable metaspace and code cache) leave too
	// little behind, and the JVM's own defaults are the better answer. Having the
	// floor emerge from the inequality rather than from a separate constant means
	// there is exactly one rule to keep true.
	budgetMinRemainder = 64 * mib

	// reserve: page cache charged to the cgroup, glibc bookkeeping, native glue.
	// cgroup v2 charges page cache to the container that touches it, so a log or
	// a heap dump written INSIDE the container lands in this accounting — it is
	// reclaimable, which is why the reserve is a percentage rather than a wild
	// guess, but it is not free.
	budgetReservePct = 8
	budgetReserveMin = 96 * mib
	budgetReserveMax = 1 * gib

	// heap: the largest single pool, but deliberately not "everything else".
	budgetHeapPct = 58

	// direct memory: NIO buffers (Netty for RabbitMQ/Hazelcast, Tomcat NIO).
	// Generous on purpose — this is the pool whose real usage we cannot measure
	// without NMT, and a cap that is too tight converts a working app into one
	// that throws OutOfMemoryError: Direct buffer memory.
	budgetDirectPct = 13
	budgetDirectMax = 1 * gib

	// metaspace: class metadata. eproc measured 242 MiB committed, so 512 MiB is
	// ~2x headroom for the largest app we have.
	budgetMetaspacePct = 12
	budgetMetaspaceMin = 96 * mib
	budgetMetaspaceMax = 512 * mib

	// code cache: 58 MiB used of 240 reserved on eproc.
	budgetCodeCachePct = 6
	budgetCodeCacheMin = 48 * mib
	budgetCodeCacheMax = 240 * mib
)

// MemoryBudget is the per-app division of a container memory limit. Every field
// is bytes, and the invariant Reserve + Heap + Direct + Metaspace + CodeCache +
// Remainder == Limit holds by construction.
type MemoryBudget struct {
	Limit     int64
	Reserve   int64
	Heap      int64
	Direct    int64
	Metaspace int64
	CodeCache int64
	// Remainder is what is left for thread stacks (~150 KiB resident each), G1
	// side structures (mark bitmap = heap/64, card table, BOT) and glibc arenas.
	// None of those takes a flag, but budgeting them is what keeps the sum from
	// being wishful.
	Remainder int64
}

// ComputeMemoryBudget divides a container memory limit into JVM pools.
// ok is false when the limit is too small to divide sensibly (or absent), in
// which case the caller must leave the JVM's own defaults alone.
func ComputeMemoryBudget(limit int64) (budget MemoryBudget, ok bool) {
	if limit <= 0 {
		return MemoryBudget{}, false // no limit configured — nothing to divide
	}
	reserve := clampBytes(limit*budgetReservePct/100, budgetReserveMin, budgetReserveMax)
	budgetable := limit - reserve

	b := MemoryBudget{
		Limit:     limit,
		Reserve:   reserve,
		Heap:      budgetable * budgetHeapPct / 100,
		Direct:    min(budgetable*budgetDirectPct/100, budgetDirectMax),
		Metaspace: clampBytes(budgetable*budgetMetaspacePct/100, budgetMetaspaceMin, budgetMetaspaceMax),
		CodeCache: clampBytes(budgetable*budgetCodeCachePct/100, budgetCodeCacheMin, budgetCodeCacheMax),
	}
	b.Remainder = limit - reserve - b.Heap - b.Direct - b.Metaspace - b.CodeCache

	// The clamps are absolute, so on a small limit they overshoot and the
	// remainder goes negative or vanishes. A budget that does not fit its own box
	// is worse than no budget: it would promise headroom that is not there, and
	// the app would still be killed — only now with our numbers on it.
	if b.Remainder < b.Heap/64+budgetMinRemainder {
		return MemoryBudget{}, false
	}
	return b, true
}

// JavaOpts renders the budget as JVM flags.
//
// -Xms is deliberately NOT set. The runtime images add -XX:+AlwaysPreTouch, so
// -Xms is committed and touched at startup; pinning it to -Xmx would make every
// app claim its whole heap immediately, and an enterprise namespace is 24 apps
// on a box where the documented minimum is 16 GB. Steady-state RSS converging on
// -Xmx is fine; paying all of it during a cold start of the whole namespace is
// not.
//
// MALLOC_ARENA_MAX is an env var rather than a flag, so applyMemoryBudget sets
// it separately.
func (b MemoryBudget) JavaOpts() string {
	return strings.Join([]string{
		fmt.Sprintf("-Xmx%dm", b.Heap/mib),
		fmt.Sprintf("-XX:MaxDirectMemorySize=%dm", b.Direct/mib),
		fmt.Sprintf("-XX:MaxMetaspaceSize=%dm", b.Metaspace/mib),
		fmt.Sprintf("-XX:ReservedCodeCacheSize=%dm", b.CodeCache/mib),
	}, " ")
}

// String renders the budget for logs: one line, all pools, MiB.
func (b MemoryBudget) String() string {
	return fmt.Sprintf("limit=%dm heap=%dm direct=%dm metaspace=%dm codecache=%dm reserve=%dm remainder=%dm",
		b.Limit/mib, b.Heap/mib, b.Direct/mib, b.Metaspace/mib, b.CodeCache/mib, b.Reserve/mib, b.Remainder/mib)
}

// budgetFlagNames are the flags JavaOpts emits. If an operator has set any of
// them by hand, that pool is theirs and the whole budget steps aside.
var budgetFlagNames = []string{
	"-Xmx",
	"-XX:MaxRAMPercentage",
	"MaxDirectMemorySize",
	"MaxMetaspaceSize",
	"ReservedCodeCacheSize",
}

// hasManualMemoryOpts reports whether JAVA_OPTS already sizes a JVM pool.
// MaxRAMPercentage counts: it sizes the heap from the limit too, and mixing the
// two would produce a budget whose arithmetic no longer describes reality.
func hasManualMemoryOpts(javaOpts string) bool {
	for _, flag := range budgetFlagNames {
		if strings.Contains(javaOpts, flag) {
			return true
		}
	}
	return false
}

func clampBytes(v, lo, hi int64) int64 {
	return max(lo, min(v, hi))
}

// applyMemoryBudget appends the computed pool flags to an app's JAVA_OPTS.
//
// It is a no-op when the operator has sized any pool by hand (their number
// wins — a budget silently overriding a deliberate -Xmx would be worse than no
// budget at all), when the app has no memory limit, or when the limit is too
// small to divide.
func applyMemoryBudget(app *AppBuilder) {
	if app.Resources == nil || app.Resources.Limits.Memory == "" {
		return
	}
	opts, _ := app.Environments.Get("JAVA_OPTS")
	if hasManualMemoryOpts(opts) {
		slog.Debug("JVM memory budget skipped: pools are configured by hand",
			"app", app.Name, "javaOpts", opts)
		return
	}
	limit := docker.ParseMemory(app.Resources.Limits.Memory)
	budget, ok := ComputeMemoryBudget(limit)
	if !ok {
		slog.Debug("JVM memory budget skipped: limit too small to divide",
			"app", app.Name, "limit", app.Resources.Limits.Memory)
		return
	}
	app.AddEnv("JAVA_OPTS", strings.TrimSpace(opts+" "+budget.JavaOpts()))
	// glibc grows up to 8 arenas per core by default, each up to 64 MiB of
	// untouched-but-charged address space; on a 16-core box that is the single
	// largest unattributed native consumer after NIO. Two arenas costs a little
	// malloc contention and buys a bounded term in the inequality above.
	app.AddEnv("MALLOC_ARENA_MAX", "2")
	slog.Info("Computed JVM memory budget", "app", app.Name, "budget", budget.String())
}
