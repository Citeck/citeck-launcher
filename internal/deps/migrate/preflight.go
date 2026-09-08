package migrate

// PreflightResult is what the confirm dialog and `citeck deps upgrade` show
// before anything is touched. Problems block the migration; Warnings need an
// explicit confirmation (today: an existing target volume).
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
