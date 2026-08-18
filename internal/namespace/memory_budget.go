package namespace

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
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

	// The pool shares are WEIGHTS, not fractions of the limit: whatever the
	// operator has already fixed by hand is subtracted first, and the pools that
	// are left share what remains in these proportions. With nothing configured
	// the weights sum to 100 and each share is simply a percentage of the
	// budgetable space; with -Xmx set by hand the other three split what is left
	// 13:12:6 against a remainder weight of 11. One formula covers both.
	//
	// heap: the largest single pool, but deliberately not "everything else".
	budgetHeapPct = 58

	// direct memory: NIO buffers (Netty for RabbitMQ/Hazelcast, Tomcat NIO).
	// Generous on purpose — this is the pool whose real usage we cannot measure
	// without NMT, and a cap that is too tight converts a working app into one
	// that throws OutOfMemoryError: Direct buffer memory.
	budgetDirectPct = 13
	budgetDirectMax = 1 * gib

	// metaspace: class metadata — and the one pool whose demand has NOTHING to do
	// with the container limit. It is set by how much code the app loads, and a
	// Citeck webapp loads the same 26–43 thousand classes whether it runs in 1 GiB
	// or in 8.
	//
	// Measured on the live stand (2026-08-12, `citeck jcmd <app> VM.metaspace`,
	// steady state, committed ≈ used within ~2 MiB in every sample):
	//
	//	gateway         114 MB   26042 classes
	//	transformations 126 MB   28284
	//	history         165 MB   36246
	//	notifications   180 MB   38769
	//	emodel          184 MB   39547
	//	uiserv          186 MB   40094
	//	eproc           205 MB   42846
	//	eapps, integrations — OutOfMemoryError: Metaspace under a 192 MiB cap
	//
	// The percentage share alone produced 192 MiB at the 1 GiB default, which
	// killed two apps outright and left four more within 6% of their cap.
	//
	// The floor is 384 MiB rather than something nearer those numbers because of
	// what the same two apps did once they were UNCAPPED again: measured after
	// 10 hours of uptime, eapps settled at 197 MiB and integrations at 275 MiB —
	// i.e. the biggest consumer is an app that never got to report a steady state
	// before, because the cap killed it first. 384 MiB leaves it ~40% headroom;
	// 320 would have left 16%, which is the same mistake one release of extra
	// code later. Going further (448) is not free either — it would push eproc's
	// 2 GiB container back out of the budget, and that container is the whole
	// reason this exists.
	//
	// Being generous is cheap in itself: this is a CEILING, not a reservation —
	// metaspace commits on demand — so the only cost is that the inequality below
	// gets harder to satisfy.
	//
	// Which it should: the honest consequence is that a 1 GiB webapp cannot be
	// budgeted at all (classes alone want ~200 MiB, its own heap wants 256–500),
	// so ComputeMemoryBudgetWith refuses and the app keeps the JVM's defaults it
	// ran on for years. Budgeting starts to apply around 2 GiB, which is where a
	// runaway pool actually has room to hurt.
	budgetMetaspacePct = 12
	budgetMetaspaceMin = 384 * mib
	budgetMetaspaceMax = 512 * mib

	// code cache: 58 MiB used of 240 reserved on eproc.
	budgetCodeCachePct = 6
	budgetCodeCacheMin = 48 * mib
	budgetCodeCacheMax = 240 * mib

	// budgetRemainderPct is what the weights leave for the pools that take no
	// flag: thread stacks, G1 side structures, glibc. It is a weight like the
	// others precisely so that it is not silently squeezed to zero when another
	// pool is fixed by hand.
	budgetRemainderPct = 100 - budgetHeapPct - budgetDirectPct - budgetMetaspacePct - budgetCodeCachePct
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

	// manual records which pools came from the operator, so JavaOpts does not
	// emit a second flag for a pool that already has one.
	manual ManualPools
}

// ManualPools carries the pools an operator has already sized by hand, in bytes.
// A zero field means "not configured, derive it".
type ManualPools struct {
	Heap      int64
	Direct    int64
	Metaspace int64
	CodeCache int64
}

func (m ManualPools) empty() bool { return m == ManualPools{} }

