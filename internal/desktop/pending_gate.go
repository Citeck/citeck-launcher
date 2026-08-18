package desktop

import (
	"context"
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/update"
)

// GatePendingPayload health-gates a staged payload that was applied but never
// judged, at wrapper start.
//
// The normal apply path is gated: the daemon stages the payload (manifest state
// `pending`), the wrapper restarts into it, and applyDaemonSwap writes `good` or
// `failed` — and `failed` is what makes SelectBest fall back to the previous
// good / bundled binary. The hole is everything that can happen BETWEEN those
// two steps: the apply verb never reaches the wrapper (no control socket, a
// transport error, a 5s timeout), the wrapper crashes, or the user quits the app
// while the daemon has already recorded the payload. The entry then stays
// `pending` forever, SelectBest treats `pending` as selectable, and every
// subsequent start spawns that payload with NO gate and no way to ever mark it
// failed. If it does not come up, the supervisor burns its restart budget, the
// next launch does exactly the same, and the only way out is deleting the
// updates directory by hand.
//
// So: whatever selected binary the supervisor just started, if the manifest says
// it is still `pending`, judge it now with the same gate the swap uses. Passing
// promotes it to `good` (and stops it being re-judged on every start); failing
// marks it `failed` and restarts, which is the rollback the swap path would have
// performed. Returns true if it rolled back.
//
// Called once per wrapper start, from a goroutine — it blocks for up to
// healthTimeout.
func GatePendingPayload(ctx context.Context, sup *Supervisor, updatesDir, currentVersion string, healthTimeout time.Duration) (rolledBack bool) {
	entry, ok := update.SelectBestEntry(updatesDir, currentVersion)
	if !ok || entry.State != update.StatePending {
		return false // running the bundled binary, or an already-judged payload
	}

	if err := sup.WaitReady(ctx, healthTimeout); err == nil {
		if merr := update.MarkState(updatesDir, entry.Version, update.StateGood); merr != nil {
			slog.Error("Failed to mark ungated payload good", "version", entry.Version, "err", merr)
		}
		slog.Info("Ungated update payload passed the boot health gate", "version", entry.Version)
		return false
	} else if ctx.Err() != nil {
		// Shutting down — no verdict was reached, so do not record one. The
		// payload stays pending and is judged on the next start instead.
		return false
	}

	slog.Error("Ungated update payload failed the boot health gate; rolling back", "version", entry.Version)
	if merr := update.MarkState(updatesDir, entry.Version, update.StateFailed); merr != nil {
		slog.Error("Failed to mark ungated payload failed", "version", entry.Version, "err", merr)
		return false // the restart below would just pick the same binary again
	}
	if rerr := sup.Restart(ctx, healthTimeout); rerr != nil {
		slog.Error("Rollback restart after a failed boot health gate also failed", "err", rerr)
	}
	return true
}
