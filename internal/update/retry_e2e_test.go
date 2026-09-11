package update_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
)

// A release that failed its health-gate is blacklisted so the machine cannot
// loop on it (download → apply → roll back → repeat). But the failure is just
// as often environmental — Docker not answering that afternoon, a boot that was
// merely slow — and before this the user had no way back to that release at
// all, by any means. The rule these tests pin is: the MACHINE may not retry a
// failed release, the USER may, and only by asking for it explicitly.

// stagedBinary is where Stage extracts a version's payload.
func stagedBinary(dir, version string) string {
	return filepath.Join(dir, version, update.DaemonBinaryName(runtime.GOOS))
}

// entryState returns the manifest state recorded for version ("" when absent).
func entryState(t *testing.T, dir, version string) update.State {
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

// newFailedRelease stages 2.6.0 against a fake GitHub and then marks it failed,
// exactly as the wrapper does after a health-gate rollback.
func newFailedRelease(t *testing.T) (*update.Service, *updatetest.FakeGitHub, string) {
	t.Helper()
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "daemon-bin-2.6.0",
			Notes: map[string]string{"en": "# 2.6.0"}},
	)
	dir := t.TempDir()
	svc := update.NewService("2.4.0", dir,
		// Staging-logic tests: disable the embedded production signing key
		// (signature behavior is covered by signature_e2e_test.go).
		append(fake.Options(), update.WithSigningPublicKeyHex(""))...)
	if _, err := svc.Stage(t.Context()); err != nil {
		t.Fatalf("initial Stage: %v", err)
	}
	if err := update.MarkState(dir, "2.6.0", update.StateFailed); err != nil {
		t.Fatal(err)
	}
	return svc, fake, dir
}

// TestAutomaticStageStillRefusesAFailedRelease keeps the loop guard intact: a
// Stage nobody asked for (the badge, any future automatic path) must still bounce
// off a version recorded failed, and the version must stay unoffered.
func TestAutomaticStageStillRefusesAFailedRelease(t *testing.T) {
	svc, _, dir := newFailedRelease(t)

	if _, err := svc.Stage(t.Context()); err == nil {
		t.Fatal("an automatic Stage must refuse a release already marked failed")
	}
	if st := svc.Status(); st.Available {
		t.Fatalf("status = %+v; a failed release must never be offered as available", st)
	}
	if got := entryState(t, dir, "2.6.0"); got != update.StateFailed {
		t.Fatalf("entry state = %q; a refused automatic Stage must leave the record alone", got)
	}
}

// TestExplicitRetryStagesAFailedReleaseAfresh is the user pressing "Try again".
// It must proceed past the blacklist AND re-stage from scratch: the previous
// attempt's directory and manifest entry are from a run we already know ended
// badly, so nothing of it may be carried into the new attempt.
func TestExplicitRetryStagesAFailedReleaseAfresh(t *testing.T) {
	svc, _, dir := newFailedRelease(t)

	// The leftover on disk is unverified as far as this attempt is concerned:
	// mark it so reuse is visible rather than plausible.
	bin := stagedBinary(dir, "2.6.0")
	if err := os.WriteFile(bin, []byte("stale-unverified-leftover"), 0o755); err != nil { //nolint:gosec // test payload
		t.Fatal(err)
	}
	// The badge stays dark while the record says "failed" — the retry offer
	// rides on ApplyError, not on Available.
	if st := svc.Status(); st.Available {
		t.Fatalf("status = %+v; Available must stay false for a failed version", st)
	}
	if svc.Status().ApplyError != "2.6.0" {
		t.Fatalf("ApplyError = %q; the UI's retry offer rides on it", svc.Status().ApplyError)
	}

	version, err := svc.Stage(t.Context(), update.UserRetry())
	if err != nil {
		t.Fatalf("an explicit user retry must proceed: %v", err)
	}
	if version != "2.6.0" {
		t.Fatalf("staged version = %q, want 2.6.0", version)
	}

	got, err := os.ReadFile(bin) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read staged binary: %v", err)
	}
	if string(got) != "daemon-bin-2.6.0" {
		t.Fatalf("staged payload = %q; the retry must re-download, never reuse the leftover", got)
	}
	// The entry that was `failed` is handed back to the swap to judge again.
	if state := entryState(t, dir, "2.6.0"); state != update.StatePending {
		t.Fatalf("entry state = %q, want %q after an explicit retry", state, update.StatePending)
	}
	if _, ok := update.SelectBest(dir, "2.4.0"); !ok {
		t.Fatal("the re-staged payload must be selectable by the supervisor")
	}
}

// TestExplicitRetryReVerifiesAndDropsTheOldPayload: the retry is a full
// download + sha256 (+ signature) pass, not a re-apply of what is lying there.
// When THIS attempt fails verification, the old payload must not survive to be
// swapped in by the back door.
func TestExplicitRetryReVerifiesAndDropsTheOldPayload(t *testing.T) {
	svc, fake, dir := newFailedRelease(t)
	bin := stagedBinary(dir, "2.6.0")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("precondition: the failed attempt left a payload on disk: %v", err)
	}

	// Republish the same version with a checksum that does not match.
	fake.SetRelease(updatetest.Release{Version: "2.6.0", Date: "2026-06-01",
		BinaryContent: "daemon-bin-2.6.0", Notes: map[string]string{"en": "# 2.6.0"}, CorruptSHA: true})

	if _, err := svc.Stage(t.Context(), update.UserRetry()); err == nil {
		t.Fatal("a retry must re-verify: a checksum mismatch has to fail the stage")
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("stat leftover = %v; a retry that failed verification must not leave the old payload behind", err)
	}
	if _, ok := update.SelectBest(dir, "2.4.0"); ok {
		t.Fatal("a retry that failed verification must leave nothing selectable")
	}
}
