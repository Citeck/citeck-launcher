package daemon

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/h2migrate"
)

// ---------------------------------------------------------------------------
// The degraded-migration log line must describe the degradation that ACTUALLY
// happened.
//
// A partial read (storage.db opened, layout/meta verified, some B-tree
// sub-trees undecodable) used to be logged with the filesystem-fallback
// wording, which makes three claims that are all false on that path:
//   1. "storage.db could not be read"      — it was read.
//   2. "reconstructed from the filesystem"  — the ws/ tree was never walked.
//   3. "secrets ... were NOT migrated"      — secrets came through.
//
// …and it logged reason=result.FallbackReason, which is EMPTY there, so the
// operator got no cause at all and went chasing the wrong problem. These tests
// pin the split (mirroring MigrationDegradedBanner.tsx's `info.partial` branch).
// ---------------------------------------------------------------------------

// fallbackPhrases are the claims that are true ONLY on the filesystem-fallback
// path and must never appear on a partial read.
var fallbackPhrases = []string{
	"could not be read",
	"reconstructed from the filesystem",
	"were NOT migrated",
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestLogMigrationResult_PartialReadDoesNotClaimTheFallbackStory(t *testing.T) {
	buf := captureSlog(t)

	logMigrationResult(&h2migrate.MigrateResult{
		Workspaces: 2, Namespaces: 3, Secrets: 7,
		Degraded:      true,
		PartialReason: "partial read of storage.db: the store opened and its layout/meta index verified, but 4 entries and 1 B-tree sub-trees could not be decoded in 1 user map(s): workspaces",
	}, t.TempDir())

	out := buf.String()
	assert.Contains(t, out, "PARTIAL", "the partial path needs its own headline")
	assert.Contains(t, out, "partial read of storage.db",
		"the real cause must be logged — the old code logged the EMPTY FallbackReason here")
	for _, phrase := range fallbackPhrases {
		assert.NotContains(t, out, phrase,
			"the filesystem-fallback claim %q is false on a partial read", phrase)
	}
}

func TestLogMigrationResult_FallbackKeepsTheFallbackWording(t *testing.T) {
	buf := captureSlog(t)

	logMigrationResult(&h2migrate.MigrateResult{
		Workspaces:     1,
		Degraded:       true,
		FallbackReason: "cannot open storage.db: bad header",
	}, t.TempDir())

	out := buf.String()
	for _, phrase := range fallbackPhrases {
		assert.Contains(t, out, phrase, "the fallback path must keep its (accurate) wording")
	}
	assert.Contains(t, out, "cannot open storage.db: bad header", "the fallback reason must be logged")
	assert.NotContains(t, out, "PARTIAL", "the fallback is not a partial read")
}

// A clean migration must log its summary and NOTHING at warn level — the
// warning is the operator's only server-mode/CLI signal, so a false positive
// would train them to ignore it.
func TestLogMigrationResult_CleanLogsNoWarning(t *testing.T) {
	buf := captureSlog(t)

	logMigrationResult(&h2migrate.MigrateResult{Workspaces: 2, Namespaces: 3, Secrets: 7}, t.TempDir())

	out := buf.String()
	assert.Contains(t, out, "H2 migration complete")
	assert.NotContains(t, out, "level=WARN")
	for _, phrase := range fallbackPhrases {
		assert.NotContains(t, out, phrase)
	}
}

// A nil result (migration skipped) must not panic or log anything.
func TestLogMigrationResult_NilIsANoOp(t *testing.T) {
	buf := captureSlog(t)
	require.NotPanics(t, func() { logMigrationResult(nil, t.TempDir()) })
	assert.Empty(t, buf.String())
}

// Both degraded paths must point the operator at the untouched 1.x parachute —
// it is the only thing support can re-examine.
func TestLogMigrationResult_BothDegradedPathsNameTheBackup(t *testing.T) {
	home := t.TempDir()
	for name, res := range map[string]*h2migrate.MigrateResult{
		"partial":  {Degraded: true, PartialReason: "partial read of storage.db: x"},
		"fallback": {Degraded: true, FallbackReason: "cannot open storage.db: x"},
	} {
		t.Run(name, func(t *testing.T) {
			buf := captureSlog(t)
			logMigrationResult(res, home)
			assert.Contains(t, buf.String(), "storage.db.kotlin-bak")
		})
	}
}
