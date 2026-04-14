package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/bundlepicker"
)

func TestBuildPickerTabs_OrderedByBundleRepos(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "enterprise", Key: "2026.1", Ref: "enterprise:2026.1", Current: true},
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "community", Key: "2025.12", Ref: "community:2025.12"},
	}
	repos := []bundle.BundlesRepo{
		{ID: "community", Name: "Community Bundles"},
		{ID: "enterprise", Name: "Enterprise Bundles"},
	}
	tabs := buildPickerTabs(versions, repos)
	if len(tabs) != 2 {
		t.Fatalf("tabs=%d, want 2", len(tabs))
	}
	if tabs[0].ID != "community" || tabs[1].ID != "enterprise" {
		t.Errorf("tab order=%v, want [community enterprise]", []string{tabs[0].ID, tabs[1].ID})
	}
	if tabs[0].Name != "Community Bundles" {
		t.Errorf("tab name=%q, want display name from repo", tabs[0].Name)
	}
	// First version in each tab should be marked Latest.
	if !tabs[0].Versions[0].Latest {
		t.Error("first community version not marked Latest")
	}
	if !tabs[1].Versions[0].Latest {
		t.Error("first enterprise version not marked Latest")
	}
	// Current flag should be preserved on the right entry.
	found := false
	for _, v := range tabs[1].Versions {
		if v.Current && v.Ref == "enterprise:2026.1" {
			found = true
		}
	}
	if !found {
		t.Error("current flag lost on enterprise:2026.1")
	}
}

func TestBuildPickerTabs_UnknownRepoAppended(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "dev", Key: "2099.0", Ref: "dev:2099.0"},
	}
	repos := []bundle.BundlesRepo{
		{ID: "community", Name: "Community"},
	}
	tabs := buildPickerTabs(versions, repos)
	if len(tabs) != 2 {
		t.Fatalf("tabs=%d, want 2 (known + unknown)", len(tabs))
	}
	if tabs[0].ID != "community" || tabs[1].ID != "dev" {
		t.Errorf("unknown repo not appended after known; got %v", []string{tabs[0].ID, tabs[1].ID})
	}
	if tabs[1].Name != "dev" {
		t.Errorf("unknown repo should fall back to ID as name, got %q", tabs[1].Name)
	}
}

func TestBuildPickerTabs_SortsDescending(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "r", Key: "2025.11", Ref: "r:2025.11"},
		{Repo: "r", Key: "2026.1", Ref: "r:2026.1"},
		{Repo: "r", Key: "2025.12", Ref: "r:2025.12"},
	}
	tabs := buildPickerTabs(versions, []bundle.BundlesRepo{{ID: "r"}})
	want := []string{"2026.1", "2025.12", "2025.11"}
	got := make([]string, 0, len(tabs[0].Versions))
	for _, v := range tabs[0].Versions {
		got = append(got, v.Label)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("versions not sorted newest-first; got=%v want=%v", got, want)
			break
		}
	}
	// Only the first one should be marked Latest.
	for i, v := range tabs[0].Versions {
		if v.Latest != (i == 0) {
			t.Errorf("Latest mis-flagged on index %d (%q)", i, v.Label)
		}
	}
}

func TestBuildPickerTabs_Empty(t *testing.T) {
	var v []bundlepicker.Version
	_ = v // keep import used if rest of test evolves
	tabs := buildPickerTabs(nil, nil)
	if len(tabs) != 0 {
		t.Errorf("tabs=%v, want empty", tabs)
	}
}

func TestResolveUpgradeVersion_ExactRefMatch(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "enterprise", Key: "2026.1", Ref: "enterprise:2026.1"},
	}

	got, err := resolveUpgradeVersion("community:2026.1", versions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "community:2026.1" {
		t.Errorf("got %q, want %q", got, "community:2026.1")
	}
}

func TestResolveUpgradeVersion_BareVersionUnambiguous(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "community", Key: "2025.12", Ref: "community:2025.12"},
	}

	got, err := resolveUpgradeVersion("2025.12", versions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "community:2025.12" {
		t.Errorf("got %q, want %q", got, "community:2025.12")
	}
}

