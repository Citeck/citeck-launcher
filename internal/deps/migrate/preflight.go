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
	DataSizeBytes       int64 `json:"dataSizeBytes"`
	RequiredHostBytes   int64 `json:"requiredHostBytes"`
	RequiredVolumeBytes int64 `json:"requiredVolumeBytes"`
	FreeHostBytes       int64 `json:"freeHostBytes"`
	FreeVolumeBytes     int64 `json:"freeVolumeBytes"`
	// SharedFilesystem reports that those two are ONE filesystem — the ordinary
	// server layout, where the dump directory and the data volumes are both
	// under the namespace's volumes base. The dump and the new cluster coexist
	// on it (the scratch directory is removed only after the commit, and the
	// new cluster is built next to the old data), so what has to fit there is
	// RequiredTotalBytes and not either half on its own.
	SharedFilesystem bool `json:"sharedFilesystem"`
	// RequiredTotalBytes is what that one filesystem must have free: the two
	// halves added up. It is 0 when SharedFilesystem is false, where a sum
	// across two disks means nothing — SharedFilesystem is the discriminator,
	// never the zero (the same rule Measured() states for the other sizes).
	RequiredTotalBytes   int64           `json:"requiredTotalBytes"`
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

// Measured reports whether the space checks actually ran. A result built by
// RefusedPreflight never probed anything, so every size on it is a zero that
// means "not measured" — and rendered verbatim that reads as a namespace with
// no data and a full disk ("Data size: 0 B", "Host (dump): need 0 B, free
// 0 B") printed above the real reason. RequiredHostBytes is the discriminator
// because checkSpace always sets it to the data size plus SpaceMargin, so a
// measured result cannot have it at zero.
func (r PreflightResult) Measured() bool { return r.RequiredHostBytes > 0 }

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
//
// 18 → 19 is the pair that exists today: deps.PostgresLayoutFor maps every
// major from 18 up into ONE volume, so until a release adds a layout for 19
// (an entry in deps' postgresLayouts table) that move is reported here and
// refused by both routes. The namespace keeps running 18 in the meantime.
func UnsupportedPairProblem(from, to string) string {
	return fmt.Sprintf("this launcher cannot migrate %s → %s yet; update the launcher", from, to)
}

// InPlaceUpgradeProblem is the OTHER same-volume refusal: two majors that share
// one volume AND one cluster directory (16 → 17), which is a genuine in-place
// upgrade — something this migrator deliberately does not do, since its whole
// safety story is that the old data is only ever read and the rollback is
// "delete the volume we made".
//
// The reason differs from UnsupportedPairProblem's, but the operator's position
// does not, so the message has to carry the same two things: the exit (only a
// launcher release can change this) and what happens meanwhile (the namespace
// goes on running the major it has). Without them it reads as a disk-layout
// complaint and sends the operator looking for a problem they do not have.
func InPlaceUpgradeProblem(from, to, volume string) string {
	return fmt.Sprintf(
		"%s → %s keeps the data in volume %s; this migration builds the new cluster in a separate "+
			"volume and cannot upgrade one in place, so the namespace goes on running %s — "+
			"update the launcher once a release can do it",
		from, to, volume, from)
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
