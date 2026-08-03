package h2migrate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// degradedMigrationStateKey is the key-value state entry under which a lossy
// migration records itself. Read back via LoadDegradedMigration.
const degradedMigrationStateKey = "migration.degraded"

// DegradationKind names WHICH of the two lossy migration paths a MigrateResult
// took. The distinction is user-facing and the advice is opposite in each case,
// so every operator-facing surface (the CLI/daemon log, the migration-status
// endpoint, the web banner) must branch on it rather than on Degraded alone.
type DegradationKind string

const (
	// DegradationNone means the migration was clean and nothing was lost.
	DegradationNone DegradationKind = ""
	// DegradationFallback means storage.db could not be opened at all and the
	// filesystem fallback rebuilt what the on-disk ws/ tree could prove.
	// Secrets and real namespace configs did NOT make it across.
	DegradationFallback DegradationKind = "fallback"
	// DegradationPartial means storage.db opened and its layout/meta index
	// verified, but a lenient user-map read had to drop entries or whole
	// sub-trees. Most data came through; the lost rows exist nowhere on disk.
	DegradationPartial DegradationKind = "partial"
)

// Degradation classifies this result and returns the matching reason string.
//
// It exists so callers cannot repeat the bug it was extracted to fix: branching
// on Degraded alone and then reporting FallbackReason produced, on the partial
// path, a message whose every claim was false plus an EMPTY reason. The split
// mirrors the durable record's Partial flag (and the web banner's branch on
// it), so the CLI and the UI can never tell the user two different stories.
//
// Degraded is the authority: a stray reason without the flag is not a
// degradation, and the flag without either reason is still reported as lossy
// (as the fallback kind, the older and more severe of the two) rather than
// silently downgraded to "clean".
func (r *MigrateResult) Degradation() (kind DegradationKind, reason string) {
	switch {
	case r == nil || !r.Degraded:
		return DegradationNone, ""
	case r.PartialReason != "":
		return DegradationPartial, r.PartialReason
	default:
		return DegradationFallback, r.FallbackReason
	}
}

// DegradedMigration is the durable record of a lossy migration.
//
// It exists because the failure it describes was previously INVISIBLE: the
// MigrateResult was logged once at bootstrap and discarded, no secrets blob
// was ever written (so the master-password prompt never appeared), and the
// user was left looking at what appeared to be a fresh install with an empty
// repo URL, no secrets and stub namespaces.
//
// Partial distinguishes the two ways a migration can be lossy, and the
// distinction is user-facing: with Partial=false the reader could not open
// storage.db at all and everything here was rebuilt from the ws/ tree, whereas
// with Partial=true the store WAS readable, most data came through, and only
// the listed maps lost rows — there is nothing on disk to rebuild them from.
type DegradedMigration struct {
	// Reason is the internal fallback reason plus the underlying reader error.
	Reason string `json:"reason"`
	// OccurredAt is an RFC3339 timestamp.
	OccurredAt string `json:"occurredAt"`
	// Partial marks the store-was-readable case. Absent (false) for the
	// filesystem fallback, keeping that record's JSON shape unchanged.
	Partial bool `json:"partial,omitempty"`
	// LostEntries counts key/value pairs a lenient user-map read could not
	// decode. Only meaningful when Partial is set.
	LostEntries int `json:"lostEntries,omitempty"`
	// LostSubtrees counts B-tree sub-trees (or whole maps) skipped wholesale;
	// their row count is unknowable. Only meaningful when Partial is set.
	LostSubtrees int `json:"lostSubtrees,omitempty"`
	// BackupPath points at the untouched pre-migration storage.db copy, so the
	// UI can tell the user exactly which file to hand to support.
	BackupPath string `json:"backupPath,omitempty"`

	Workspaces int `json:"workspaces"`
	Namespaces int `json:"namespaces"`
	Secrets    int `json:"secrets"`
	// RecoveredRepoURLs counts workspaces whose git remote could be read back
	// out of their on-disk clone.
	RecoveredRepoURLs int `json:"recoveredRepoUrls"`
	// LostFields names what could NOT be reconstructed, in dotted-path form:
	// "workspace.secretId" and friends on the fallback path, "map.<mvstore map
	// name>" on the partial-read path. Intended for a UI list, not for parsing.
	LostFields []string `json:"lostFields,omitempty"`
}