// ComputeMemoryBudget divides a container memory limit into JVM pools, deriving
// every one of them.
func ComputeMemoryBudget(limit int64) (budget MemoryBudget, ok bool) {
	return ComputeMemoryBudgetWith(limit, ManualPools{})
}

// ComputeMemoryBudgetWith divides a container memory limit into JVM pools,
// taking the operator's own numbers as fixed and distributing what is left over
// the pools they did not configure.
//
// This is the case that matters in practice: today's namespaces set heapSize and
// nothing else, so the heap is deliberate and the non-heap pools are simply
// unbounded — the exact shape that put eproc at 94% of its limit. Stepping aside
// entirely there would leave the real problem untouched; overriding their -Xmx
// would be worse. So the heap stays theirs and the rest of the box gets divided.
//
// ok is false when nothing is left to divide (a hand-set heap that already fills
// the container, or a limit too small to carve a usable metaspace and code cache
// out of) — the caller must then leave the JVM's own defaults alone.
func ComputeMemoryBudgetWith(limit int64, manual ManualPools) (budget MemoryBudget, ok bool) {
	if limit <= 0 {
		return MemoryBudget{}, false // no limit configured — nothing to divide
	}
	reserve := clampBytes(limit*budgetReservePct/100, budgetReserveMin, budgetReserveMax)
	budgetable := limit - reserve

	// Space still to be handed out, and the weights that will share it. The
	// remainder always keeps its weight so the unflagged pools (stacks, G1,
	// glibc) cannot be squeezed out by a fixed heap.
	free := budgetable - manual.Heap - manual.Direct - manual.Metaspace - manual.CodeCache
	weights := int64(budgetRemainderPct)
	for _, w := range []struct {
		fixed  int64
		weight int64
	}{
		{manual.Heap, budgetHeapPct},
		{manual.Direct, budgetDirectPct},
		{manual.Metaspace, budgetMetaspacePct},
		{manual.CodeCache, budgetCodeCachePct},
	} {
		if w.fixed == 0 {
			weights += w.weight
		}
	}
	if free <= 0 {
		return MemoryBudget{}, false
	}

	share := func(fixed, weight, lo, hi int64) int64 {
		if fixed > 0 {
			return fixed
		}
		return clampBytes(free*weight/weights, lo, hi)
	}

	b := MemoryBudget{
		Limit:     limit,
		Reserve:   reserve,
		manual:    manual,
		Heap:      share(manual.Heap, budgetHeapPct, 0, budgetable),
		Direct:    share(manual.Direct, budgetDirectPct, 0, budgetDirectMax),
		Metaspace: share(manual.Metaspace, budgetMetaspacePct, budgetMetaspaceMin, budgetMetaspaceMax),
		CodeCache: share(manual.CodeCache, budgetCodeCachePct, budgetCodeCacheMin, budgetCodeCacheMax),
	}
	b.Remainder = budgetable - b.Heap - b.Direct - b.Metaspace - b.CodeCache

	// The clamps are absolute, so on a small limit — or under a hand-set heap
	// that leaves too little behind — the remainder vanishes or goes negative. A
	// budget that does not fit its own box is worse than no budget: it would
	// promise headroom that is not there, and the app would still be killed —
	// only now with our numbers on it.
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
	opts := make([]string, 0, 4)
	if b.manual.Heap == 0 {
		opts = append(opts, fmt.Sprintf("-Xmx%dm", b.Heap/mib))
	}
	if b.manual.Direct == 0 {
		opts = append(opts, fmt.Sprintf("-XX:MaxDirectMemorySize=%dm", b.Direct/mib))
	}
	if b.manual.Metaspace == 0 {
		opts = append(opts, fmt.Sprintf("-XX:MaxMetaspaceSize=%dm", b.Metaspace/mib))
	}
	if b.manual.CodeCache == 0 {
		opts = append(opts, fmt.Sprintf("-XX:ReservedCodeCacheSize=%dm", b.CodeCache/mib))
	}
	return strings.Join(opts, " ")
}

// String renders the budget for logs: one line, all pools, MiB.
func (b MemoryBudget) String() string {
	return fmt.Sprintf("limit=%dm heap=%dm direct=%dm metaspace=%dm codecache=%dm reserve=%dm remainder=%dm",
		b.Limit/mib, b.Heap/mib, b.Direct/mib, b.Metaspace/mib, b.CodeCache/mib, b.Reserve/mib, b.Remainder/mib)
}