func TestResolveUpgradeVersion_BareVersionAmbiguous(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "enterprise", Key: "2026.1", Ref: "enterprise:2026.1"},
	}

	_, err := resolveUpgradeVersion("2026.1", versions)
	if err == nil {
		t.Fatal("expected error for ambiguous version, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguity, got: %v", err)
	}
	if !strings.Contains(err.Error(), "community:2026.1") ||
		!strings.Contains(err.Error(), "enterprise:2026.1") {
		t.Errorf("error should list both candidates, got: %v", err)
	}
}

func TestResolveUpgradeVersion_NotFound(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
	}

	_, err := resolveUpgradeVersion("bogus:9999.0", versions)
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

func TestResolveUpgradeVersion_BareVersionNotFound(t *testing.T) {
	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
	}

	_, err := resolveUpgradeVersion("9999.0", versions)
	if err == nil {
		t.Fatal("expected error for missing bare version, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

func TestResolveUpgradeVersion_EmptyList(t *testing.T) {
	_, err := resolveUpgradeVersion("community:2026.1", nil)
	if err == nil {
		t.Fatal("expected error for empty version list, got nil")
	}
	if !strings.Contains(err.Error(), "no bundle versions") {
		t.Errorf("error should mention empty list, got: %v", err)
	}
}

// TestSelectUpgradeTarget_NonTTYNoArgRefuses is the regression test for
// bug B5-F1: when stdin is NOT a TTY and no version arg is provided, the
// command must refuse with ExitConfigError (exit code 2) rather than
// silently auto-selecting a bundle (the `huh` library's default behavior).
func TestSelectUpgradeTarget_NonTTYNoArgRefuses(t *testing.T) {
	orig := stdinIsTTY
	t.Cleanup(func() { stdinIsTTY = orig })
	stdinIsTTY = func() bool { return false }

	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "community", Key: "2025.12", Ref: "community:2025.12"},
	}

	ref, err := selectUpgradeTarget("", versions, nil)
	if err == nil {
		t.Fatal("expected error when non-TTY and no arg, got nil (would auto-select!)")
	}
	if ref != "" {
		t.Errorf("expected empty ref on error, got %q", ref)
	}

	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if ece.Code != ExitConfigError {
		t.Errorf("expected exit code %d (ExitConfigError), got %d", ExitConfigError, ece.Code)
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Errorf("error should mention interactive terminal requirement, got: %v", err)
	}
}

// TestSelectUpgradeTarget_NonTTYWithArgProceeds verifies that providing an
// explicit version arg bypasses the TTY check entirely (the intended escape
// hatch for CI/automation).
func TestSelectUpgradeTarget_NonTTYWithArgProceeds(t *testing.T) {
	orig := stdinIsTTY
	t.Cleanup(func() { stdinIsTTY = orig })
	stdinIsTTY = func() bool { return false }

	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
		{Repo: "community", Key: "2025.12", Ref: "community:2025.12"},
	}

	ref, err := selectUpgradeTarget("community:2026.1", versions, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "community:2026.1" {
		t.Errorf("got %q, want %q", ref, "community:2026.1")
	}
}

// TestSelectUpgradeTarget_NonTTYInvalidArgReturnsConfigError verifies that an
// invalid arg in non-interactive mode yields a ConfigError (not a silent
// auto-selection).
func TestSelectUpgradeTarget_NonTTYInvalidArgReturnsConfigError(t *testing.T) {
	orig := stdinIsTTY
	t.Cleanup(func() { stdinIsTTY = orig })
	stdinIsTTY = func() bool { return false }

	versions := []bundle.VersionEntry{
		{Repo: "community", Key: "2026.1", Ref: "community:2026.1"},
	}

	_, err := selectUpgradeTarget("bogus:9.9", versions, nil)
	if err == nil {
		t.Fatal("expected error for unknown version arg, got nil")
	}
	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if ece.Code != ExitConfigError {
		t.Errorf("expected exit code %d, got %d", ExitConfigError, ece.Code)
	}
}

