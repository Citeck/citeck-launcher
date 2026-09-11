package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
)

// The update routes answer the client and, until now, told the daemon log
// nothing. A user's Windows auto-update failed and was rolled back, and the
// only trace of the first attempt in their dump was
// `POST /desktop/update/apply → 500` with a duration and no reason — so the
// cause had to be guessed from the 502s on either side of it. Every genuine
// failure of these routes must name itself in the log.

// stageableDaemon builds a daemon whose updater can stage 2.6.0 cleanly, so a
// test can reach the wrapper-handoff half of handleUpdateApply.
func stageableDaemon(t *testing.T) *Daemon {
	t.Helper()
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "daemon-bin-2.6.0"},
	)
	svc := update.NewService("2.4.0", t.TempDir(),
		// Route behavior, not release signing (covered by signature_e2e_test.go).
		append(fake.Options(), update.WithSigningPublicKeyHex(""))...)
	return &Daemon{updateSvc: svc}
}

// TestUpdateApplyLogsWhyItFailed: a refused/failed stage leaves a line naming
// the reason, not just a 500 on the wire.
func TestUpdateApplyLogsWhyItFailed(t *testing.T) {
	d, _ := failedReleaseDaemon(t)
	buf := captureSyncSlog(t)

	rec := postApply(t, d, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "Update apply failed") {
		t.Fatalf("a failed apply left no log line:\n%s", out)
	}
	if !strings.Contains(out, "previously failed to apply") {
		t.Fatalf("the log line does not name the reason:\n%s", out)
	}
}

// TestUpdateApplyLogsAMissingWrapper: the payload staged fine and there is no
// wrapper to hand it to. That 503 is as invisible on the wire as the 500 was.
func TestUpdateApplyLogsAMissingWrapper(t *testing.T) {
	d := stageableDaemon(t)
	t.Setenv("CITECK_WRAPPER_SOCK", "")
	buf := captureSyncSlog(t)

	rec := postApply(t, d, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "Update apply cannot reach the desktop wrapper") {
		t.Fatalf("a wrapper-less apply left no log line:\n%s", out)
	}
	if !strings.Contains(out, "2.6.0") {
		t.Fatalf("the log line does not name the staged version:\n%s", out)
	}
}

// TestUpdateApplyLogsAWrapperHandoffFailure: the wrapper socket is set but
// nothing answers it — the swap never happens and the log must say so.
func TestUpdateApplyLogsAWrapperHandoffFailure(t *testing.T) {
	d := stageableDaemon(t)
	t.Setenv("CITECK_WRAPPER_SOCK", t.TempDir()+"/nothing-here.sock")
	buf := captureSyncSlog(t)

	rec := postApply(t, d, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if out := buf.String(); !strings.Contains(out, "Update apply could not hand the payload to the wrapper") {
		t.Fatalf("a failed wrapper handoff left no log line:\n%s", out)
	}
}

// deadUpdateDaemon points the updater at a port nothing listens on, which is
// what an unreachable GitHub looks like from inside the routes.
func deadUpdateDaemon(t *testing.T) *Daemon {
	t.Helper()
	dead := "http://127.0.0.1:1"
	return &Daemon{updateSvc: update.NewService("2.4.0", t.TempDir(), update.WithBaseURLs(dead, dead))}
}

// TestUpdateChangelogLogsWhyItFailed: the 502 the UI shows is the same 502 the
// dump showed, with the cause only on the client side.
func TestUpdateChangelogLogsWhyItFailed(t *testing.T) {
	d := deadUpdateDaemon(t)
	buf := captureSyncSlog(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/update/changelog?locale=en", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleUpdateChangelog(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if out := buf.String(); !strings.Contains(out, "Update changelog fetch failed") {
		t.Fatalf("a failed changelog fetch left no log line:\n%s", out)
	}
}

// TestUpdateCheckLogsWhyItFailed: the route deliberately answers 200 with the
// error only inside the status payload, so without a log line a user-pressed
// check that could not reach GitHub leaves no trace at all.
func TestUpdateCheckLogsWhyItFailed(t *testing.T) {
	d := deadUpdateDaemon(t)
	buf := captureSyncSlog(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/desktop/update/check", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleUpdateCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the route stays silent on the wire)", rec.Code)
	}
	if out := buf.String(); !strings.Contains(out, "Update check failed") {
		t.Fatalf("a failed check left no log line:\n%s", out)
	}
}

// TestUpdateRoutesStaySilentWhenNothingFailed guards the other half of the
// rule: these lines are per-failure, never per-request.
func TestUpdateRoutesStaySilentWhenNothingFailed(t *testing.T) {
	d := stageableDaemon(t)
	buf := captureSyncSlog(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/desktop/update/check", http.NoBody)
	d.handleUpdateCheck(httptest.NewRecorder(), req)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/update/status", http.NoBody)
	d.handleUpdateStatus(httptest.NewRecorder(), statusReq)

	for _, unwanted := range []string{"Update check failed", "Update apply failed", "Update changelog fetch failed"} {
		if strings.Contains(buf.String(), unwanted) {
			t.Fatalf("a successful request logged %q:\n%s", unwanted, buf.String())
		}
	}
}
