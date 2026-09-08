package migrate

import "fmt"

// PreflightResult is what the confirm dialog and `citeck deps upgrade` show
// before anything is touched. Problems block the migration; Warnings need an
// explicit confirmation (today: an existing target volume).
//
// Problems and Warnings are JSON ARRAYS on the wire, never null: the web
// dialog maps over both without a guard for the ordinary case, and the CLI's
// `for range` over a nil slice hides the difference. Build every result with
// NewPreflightResult (or set both fields) rather than with a bare literal.
type PreflightResult struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	// Sizes in bytes. Host = the filesystem holding the dump; Volume = the
	// filesystem holding the data volumes (the Docker VM's disk on a
	// macOS/Windows desktop, which is NOT the host's).
	DataSizeBytes        int64           `json:"dataSizeBytes"`
	RequiredHostBytes    int64           `json:"requiredHostBytes"`
	RequiredVolumeBytes  int64           `json:"requiredVolumeBytes"`
	FreeHostBytes        int64           `json:"freeHostBytes"`
	FreeVolumeBytes      int64           `json:"freeVolumeBytes"`
	ExistingTargetVolume *ExistingVolume `json:"existingTargetVolume,omitempty"`
	WasRunning           bool            `json:"wasRunning"`
}

// ExistingVolume describes a target volume that is already there — a leftover
// from an earlier attempt, or somebody else's data. Size and version are what
// let the user tell those two apart before confirming its deletion.
type ExistingVolume struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	Version   string `json:"version"` // PG_VERSION content, or "empty"
}

// PlanOptions carries the user's explicit confirmations.
type PlanOptions struct {
	// ReplaceExistingVolume confirms deleting an existing target volume. It is
	// carried into the plan (not consumed at plan time only) because the step
	// re-checks: a volume can appear between the preflight and the step.
	ReplaceExistingVolume bool
}

// SpaceMargin is the free-space headroom demanded on top of the data size, on
// both filesystems. A logical dump of a cluster is usually well under the
// cluster's own size (no indexes, no bloat, no WAL), so "the data size again"
// is already generous; the margin covers the difference between a volume's
// measured size and what the restore's WAL and indexes need on top.
const SpaceMargin int64 = 512 << 20

// NewPreflightResult is the only way a PreflightResult should be built: it
// gives Problems and Warnings the empty-slice value the wire contract demands
// (see the type's doc), which a struct literal silently would not.
func NewPreflightResult(from, to string) PreflightResult {
	return PreflightResult{From: from, To: to, Problems: []string{}, Warnings: []string{}}
}

// RefusedPreflight is a preflight that never ran: a condition the daemon knows
// about before it touches Docker (an open journal, a busy namespace, another
// long operation) already refuses the migration, and the confirm screen has to
// show it as its own input rather than as a 409 after the click.
func RefusedPreflight(from, to string, problems ...string) PreflightResult {
	res := NewPreflightResult(from, to)
	res.Problems = append(res.Problems, problems...)
	return res
}

// UnsupportedPairProblem is what an operator is told about a version pair this
// launcher has no migration for — a newer PostgreSQL major whose data layout
// this release does not know (see PostgresMigrator.Supports). It is worded as
// an action on the LAUNCHER because that is the only thing that can change:
// the bundle offers the version, the generator holds it back, and no amount of
// disk space or namespace juggling will make this release move the data.
func UnsupportedPairProblem(from, to string) string {
	return fmt.Sprintf("this launcher cannot migrate %s → %s yet; update the launcher", from, to)
}

// PostgresStepIDs are the ids of the PostgreSQL plan's steps, in order. It is
// exported because four other places name the same list — the CLI's locale
// keys, the plan tests, the integration test and the web dialog's STEP_IDS —
// and a step renamed in Plan without them is a progress screen full of raw
// ids. The Go callers read it from here; the web copy carries a pointer back
// to this function.
func PostgresStepIDs() []string {
	return []string{
		"stop-namespace", "pull-image", "start-source", "dump", "stop-source",
		"create-volume", "start-target", "restore", "verify", "stop-target",
	}
}
