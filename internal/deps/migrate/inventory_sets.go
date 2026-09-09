package migrate

import (
	"fmt"
	"sort"
	"strings"
)

// The set arithmetic both copy-upgrade inventories are built out of.
//
// It is shared rather than written twice because the two inventories make the
// same asymmetric judgement and it has to mean the same thing in both: an
// object that is MISSING after the upgrade is a loss and fails the verify,
// while one that APPEARED is reported and nothing more. The asymmetry is
// deliberate — nothing writes to the copy but the upgrade itself, so a new
// object is the new version's own (an internal exchange, a znode a newer
// server keeps), and failing on it would refuse a migration that lost nothing.

// maxNamedItems caps how many names a verdict spells out. A namespace can hold
// thousands of queues and znodes, and a failure the operator cannot read is a
// failure they cannot act on; the count is always exact.
const maxNamedItems = 20

// setDiff reports what before has that after does not, and the other way
// round. Both results are sorted, so a verdict reads the same every time.
func setDiff(before, after []string) (missing, appeared []string) {
	inAfter := make(map[string]bool, len(after))
	for _, s := range after {
		inAfter[s] = true
	}
	inBefore := make(map[string]bool, len(before))
	for _, s := range before {
		inBefore[s] = true
		if !inAfter[s] {
			missing = append(missing, s)
		}
	}
	for _, s := range after {
		if !inBefore[s] {
			appeared = append(appeared, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(appeared)
	return missing, appeared
}

// lostAndFound turns one set comparison into the verdict's two halves: a
// problem for what the upgrade lost, a note for what it added.
func lostAndFound(kind string, before, after []string) (problems, notes []string) {
	missing, appeared := setDiff(before, after)
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d %s missing after the upgrade: %s",
			len(missing), kind, namesPreview(missing)))
	}
	if len(appeared) > 0 {
		notes = append(notes, fmt.Sprintf("%d %s that did not exist before: %s",
			len(appeared), kind, namesPreview(appeared)))
	}
	return problems, notes
}

// namesPreview joins up to maxNamedItems names and says how many were left
// out, so a long list stays readable without hiding its size.
func namesPreview(names []string) string {
	if len(names) <= maxNamedItems {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxNamedItems], ", "), len(names)-maxNamedItems)
}
