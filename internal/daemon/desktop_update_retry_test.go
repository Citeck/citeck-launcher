package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
)

// The apply route is the ONE place a failed release may be retried, and only
// when the caller says so: the user pressed "Try again". Anything that posts
// the route without asking (a stray refresh, a future automatic path) must keep
// hitting the loop guard.

// failedReleaseDaemon builds a daemon whose updater has 2.6.0 staged and then
// marked failed, exactly as the wrapper leaves it after a health-gate rollback.
func failedReleaseDaemon(t *testing.T) (d *Daemon, updatesDir string) {
	t.Helper()
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "daemon-bin-2.6.0"},
	)
	dir := t.TempDir()
	svc := update.NewService("2.4.0", dir,
		// Route behavior, not release signing (covered by signature_e2e_test.go).
		append(fake.Options(), update.WithSigningPublicKeyHex(""))...)
	if _, err := svc.Stage(t.Context()); err != nil {
		t.Fatalf("initial Stage: %v", err)
	}
	if err := update.MarkState(dir, "2.6.0", update.StateFailed); err != nil {
		t.Fatal(err)
	}
	// No wrapper in a unit test: staging is what we are measuring, and the
	// control-verb call after it answers 503 either way.
	t.Setenv("CITECK_WRAPPER_SOCK", "")
	return &Daemon{updateSvc: svc}, dir
}

func manifestState(t *testing.T, dir, version string) update.State {
	t.Helper()
	m, err := update.Load(dir)
	if err != nil {
		t.Fatalf("Load manifest: %v", err)
	}
	for _, e := range m.Entries {
		if e.Version == version {
			return e.State
		}
	}
	return ""
}

func postApply(t *testing.T, d *Daemon, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/desktop/update/apply"+query, http.NoBody)
	rec := httptest.NewRecorder()
	d.handleUpdateApply(rec, req)
	return rec
}

// TestApplyWithoutRetryRefusesAFailedRelease: the default is the loop guard.
func TestApplyWithoutRetryRefusesAFailedRelease(t *testing.T) {
	d, dir := failedReleaseDaemon(t)

	rec := postApply(t, d, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (the stage is refused)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "previously failed to apply") {
		t.Fatalf("body = %q; want the loop-guard refusal", rec.Body.String())
	}
	if st := manifestState(t, dir, "2.6.0"); st != update.StateFailed {
		t.Fatalf("entry state = %q; a refused apply must leave the record alone", st)
	}
	if d.updateSvc.Status().Available {
		t.Fatal("Available must stay false while the release is recorded failed")
	}
}

// TestApplyRetriesOnlyWhenAsked: ?retry=true is the user's explicit request and
// the only spelling that gets past the blacklist.
func TestApplyRetriesOnlyWhenAsked(t *testing.T) {
	d, dir := failedReleaseDaemon(t)

	// Not the opt-in: an absent value, and anything that is not "true", stays
	// on the refusing side.
	for _, q := range []string{"", "?retry=", "?retry=1", "?retry=yes"} {
		if rec := postApply(t, d, q); rec.Code != http.StatusInternalServerError {
			t.Fatalf("query %q: status = %d, want 500", q, rec.Code)
		}
		if st := manifestState(t, dir, "2.6.0"); st != update.StateFailed {
			t.Fatalf("query %q: entry state = %q, want it untouched", q, st)
		}
	}

	rec := postApply(t, d, "?retry=true")
	// The stage itself succeeded; with no wrapper socket the handler stops at
	// the control verb, which is the next step and not this route's subject.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d (%s), want 503 — the stage must have proceeded to the wrapper call",
			rec.Code, rec.Body.String())
	}
	if st := manifestState(t, dir, "2.6.0"); st != update.StatePending {
		t.Fatalf("entry state = %q, want %q: the retry re-staged the payload", st, update.StatePending)
	}
}
