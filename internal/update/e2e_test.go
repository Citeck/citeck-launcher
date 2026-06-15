package update_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
)

// TestAutoUpdateE2E_DiscoverChangelogStageSelect drives the full GitHub-facing
// auto-update flow through the public update.Service API against a fake GitHub:
// discover latest → assemble the (current, latest] changelog (locale + en
// fallback) → download + verify sha256 + extract the payload → confirm the staged
// binary is what the supervisor's SelectBest would pick.
func TestAutoUpdateE2E_DiscoverChangelogStageSelect(t *testing.T) {
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.5.0", Date: "2026-03-01", BinaryContent: "daemon-bin-2.5.0",
			Notes: map[string]string{"en": "# 2.5.0\n- older release"}},
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "daemon-bin-2.6.0",
			Notes: map[string]string{"en": "# 2.6.0\n- newest", "ru": "# 2.6.0\n- новейшее"}},
	)

	dir := t.TempDir()
	svc := update.NewService("2.4.0", dir,
		// Staging-logic tests: disable the embedded production signing key
		// (signature behavior is covered by signature_e2e_test.go).
		append(fake.Options(), update.WithSigningPublicKeyHex(""))...)
	ctx := t.Context()

	// 1. Discovery — nothing offered before the first check.
	if svc.Status().Available {
		t.Fatal("available should be false before the first check")
	}
	latest, err := svc.CheckLatest(ctx)
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if latest.Version != "2.6.0" || latest.Tag != "v2.6.0" {
		t.Fatalf("latest = %+v, want 2.6.0 / v2.6.0", latest)
	}
	if st := svc.Status(); !st.Available || st.LatestVersion != "2.6.0" {
		t.Fatalf("status = %+v, want available latest 2.6.0", st)
	}

	// 2. Changelog — (2.4.0, 2.6.0] newest-first; ru for 2.6.0, en fallback for 2.5.0.
	notes, err := svc.Changelog(ctx, "ru")
	if err != nil {
		t.Fatalf("Changelog: %v", err)
	}
	if len(notes) != 2 || notes[0].Version != "2.6.0" || notes[1].Version != "2.5.0" {
		t.Fatalf("notes = %+v, want [2.6.0, 2.5.0]", notes)
	}
	if !strings.Contains(notes[0].Markdown, "новейшее") {
		t.Fatalf("2.6.0 note should be the ru file: %q", notes[0].Markdown)
	}
	if !strings.Contains(notes[1].Markdown, "older release") {
		t.Fatalf("2.5.0 note should fall back to en: %q", notes[1].Markdown)
	}

	// 3. Stage — real download → sha256 verify → tar.gz extract → manifest pending.
	ver, err := svc.Stage(ctx)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if ver != "2.6.0" {
		t.Fatalf("staged %q, want 2.6.0", ver)
	}
	staged := filepath.Join(dir, "2.6.0", "citeck")
	got, err := os.ReadFile(staged) //nolint:gosec // test path
	if err != nil || string(got) != "daemon-bin-2.6.0" {
		t.Fatalf("staged binary content = %q err=%v", got, err)
	}

	// 4. The staged payload is exactly what the supervisor's SelectBest picks.
	best, ok := update.SelectBest(dir, "2.4.0")
	if !ok || best != staged {
		t.Fatalf("SelectBest = %q ok=%v, want %q", best, ok, staged)
	}
}

// TestAutoUpdateE2E_RejectsCorruptChecksum confirms a tampered payload (wrong
// sha256 sidecar) is rejected and leaves nothing selectable.
func TestAutoUpdateE2E_RejectsCorruptChecksum(t *testing.T) {
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "x", CorruptSHA: true,
			Notes: map[string]string{"en": "# 2.6.0"}},
	)
	dir := t.TempDir()
	svc := update.NewService("2.4.0", dir,
		// Staging-logic tests: disable the embedded production signing key
		// (signature behavior is covered by signature_e2e_test.go).
		append(fake.Options(), update.WithSigningPublicKeyHex(""))...)

	if _, err := svc.Stage(t.Context()); err == nil {
		t.Fatal("Stage must reject a corrupt sha256 sidecar")
	}
	if _, ok := update.SelectBest(dir, "2.4.0"); ok {
		t.Fatal("a rejected stage must leave no selectable payload")
	}
}

// TestAutoUpdateE2E_SuppressesFailedRelease confirms a release that failed its
// health-gate (marked failed by the wrapper after a rollback) is neither offered
// nor re-applied until a newer one appears.
func TestAutoUpdateE2E_SuppressesFailedRelease(t *testing.T) {
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "daemon",
			Notes: map[string]string{"en": "# 2.6.0"}},
	)
	dir := t.TempDir()
	svc := update.NewService("2.4.0", dir,
		// Staging-logic tests: disable the embedded production signing key
		// (signature behavior is covered by signature_e2e_test.go).
		append(fake.Options(), update.WithSigningPublicKeyHex(""))...)
	ctx := t.Context()

	if _, err := svc.Stage(ctx); err != nil {
		t.Fatalf("initial Stage: %v", err)
	}
	// Simulate the wrapper marking the swapped daemon failed after a rollback.
	if err := update.MarkState(dir, "2.6.0", update.StateFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CheckLatest(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.Status().Available {
		t.Fatal("a failed release must not be offered again")
	}
	if _, err := svc.Stage(ctx); err == nil {
		t.Fatal("Stage must refuse a release already marked failed")
	}
}
