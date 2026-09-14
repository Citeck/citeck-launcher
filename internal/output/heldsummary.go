package output

import (
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// This file holds the two sentences a wait loop prints when it ends with apps
// HELD by a dependency the operator detached. They live here, in the package
// every CLI surface already imports for its table, because there are THREE such
// loops — `citeck start` (internal/cli/start.go), `citeck setup`'s reload
// (internal/cli/setup) and the live status stream — and `internal/cli/setup`
// cannot import `internal/cli` (that is an import cycle), so the natural home,
// beside the caller that happened to be written first, was not available.
//
// The duplication was not hypothetical: the same key with the same three
// arguments was spelled out in three places, and the failed+held composition in
// two, which is two chances for the two loops to word the same state
// differently — the exact defect the hold reporting was added to remove.

// HeldSummary is what a wait prints when every remaining app is held: how many
// are held, out of how many, and the DETACHED apps to start. The held apps
// themselves are not named — they release themselves as soon as the root comes
// back.
func HeldSummary(held, total int, deps []string) string {
	return i18n.T("cli.appsHeldByStoppedDeps",
		"held", strconv.Itoa(held),
		"total", strconv.Itoa(total),
		"deps", strings.Join(deps, ", "))
}

// FailedSummary is the mixed verdict: a count of what came up and what failed,
// with the hold appended when there is one.
//
// Failed and held are ADDITIVE, never alternatives — two different problems
// with two different fixes, and the apps a hold parks are not among the failed
// ones — so a run that has both says both. summaryKey differs between the
// callers only because one of them started apps and the other reloaded them
// ("3/5 apps started" vs "3/5 running"); everything after it is shared, which
// is the part that was drifting.
//
// The whole line is translated, including the counts: a sentence that is
// English in front and translated behind reads worse than either.
func FailedSummary(summaryKey string, running, total, failed, held int, deps []string) string {
	line := i18n.T(summaryKey,
		"running", strconv.Itoa(running),
		"total", strconv.Itoa(total),
		"failed", strconv.Itoa(failed))
	if held > 0 {
		line += i18n.T("cli.heldSuffix",
			"held", strconv.Itoa(held),
			"deps", strings.Join(deps, ", "))
	}
	return line
}