// Pool flags an operator can set by hand, mapped to the ManualPools field they
// fill. -Xmx and MaxRAMPercentage both size the heap; the percentage form is
// resolved against the same container limit the JVM would use.
var (
	manualSizeFlags = []struct {
		flag string
		set  func(*ManualPools, int64)
	}{
		{"-Xmx", func(m *ManualPools, v int64) { m.Heap = v }},
		{"-XX:MaxDirectMemorySize=", func(m *ManualPools, v int64) { m.Direct = v }},
		{"-XX:MaxMetaspaceSize=", func(m *ManualPools, v int64) { m.Metaspace = v }},
		{"-XX:ReservedCodeCacheSize=", func(m *ManualPools, v int64) { m.CodeCache = v }},
	}
	// -Xmx3g / -XX:MaxMetaspaceSize=512m / …=1073741824 — the JVM's own size
	// syntax: digits with an optional k/m/g suffix, any case.
	jvmSizeRe = regexp.MustCompile(`^(\d+)([kKmMgG]?)$`)
)

// maxRAMPercentageFlag sizes the heap as a fraction of the container limit —
// the same limit this budget divides, so it is read as the heap rather than
// treated as an opaque flag.
const maxRAMPercentageFlag = "-XX:MaxRAMPercentage="

// parseManualPools extracts the pools an operator has already sized from
// JAVA_OPTS. ok is false when a pool flag is present but its value cannot be
// read — the budget then steps aside entirely rather than reasoning from a
// number it does not understand.
func parseManualPools(javaOpts string, limit int64) (pools ManualPools, ok bool) {
	fields := strings.Fields(javaOpts)
	for _, f := range manualSizeFlags {
		raw, found := findFlagValue(fields, f.flag)
		if !found {
			continue
		}
		size, sizeOK := parseJVMSize(raw)
		if !sizeOK {
			return ManualPools{}, false
		}
		f.set(&pools, size)
	}
	// A percentage is only meaningful against a known limit. Presence with an
	// unreadable value is refused like any other pool flag — silently ignoring a
	// typo would have us emit our own -Xmx next to theirs.
	if raw, found := findFlagValue(fields, maxRAMPercentageFlag); found {
		pct, err := strconv.ParseFloat(raw, 64)
		if err != nil || pct <= 0 || pct > 100 || limit <= 0 {
			return ManualPools{}, false
		}
		// An explicit -Xmx wins over the percentage, exactly as it does in the JVM.
		if pools.Heap == 0 {
			pools.Heap = int64(float64(limit) * pct / 100)
		}
	}
	return pools, true
}

// findFlagValue returns the value part of the first field starting with prefix.
func findFlagValue(fields []string, prefix string) (value string, found bool) {
	for _, field := range fields {
		if v, cut := strings.CutPrefix(field, prefix); cut {
			return v, true
		}
	}
	return "", false
}

// parseJVMSize reads the JVM's size syntax (1024, 512m, 3G) into bytes.
func parseJVMSize(s string) (bytes int64, ok bool) {
	m := jvmSizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	switch strings.ToLower(m[2]) {
	case "k":
		return n * 1024, true
	case "m":
		return n * mib, true
	case "g":
		return n * gib, true
	default:
		return n, true
	}
}

func clampBytes(v, lo, hi int64) int64 {
	return max(lo, min(v, hi))
}