// TestShouldPromptUpgradeConfirm verifies the policy used by runUpgrade:
//   - interactive TTY without --yes  → prompt
//   - --yes given (any mode)         → skip (proceed)
//   - non-TTY (CI/scripts)           → skip (proceed — current scripted
//     behavior preserved)
func TestShouldPromptUpgradeConfirm(t *testing.T) {
	cases := []struct {
		name      string
		isTTY     bool
		assumeYes bool
		want      bool
	}{
		{"tty without --yes prompts", true, false, true},
		{"tty with --yes skips", true, true, false},
		{"non-tty without --yes skips (scripts)", false, false, false},
		{"non-tty with --yes skips", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPromptUpgradeConfirm(tc.isTTY, tc.assumeYes); got != tc.want {
				t.Errorf("shouldPromptUpgradeConfirm(isTTY=%v, assumeYes=%v)=%v, want %v",
					tc.isTTY, tc.assumeYes, got, tc.want)
			}
		})
	}
}

// TestUpgradeCmd_YesFlagWiring verifies that `citeck upgrade` registers
// both `--yes` (long) and `-y` (shorthand), both bound to the same global
// flagYes variable so the caller's intent is respected regardless of form.
func TestUpgradeCmd_YesFlagWiring(t *testing.T) {
	cmd := newUpgradeCmd()
	f := cmd.Flags().Lookup("yes")
	if f == nil {
		t.Fatal("--yes flag not registered on upgrade command")
	}
	if f.Shorthand != "y" {
		t.Errorf("--yes shorthand=%q, want %q", f.Shorthand, "y")
	}
	// Parse `-y` and confirm flagYes flips. Restore after the test so
	// other tests are not affected.
	orig := flagYes
	t.Cleanup(func() { flagYes = orig })
	flagYes = false
	if err := cmd.ParseFlags([]string{"-y"}); err != nil {
		t.Fatalf("parse -y: %v", err)
	}
	if !flagYes {
		t.Error("-y did not set flagYes=true")
	}

	flagYes = false
	cmd2 := newUpgradeCmd()
	if err := cmd2.ParseFlags([]string{"--yes"}); err != nil {
		t.Fatalf("parse --yes: %v", err)
	}
	if !flagYes {
		t.Error("--yes did not set flagYes=true")
	}
}

func TestStdinIsTTY_OverrideSeam(t *testing.T) {
	orig := stdinIsTTY
	t.Cleanup(func() { stdinIsTTY = orig })

	stdinIsTTY = func() bool { return false }
	if stdinIsTTY() {
		t.Fatal("seam override did not take effect")
	}

	stdinIsTTY = func() bool { return true }
	if !stdinIsTTY() {
		t.Fatal("seam override did not take effect")
	}
}

// TestRunUpgrade_NoTTYNoArg_FailFast verifies that runUpgrade refuses with
// ExitConfigError immediately when invoked without a version arg in a
// non-interactive context — BEFORE any side effects (namespace config load,
// git pull, "Updating..." output). This is the regression test for the
// MEDIUM-severity M1 finding: CI probes with non-TTY were otherwise getting
// git pull mutations and "Updating..." output before the usage error.
func TestRunUpgrade_NoTTYNoArg_FailFast(t *testing.T) {
	orig := stdinIsTTY
	t.Cleanup(func() { stdinIsTTY = orig })
	stdinIsTTY = func() bool { return false }

	// Empty versionArg + non-TTY → must short-circuit before any IO.
	// If the guard is missing, runUpgrade would call LoadNamespaceConfig,
	// which would return a different "load namespace config" error (or
	// succeed and proceed to git pull, which is the bug we're preventing).
	err := runUpgrade("")
	if err == nil {
		t.Fatal("expected error when non-TTY and no arg, got nil")
	}

	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError (guard), got %T: %v", err, err)
	}
	if ece.Code != ExitConfigError {
		t.Errorf("expected exit code %d (ExitConfigError), got %d", ExitConfigError, ece.Code)
	}
	if ece.Code != 2 {
		t.Errorf("ExitConfigError should equal 2, got %d", ece.Code)
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Errorf("error should mention interactive terminal requirement, got: %v", err)
	}
	// Confirm the guard fired — not some later error from config load
	// that happens to wrap an unrelated failure.
	if strings.Contains(err.Error(), "load namespace config") {
		t.Errorf("guard did not fire first; got later error: %v", err)
	}
}
