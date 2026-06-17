package namespace

import (
	"context"
	"maps"
	"sort"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// Reload-plan verdicts. These are the wire values carried by
// api.ReloadPlanAppDto.Verdict — string-typed on purpose so the api package
// does not need to import namespace.
const (
	PlanVerdictCreate   = "create"
	PlanVerdictRecreate = "recreate"
	PlanVerdictKeep     = "keep"
	PlanVerdictRemove   = "remove"
	PlanVerdictDetached = "detached"
)

// ReloadPlanEntry is one app's predicted outcome of a Regenerate with the
// given desired set, computed by PlanRegenerate without performing any of it.
type ReloadPlanEntry struct {
	Name    string
	Verdict string // PlanVerdict* constant
	// DiffAdded / DiffRemoved are GetHashInput lines present only in the new /
	// only in the current definition (populated for recreate verdicts). The
	// hash input is line-oriented and human-readable by design, so the raw
	// lines double as the change explanation.
	DiffAdded   []string
	DiffRemoved []string
	// SnapshotTag marks a kept app whose image matches the :snapshot pre-pull
	// rule (shouldPullImage). A real reload refreshes such digests from the
	// registry BEFORE the hash diff (refreshSnapshotDigests), which the plan
	// deliberately does not do (no side effects) — so "keep" can turn into
	// "recreate" if a new image was pushed under the same tag.
	SnapshotTag bool
}

// DiffHashInputLines compares two appdef.GetHashInput strings line-by-line and
// returns the lines present only in next (added) and only in current
// (removed). Order is preserved; duplicate lines are matched pairwise so a
// repeated line (e.g. two identical vol= entries) only shows up when its
// multiplicity actually changes. Pure function — unit-testable in isolation.
func DiffHashInputLines(current, next string) (added, removed []string) {
	curLines := splitHashInputLines(current)
	nextLines := splitHashInputLines(next)

	// Multiset of current lines: consume one occurrence per matching next line.
	remaining := make(map[string]int, len(curLines))
	for _, l := range curLines {
		remaining[l]++
	}
	for _, l := range nextLines {
		if remaining[l] > 0 {
			remaining[l]--
			continue
		}
		added = append(added, l)
	}
	for _, l := range curLines {
		if remaining[l] > 0 {
			remaining[l]--
			removed = append(removed, l)
		}
	}
	return added, removed
}

// splitHashInputLines splits a GetHashInput string into its non-empty lines.
// GetHashInput terminates every line with \n, so a plain Split would always
// yield one trailing "" element.
func splitHashInputLines(s string) []string {
	lines := make([]string, 0, 16)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				lines = append(lines, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// PlanRegenerate computes what doRegenerate WOULD do with the given desired
// set, without doing any of it. It is the read-only mirror of doRegenerate's
// no-lock + diff phases and must stay in lockstep with it so the plan never
// lies:
//
//   - edited-locked apps substitute their edited definition (same override);
//   - :snapshot images drop any cached digest (shouldPullImage rule);
//   - missing digests resolve from the LOCAL Docker image cache via
//     GetImageDigest — the same source doRegenerate reads after its pre-pull;
//   - the current hash source is r.apps[name].Def — exactly what
//     doRegenerate compares against (existing.Def.GetHash());
//   - detached apps (manualStoppedApps) are excluded from start/recreate;
//   - apps in r.apps but absent from the desired set are removed.
//
// Two deliberate deviations, both side-effect-driven and both surfaced to the
// caller instead of silently diverging:
//   - refreshSnapshotDigests (a registry pull) is NOT run — kept :snapshot
//     apps are flagged via ReloadPlanEntry.SnapshotTag;
//   - doStart's gateway→proxy coupled recreate does not apply here because it
//     does not apply to doRegenerate either.
//
// Comparing GetHashInput strings is equivalent to comparing GetHash values
// (the hash is sha256 of the input) and additionally yields the line diff.
func (r *Runtime) PlanRegenerate(ctx context.Context, desired []appdef.ApplicationDef) []ReloadPlanEntry {
	r.mu.RLock()
	editPatches := maps.Clone(r.editedAppPatches)
	detached := maps.Clone(r.manualStoppedApps)
	currentInputs := make(map[string]string, len(r.apps))
	for name, app := range r.apps {
		currentInputs[name] = app.Def.GetHashInput()
	}
	r.mu.RUnlock()

	entries := make([]ReloadPlanEntry, 0, len(desired)+4)
	seen := make(map[string]bool, len(desired))
	for _, def := range desired {
		def = applyEditPatchFrom(def, editPatches[def.Name])
		snapshotTag := shouldPullImage(def.Kind, def.Image)
		if snapshotTag {
			def.ImageDigest = ""
		}
		if def.ImageDigest == "" {
			if digest := r.docker.GetImageDigest(ctx, def.Image); digest != "" {
				def.ImageDigest = digest
			}
		}
		newInput := def.GetHashInput()
		seen[def.Name] = true

		entry := ReloadPlanEntry{Name: def.Name}
		current, inApps := currentInputs[def.Name]
		switch {
		case detached[def.Name]:
			// Detached stays detached: doStart commits STOPPED, doRegenerate
			// never starts it, the reconciler skips it.
			entry.Verdict = PlanVerdictDetached
		case !inApps:
			entry.Verdict = PlanVerdictCreate
		case current == newInput:
			entry.Verdict = PlanVerdictKeep
			entry.SnapshotTag = snapshotTag
		default:
			entry.Verdict = PlanVerdictRecreate
			entry.DiffAdded, entry.DiffRemoved = DiffHashInputLines(current, newInput)
		}
		entries = append(entries, entry)
	}

	// Apps present in the runtime but absent from the desired set: doRegenerate
	// marks them for removal and drives them to STOPPED + GC.
	removed := make([]string, 0, 4)
	for name := range currentInputs {
		if !seen[name] {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		entries = append(entries, ReloadPlanEntry{Name: name, Verdict: PlanVerdictRemove})
	}
	return entries
}