// LoadDegradedMigration returns the persisted degraded-migration record, or
// (nil, nil) when the last migration was clean (or never ran).
func LoadDegradedMigration(store storage.Store) (*DegradedMigration, error) {
	if store == nil {
		return nil, nil
	}
	raw, err := store.GetStateValue(degradedMigrationStateKey)
	if err != nil {
		return nil, fmt.Errorf("read degraded migration record: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var rec DegradedMigration
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("parse degraded migration record: %w", err)
	}
	return &rec, nil
}

// saveDegradedMigration persists the record. Best-effort: a storage failure
// here must not turn a partially-successful migration into a hard boot failure,
// but it IS logged at error level because it means the user loses the warning.
func saveDegradedMigration(store storage.Store, rec DegradedMigration) {
	raw, err := json.Marshal(rec)
	if err != nil {
		slog.Error("Failed to marshal degraded migration record", "err", err)
		return
	}
	if err := store.SetStateValue(degradedMigrationStateKey, string(raw)); err != nil {
		slog.Error("CRITICAL: failed to persist the degraded-migration record — the user will not be warned",
			"err", err)
	}
}

// newDegradedRecord seeds the fields both lossy paths share: the reason, the
// timestamp, what DID make it across, and the pre-migration backup to hand to
// support. Shared so the two paths can never drift on the common half.
func (r *MigrateResult) newDegradedRecord(homeDir, reason string) DegradedMigration {
	rec := DegradedMigration{
		Reason:     reason,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Workspaces: r.Workspaces,
		Namespaces: r.Namespaces,
		Secrets:    r.Secrets,
	}
	backupPath := filepath.Join(homeDir, "storage.db"+kotlinBackupSuffix)
	if _, err := os.Stat(backupPath); err == nil {
		rec.BackupPath = backupPath
	}
	return rec
}

// recordPartialRead marks an otherwise-successful migration as degraded because
// the LENIENT user-map read dropped data, and persists the durable record.
//
// This is emphatically NOT the filesystem-fallback case: storage.db opened, its
// layout and meta index verified, and the great majority of the data came
// through. Only individual B-tree sub-trees inside user-data maps were
// undecodable. The reason string leads with "partial read" so the two can never
// be confused, because the user-facing advice differs — there is no ws/ tree to
// rebuild these rows from; they are gone from this store and only the
// storage.db.kotlin-bak parachute can be re-examined.
//
// A nil summary (the clean case) is a no-op, so Migrate can call this
// unconditionally and a clean run still writes NO record at all — which is what
// keeps `migrationDegraded` / `degradedMigration` ABSENT from the
// migration-status JSON rather than present-and-false.
func (r *MigrateResult) recordPartialRead(homeDir string, store storage.Store, pr *partialRead) {
	if pr == nil {
		return
	}
	r.Degraded = true
	r.PartialReason = pr.reason()

	rec := r.newDegradedRecord(homeDir, r.PartialReason)
	rec.Partial = true
	rec.LostEntries = pr.LostEntries
	rec.LostSubtrees = pr.LostSubtrees
	// Name the affected maps rather than reporting a bare count: "which of my
	// workspaces/namespaces/secrets is incomplete" is the only question the
	// user can act on.
	for _, name := range pr.Maps {
		rec.LostFields = append(rec.LostFields, "map."+name)
	}

	saveDegradedMigration(store, rec)
	slog.Warn("Recorded a PARTIALLY READ migration — the store opened but some entries were undecodable",
		"lostEntries", rec.LostEntries, "lostSubtrees", rec.LostSubtrees,
		"maps", pr.Maps, "defects", pr.Defects)
}

// reason renders the human-readable cause stored on the DegradedMigration. It
// deliberately opens with "partial read" so it is never mistaken for one of the
// fallback reasons ("cannot open storage.db: …", "dump error", "empty dump …").
func (p *partialRead) reason() string {
	out := fmt.Sprintf("partial read of storage.db: the store opened and its layout/meta index verified, "+
		"but %d entries and %d B-tree sub-trees could not be decoded in %d user map(s): %s",
		p.LostEntries, p.LostSubtrees, len(p.Maps), strings.Join(p.Maps, ", "))
	if len(p.Defects) > 0 {
		out += "; first defects: " + strings.Join(p.Defects, "; ")
	}
	return out
}

// recordDegradation persists the durable warning record for this lossy run.
func (r *MigrateResult) recordDegradation(homeDir string, store storage.Store, recoveredRepos, missingRepos int) {
	rec := r.newDegradedRecord(homeDir, r.FallbackReason)
	rec.RecoveredRepoURLs = recoveredRepos

	// Always lost on this path: the secrets vault lives only inside the H2
	// store, and these three workspace fields have no filesystem representation.
	rec.LostFields = []string{
		"secrets",
		"workspace.authType",
		"workspace.secretId",
		"workspace.repoPullPeriod",
	}
	if missingRepos > 0 {
		rec.LostFields = append(rec.LostFields, "workspace.repoUrl", "workspace.repoBranch")
	}
	if r.Namespaces > 0 {
		// Namespace configs are regenerated as Kotlin-parity stubs, not
		// migrated: bundleRef beyond the workspace default, proxy, webapps,
		// datasources and per-app overrides are all gone.
		rec.LostFields = append(rec.LostFields, "namespace.config")
	}

	saveDegradedMigration(store, rec)
	slog.Warn("Recorded a DEGRADED migration", "reason", rec.Reason,
		"workspaces", rec.Workspaces, "namespaces", rec.Namespaces,
		"secrets", rec.Secrets, "recoveredRepoUrls", rec.RecoveredRepoURLs,
		"lostFields", rec.LostFields)
}
