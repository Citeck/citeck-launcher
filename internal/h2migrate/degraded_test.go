package h2migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Degradation() is the single classifier every operator-facing surface must go
// through.
//
// Two DIFFERENT failures set MigrateResult.Degraded, and they demand opposite
// advice: the filesystem fallback means storage.db could not be opened at all
// (nothing but the ws/ tree survived, secrets are gone), while a partial read
// means the store DID open, its layout/meta index verified, and only some
// B-tree sub-trees were undecodable. Callers that branched on `Degraded` alone
// told partial-read users the fallback story — three false claims and a wrong
// diagnosis. The accessor exists so no caller has to re-derive the split.
// ---------------------------------------------------------------------------

func TestDegradationClassifiesTheTwoLossyPaths(t *testing.T) {
	tests := []struct {
		name       string
		result     *MigrateResult
		wantKind   DegradationKind
		wantReason string
	}{
		{
			name:     "clean migration is not degraded",
			result:   &MigrateResult{Workspaces: 2, Namespaces: 3},
			wantKind: DegradationNone,
		},
		{
			name:       "filesystem fallback",
			result:     &MigrateResult{Degraded: true, FallbackReason: "cannot open storage.db: boom"},
			wantKind:   DegradationFallback,
			wantReason: "cannot open storage.db: boom",
		},
		{
			name:       "partial read",
			result:     &MigrateResult{Degraded: true, PartialReason: "partial read of storage.db: …"},
			wantKind:   DegradationPartial,
			wantReason: "partial read of storage.db: …",
		},
		{
			// Defensive: Degraded without either reason must still classify as
			// SOMETHING lossy rather than silently reporting "clean".
			name:     "degraded with no reason falls back to the fallback kind",
			result:   &MigrateResult{Degraded: true},
			wantKind: DegradationFallback,
		},
		{
			// A reason left over without the flag is not a degradation: the
			// flag is the authority, exactly as the JSON contract states.
			name:     "reason without the flag is not degraded",
			result:   &MigrateResult{PartialReason: "stale"},
			wantKind: DegradationNone,
		},
		{
			name:     "nil result",
			result:   nil,
			wantKind: DegradationNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, reason := tt.result.Degradation()
			assert.Equal(t, tt.wantKind, kind)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

// TestDegradationMirrorsTheRecordedFlag pins the accessor against the durable
// record the UI branches on: whatever Degradation() says here must match
// DegradedMigration.Partial, or the CLI and the web banner would disagree about
// the same migration.
func TestDegradationMirrorsTheRecordedFlag(t *testing.T) {
	homeDir, store := newStoreForSynth(t, buildWorkspaceStore(t, true))

	res, err := Migrate(homeDir, store)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kind, reason := res.Degradation()
	assert.Equal(t, DegradationPartial, kind)
	assert.Contains(t, reason, "partial read")

	rec, err := LoadDegradedMigration(store)
	if err != nil {
		t.Fatalf("load record: %v", err)
	}
	assert.True(t, rec.Partial, "the record and the accessor must agree")
	assert.Equal(t, rec.Reason, reason, "the accessor must hand back the recorded reason verbatim")
}
