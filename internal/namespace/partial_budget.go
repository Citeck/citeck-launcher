package namespace

import (
	"fmt"
	"strings"
)

// Partial budgeting: what to do for a container the full budget REFUSES.
//
// ComputeMemoryBudgetWith is all-or-nothing by design — it either proves that
// heap + direct + metaspace + code cache + reserve fit inside the limit, or it
// steps aside. The honest consequence was that the most common container on a
// stand (a 1 GiB webapp with -Xmx256m) got nothing at all: metaspace unbounded,
// direct memory defaulting to the heap size, and a 240 MiB ergonomic code-cache
// reservation, whose ceilings sum well past the cgroup limit. That path ends in
// a kernel OOM-kill — no Java error, no heap dump, nothing to read afterwards.
//
// The conflation that caused it: budgetMetaspaceMin (384 MiB) is doing two jobs
// at once. It is the smallest SAFE CAP for metaspace, and it is also the space
// RESERVED for metaspace in the fit inequality. The first number is set by the
// worst case we must not kill (275 MiB measured on integrations, plus headroom);
// the second should be ordinary expected demand. Separating them is what lets a
// 1 GiB container be bounded at all.
//
// What this path will and will not do:
//
//   - It NEVER caps metaspace. A cap below the floor is what killed eapps and
//     integrations; not capping leaves exactly today's behavior.
//   - It NEVER sizes the heap. Where the full budget refuses, the heap is either
//     the operator's or the JVM's own container-aware default; a third opinion
//     helps nobody.
//   - It only ever LOWERS a ceiling. Every value it emits is below what the JVM
//     would apply on its own, so nothing it does can newly permit an allocation
//     that fails today.
//   - It makes no claim that the pools fit. That claim belongs to the full
//     budget; this one is hygiene, and the log says which pools are still
//     unbounded so "budget skipped" cannot read as "this app is fine".
//
// Measured with NMT on the local stack (gateway, 1 GiB, -Xmx256m, 45 000
// requests at concurrency 100): code cache 56.5 -> 74.3 MiB against a 240 MiB
// reservation, threads 17.4 -> 23.9 MiB, and direct ByteBuffers ("Other")
// 4.6 -> 4.9 MiB. So the code cache is the pool worth a ceiling, and capping
// direct memory is cheap hygiene rather than the protection it looked like.
const (
	// budgetMetaspaceExpected is metaspace demand, NOT a cap: the space this path
	// keeps clear so the ceilings it does emit leave room for the pool it refuses
	// to bound. Measured across the stand's nine JVM apps (steady state, committed
	// ~= used): gateway 114 MiB, transformations 126, history 165, notifications
	// 178, emodel 181, uiserv 184, eapps 196, eproc 202, integrations 275.
	budgetMetaspaceExpected = 288 * mib

	// partialCodeCacheMin: a ceiling under the measured peak would disable the JIT
	// on a healthy app. 76 MiB was the highest seen under load, so 128 MiB is
	// ~1.7x that — and still 112 MiB below the ergonomic default it replaces.
	partialCodeCacheMin = 128 * mib

	// partialDirectMin: 4.9 MiB was the measured peak under load, so anything at
	// this scale is generous. It exists to stop the share arithmetic from emitting
	// a cap so small that a burst of NIO traffic would hit it.
	partialDirectMin = 64 * mib
)

// PartialBudget is the set of ceilings applied to a container that cannot carry
// a full budget. A zero pool means "no flag emitted" — either the operator sized
// it, or there was no room to lower it safely.
type PartialBudget struct {
	Limit     int64
	Direct    int64
	CodeCache int64

	manual ManualPools
}

// ComputePartialBudget lowers the ceilings that can be lowered safely.
//
// It reserves budgetMetaspaceExpected for the pool it will not cap, then splits
// what is left by the same weights the full budget uses. Every result is clamped
// so it stays below the JVM's own default: the code cache below the ergonomic
// reservation, direct memory below the heap (which is what an unset
// MaxDirectMemorySize resolves to).
func ComputePartialBudget(limit int64, manual ManualPools) PartialBudget {
	p := PartialBudget{Limit: limit, manual: manual}
	if limit <= 0 {
		return p
	}
	reserve := clampBytes(limit*budgetReservePct/100, budgetReserveMin, budgetReserveMax)
	free := limit - reserve - manual.Heap - budgetMetaspaceExpected
	if free <= 0 {
		// The heap already fills the container. The app is misconfigured, and
		// inventing ceilings would only add a second way for it to fail.
		return p
	}

	// The remainder keeps its weight here for the same reason as in the full
	// budget: thread stacks and glibc take no flag and must not be squeezed out.
	weights := int64(budgetRemainderPct)
	if manual.Direct == 0 {
		weights += budgetDirectPct
	}
	if manual.CodeCache == 0 {
		weights += budgetCodeCachePct
	}

	if manual.CodeCache == 0 {
		p.CodeCache = clampBytes(free*budgetCodeCachePct/weights, partialCodeCacheMin, budgetCodeCacheMax)
	}
	// Without a hand-set heap the JVM sizes it from the container, so the implicit
	// direct default is unknown at generation time — and a ceiling we cannot
	// compare against is one we must not emit.
	if manual.Direct == 0 && manual.Heap >= partialDirectMin {
		p.Direct = clampBytes(free*budgetDirectPct/weights, partialDirectMin, min(manual.Heap, budgetDirectMax))
	}
	return p
}

// JavaOpts renders the ceilings as JVM flags. Pools the operator sized, and
// pools with no safe ceiling, contribute nothing.
func (p PartialBudget) JavaOpts() string {
	opts := make([]string, 0, 2)
	if p.Direct > 0 {
		opts = append(opts, fmt.Sprintf("-XX:MaxDirectMemorySize=%dm", p.Direct/mib))
	}
	if p.CodeCache > 0 {
		opts = append(opts, fmt.Sprintf("-XX:ReservedCodeCacheSize=%dm", p.CodeCache/mib))
	}
	return strings.Join(opts, " ")
}

// Unbounded names the pools that still have no ceiling after this pass. It goes
// in the log line because the alternative — reporting only what was capped —
// reads as "this app is covered", which is the misreading that let a container
// sit one runaway pool away from a kernel OOM-kill with nobody looking.
func (p PartialBudget) Unbounded() string {
	// Metaspace is unconditional: this path never caps it.
	pools := []string{"metaspace"}
	if p.Direct == 0 && p.manual.Direct == 0 {
		pools = append(pools, "direct")
	}
	if p.CodeCache == 0 && p.manual.CodeCache == 0 {
		pools = append(pools, "codecache")
	}
	if p.manual.Heap == 0 {
		pools = append(pools, "heap")
	}
	return strings.Join(pools, ", ")
}

// String renders the ceilings for logs: one line, MiB.
func (p PartialBudget) String() string {
	return fmt.Sprintf("limit=%dm direct=%dm codecache=%dm", p.Limit/mib, p.Direct/mib, p.CodeCache/mib)
}