// applyMemoryBudget appends the pool flags an app does not already have.
//
// The common case in existing namespaces is a hand-set heapSize and nothing
// else, which is exactly the shape that leaves direct memory, metaspace and the
// code cache unbounded. So a configured -Xmx is taken as given and the REST of
// the container is divided over the pools nobody sized — rather than the budget
// stepping aside and leaving the actual problem in place.
//
// Runs on the EFFECTIVE def, after any `citeck edit` patch: the patch can change
// resources.limits.memory, and budgeting the JVM for a limit the container no
// longer has is how someone raises memoryLimit to 8g and gets a 1 GiB heap.
func applyMemoryBudget(def *appdef.ApplicationDef) {
	if def.Resources == nil || def.Resources.Limits.Memory == "" {
		return
	}
	limit := docker.ParseMemory(def.Resources.Limits.Memory)
	opts, _ := def.Environments.Get("JAVA_OPTS")

	manual, ok := parseManualPools(opts, limit)
	if !ok {
		slog.Warn("JVM memory budget skipped: cannot read the configured pool sizes",
			"app", def.Name, "javaOpts", opts)
		return
	}
	budget, ok := ComputeMemoryBudgetWith(limit, manual)
	if !ok {
		applyPartialMemoryBudget(def, limit, manual, opts)
		return
	}
	if flags := budget.JavaOpts(); flags != "" {
		def.Environments.Set("JAVA_OPTS", strings.TrimSpace(opts+" "+flags))
	}
	setMallocArenaMax(def)
	// DEBUG for the same reason as the partial path: the result is not a finding,
	// it is configuration, and it is already visible where configuration is read.
	// applyJVMRuntimeDefaults runs inside Generate on the effective AND baseline
	// defs, so every flag below is in the def that `citeck edit <app>` / the gear
	// editor serve (GET /apps/{name}/config) and in the container's own env. At
	// INFO this was 24 lines per reload restating that.
	slog.Debug("Computed JVM memory budget", "app", def.Name, "budget", budget.String())
}

// applyPartialMemoryBudget handles the container the full budget refuses. It
// lowers the ceilings that can be lowered without inventing a way for the app to
// fail, and says out loud which pools are still unbounded — see partial_budget.go
// for why the refusal is no longer the end of the story.
func applyPartialMemoryBudget(def *appdef.ApplicationDef, limit int64, manual ManualPools, opts string) {
	p := ComputePartialBudget(limit, manual)
	if flags := p.JavaOpts(); flags != "" {
		def.Environments.Set("JAVA_OPTS", strings.TrimSpace(opts+" "+flags))
	}
	setMallocArenaMax(def)

	// DEBUG, not WARN or INFO. A partial budget is the ordinary shape of a real
	// namespace — a 1 GiB webapp, with or without a hand-set -Xmx — and it stops
	// nothing: the ceilings below are applied and the app starts either way. The
	// line is emitted per app on every generation, so at INFO a 24-app namespace
	// buries whatever the operator actually needs to see in a reload's output.
	// Keep both spellings: at debug level, WHY the full budget was refused is the
	// only part that is not derivable from the numbers.
	msg := "JVM memory budget partial: limit too small for a full budget"
	if !manual.empty() {
		msg = "JVM memory budget partial: the configured heap leaves too little for a full budget"
	}
	slog.Debug(msg, "app", def.Name, "limit", def.Resources.Limits.Memory,
		"capped", p.String(), "unbounded", p.Unbounded())
}

// setMallocArenaMax bounds the glibc arenas for every JVM app the launcher gives
// a memory limit — including the ones that get no budget, which is where it
// matters most: they have no other ceiling at all.
//
// glibc grows up to 8 arenas per core by default, each up to 64 MiB, and memory
// freed in one arena is not reusable by another — the classic "RSS grows well
// past the heap" shape in containerized JVMs. Two arenas costs a little malloc
// contention (the JVM allocates from its own heaps, so the effect is small) and
// removes an unbounded native term.
//
// An operator who set it by hand meant it: this never overwrites their value.
func setMallocArenaMax(def *appdef.ApplicationDef) {
	if _, ok := def.Environments.Get("MALLOC_ARENA_MAX"); ok {
		return
	}
	def.Environments.Set("MALLOC_ARENA_MAX", "2")
}

// applyJVMRuntimeDefaults applies everything that depends on the app's EFFECTIVE
// memory limit and JAVA_OPTS, so it runs after the edit patch has been merged.
func applyJVMRuntimeDefaults(def *appdef.ApplicationDef) {
	if !def.IsJVM {
		return
	}
	// An effective def that carries no patch is a struct COPY of its baseline, so
	// the two share one Environments backing array: setting JAVA_OPTS on the
	// baseline would rewrite it in the effective def as well, and the second pass
	// would then read its own output back as operator configuration and add
	// nothing. Clone before mutating.
	def.Environments = slices.Clone(def.Environments)
	applyMemoryBudget(def)
	// Appends, so it has to come after every other JAVA_OPTS writer.
	applyHeapDumpOnOOM(def)
}
