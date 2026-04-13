package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/bundlepicker"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// stdinIsTTY is a package-level seam so tests can override TTY detection.
var stdinIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// upgradeNoTTYMsg is returned when `citeck upgrade` is invoked without a
// version arg in a non-interactive context. The `huh` library silently
// degrades in non-TTY mode and would auto-select the first option — that
// would swap the bundle without user consent.
const upgradeNoTTYMsg = "`citeck upgrade` requires an interactive terminal or an explicit version.\n" +
	"Usage:\n" +
	"  citeck upgrade                        # interactive picker (TTY required)\n" +
	"  citeck upgrade <bundle>:<version>     # non-interactive (e.g., citeck upgrade community:2026.1)"

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade [bundle:version]",
		Short: "Upgrade to a different bundle version",
		Long: `Update workspace repos and upgrade to a different bundle version.

Usage:
  citeck upgrade                        # interactive picker (TTY required)
  citeck upgrade <bundle>:<version>     # non-interactive (e.g., citeck upgrade community:2026.1)

Steps:
  1. Pull latest workspace/bundle definitions from git (skipped for offline installs)
  2. Show available releases with the current version marked (interactive only)
  3. Confirm the upgrade (interactive only; skip with --yes/-y)
  4. Apply selected version and reload`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureI18n()
			arg := ""
			if len(args) == 1 {
				arg = strings.TrimSpace(args[0])
			}
			return runUpgrade(arg)
		},
	}
	// Local shadow of the persistent --yes flag to add a `-y` shorthand.
	// Both forms (the persistent parent flag and this local alias) write to
	// the same `flagYes` variable, so usage is consistent wherever it is
	// placed on the command line.
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func runUpgrade(versionArg string) error {
	// Fail fast in non-TTY without arg, before any side effects (git pull,
	// "Updating..." output). CI probes otherwise see a misleading partial
	// run before the usage error.
	if versionArg == "" && !stdinIsTTY() {
		return exitWithCode(ExitConfigError, "%s", upgradeNoTTYMsg)
	}

	// Load current config to know the active bundleRef
	nsCfg, err := namespace.LoadNamespaceConfig(config.NamespaceConfigPath())
	if err != nil {
		return fmt.Errorf("load namespace config: %w", err)
	}

	resolver := bundle.NewResolver(config.DataDir())
	wsCfg := resolver.ResolveWorkspaceOnly()

	// Step 1: update repos (skip if offline — no git URLs configured)
	hasGitRepos := false
	for _, repo := range wsCfg.BundleRepos {
		if repo.URL != "" {
			hasGitRepos = true
			break
		}
	}
	if hasGitRepos {
		output.PrintText("Updating bundle definitions...")
		if updateErr := runUpdateFromGit(); updateErr != nil {
			output.PrintText("Warning: update failed: %v", updateErr)
		}
		// Note: ListAllVersions will re-resolve workspace internally
	}

	// Step 2: collect all versions from all bundle repos
	currentRef := nsCfg.BundleRef.String()
	versions := resolver.ListAllVersions(currentRef)

	if len(versions) == 0 {
		output.PrintText("No bundle versions available.")
		return nil
	}

	// Sort descending (newest first)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Ref > versions[j].Ref
	})

	targetRef, selectErr := selectUpgradeTarget(versionArg, versions, wsCfg.BundleRepos)
	if selectErr != nil {
		return selectErr
	}
	if targetRef == "" {
		// User canceled the interactive picker.
		return nil
	}

	if targetRef == currentRef {
		output.PrintText("Already on %s — no changes.", currentRef)
		return nil
	}

	// Verify registry credentials for the TARGET bundle before starting the
	// upgrade. If the target pulls from a different registry than the current
	// bundle (cross-repo switch), we may need to prompt for new credentials.
	// Also re-probes saved credentials to catch expired / rotated passwords.
	parsedTarget, parseErr := bundle.ParseRef(targetRef)
	if parseErr != nil {
		return fmt.Errorf("parse target ref %q: %w", targetRef, parseErr)
	}
	if authErr := checkRegistryAuthForBundle(parsedTarget); authErr != nil {
		return authErr
	}

	// Final confirmation before we start changing state. Skipped on non-TTY
	// (scripts) and when --yes/-y was passed. Default is "Yes" — the user
	// already went through picker + auth; this is a "last chance", not a
	// hard gate. Upgrade is not destructive (you can always downgrade back).
	if shouldPromptUpgradeConfirm(stdinIsTTY(), flagYes) {
		prompt := t("upgrade.confirm.prompt", "from", currentRef, "to", targetRef)
		if !promptConfirm(prompt, true) {
			output.PrintText("%s", t("upgrade.canceled"))
			return nil
		}
	}

	// Step 3: apply via daemon
	c, cErr := client.New(clientOpts())
	if cErr != nil {
		return fmt.Errorf("connect to daemon: %w", cErr)
	}
	defer c.Close()

	result, err := c.UpgradeNamespace(targetRef)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	output.PrintResult(result, func() {
		output.PrintText(result.Message)
	})

	// Wait for reload
	if waitErr := StreamReloadStatus(c); waitErr != nil {
		return waitErr
	}
	return nil
}

// selectUpgradeTarget picks the target bundle ref either from an explicit
// arg (non-interactive) or via the interactive tabbed picker. Returns an
// empty string (with nil error) when the user cancels the picker.
//
// bundleRepos is used to preserve tab order and display names (from the
// workspace config). Repos missing from bundleRepos but present in versions
// are appended after the known repos in first-seen order.
func selectUpgradeTarget(versionArg string, versions []bundle.VersionEntry, bundleRepos []bundle.BundlesRepo) (string, error) {
	if versionArg != "" {
		resolved, err := resolveUpgradeVersion(versionArg, versions)
		if err != nil {
			return "", exitWithCode(ExitConfigError, "%s", err.Error())
		}
		return resolved, nil
	}

	// No arg: refuse if stdin is not a TTY. `runUpgrade` also checks this
	// up front (fail-fast, before side effects), but keep the guard here
	// too so direct callers of `selectUpgradeTarget` stay safe.
	if !stdinIsTTY() {
		return "", exitWithCode(ExitConfigError, "%s", upgradeNoTTYMsg)
	}

	tabs := buildPickerTabs(versions, bundleRepos)
	ref, ok, pickErr := bundlepicker.Pick(
		t("install.release.label"),
		tabs,
		pickerHints(),
	)
	if pickErr != nil {
		return "", fmt.Errorf("show picker: %w", pickErr)
	}
	if !ok {
		return "", nil
	}
	return ref, nil
}

// buildPickerTabs groups version entries into tabs, ordered by bundleRepos.
// Repos seen in versions but missing from bundleRepos are appended in
// first-seen order. Each tab's versions preserve the input order (expected
// newest-first). The newest version in a tab is marked Latest.
func buildPickerTabs(versions []bundle.VersionEntry, bundleRepos []bundle.BundlesRepo) []bundlepicker.Tab {
	byRepo := make(map[string][]bundle.VersionEntry)
	order := make([]string, 0, len(versions))
	for _, v := range versions {
		if _, ok := byRepo[v.Repo]; !ok {
			order = append(order, v.Repo)
		}
		byRepo[v.Repo] = append(byRepo[v.Repo], v)
	}

	seen := make(map[string]bool, len(bundleRepos))
	names := make(map[string]string, len(bundleRepos))
	orderedIDs := make([]string, 0, len(bundleRepos)+len(order))
	for _, repo := range bundleRepos {
		names[repo.ID] = repo.Name
		if _, has := byRepo[repo.ID]; has {
			orderedIDs = append(orderedIDs, repo.ID)
			seen[repo.ID] = true
		}
	}
	// Append any repos present in versions but not in bundleRepos
	// (e.g. stale data on disk) so we never hide available options.
	for _, id := range order {
		if !seen[id] {
			orderedIDs = append(orderedIDs, id)
		}
	}

	tabs := make([]bundlepicker.Tab, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		entries := byRepo[id]
		if len(entries) == 0 {
			continue
		}
		// Sort descending (newest first) in case caller did not.
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Key > entries[j].Key
		})
		vs := make([]bundlepicker.Version, 0, len(entries))
		for i, e := range entries {
			vs = append(vs, bundlepicker.Version{
				Ref:     e.Ref,
				Label:   e.Key,
				Current: e.Current,
				Latest:  i == 0,
			})
		}
		name := names[id]
		if name == "" {
			name = id
		}
		tabs = append(tabs, bundlepicker.Tab{
			ID:       id,
			Name:     name,
			Versions: vs,
		})
	}
	return tabs
}

// pickerHints returns translated KeyHints for the tabbed picker.
// When a translation is missing, the picker falls back to English defaults.
func pickerHints() bundlepicker.KeyHints {
	return bundlepicker.KeyHints{
		SwitchTab: tOrEmpty("picker.hint.switchTab"),
		Move:      tOrEmpty("picker.hint.move"),
		Select:    tOrEmpty("picker.hint.select"),
		Cancel:    tOrEmpty("picker.hint.cancel"),
		Latest:    tOrEmpty("install.release.latest"),
		Current:   tOrEmpty("picker.marker.current"),
	}
}

// tOrEmpty translates a key but returns "" when the key is missing (the
// i18n layer returns the key itself on a miss, which we treat as "use the
// caller's default").
func tOrEmpty(key string) string {
	v := t(key)
	if v == key {
		return ""
	}
	return v
}

// shouldPromptUpgradeConfirm encodes the policy for the pre-apply confirmation
// in runUpgrade: only prompt when stdin is interactive AND --yes/-y was not set.
// Non-TTY contexts (scripts/CI) skip the prompt and proceed so automation
// keeps working; --yes also skips.
func shouldPromptUpgradeConfirm(isTTY, assumeYes bool) bool {
	return isTTY && !assumeYes
}

// resolveUpgradeVersion validates an explicit version arg against the list of
// available bundle versions. Accepts either "<bundle>:<version>" (exact ref)
// or just "<version>" when the version is unambiguous across repos.
func resolveUpgradeVersion(arg string, versions []bundle.VersionEntry) (string, error) {
	if len(versions) == 0 {
		return "", fmt.Errorf("no bundle versions available")
	}

	// Exact "bundle:version" match
	if strings.Contains(arg, ":") {
		for _, v := range versions {
			if v.Ref == arg {
				return v.Ref, nil
			}
		}
		return "", fmt.Errorf("bundle version %q not found; run `citeck upgrade` without args on an interactive terminal to see available versions", arg)
	}

	// Bare version — only accept if unambiguous across repos.
	var matches []bundle.VersionEntry
	for _, v := range versions {
		if v.Key == arg {
			matches = append(matches, v)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("bundle version %q not found; run `citeck upgrade` without args on an interactive terminal to see available versions", arg)
	case 1:
		return matches[0].Ref, nil
	default:
		refs := make([]string, 0, len(matches))
		for _, m := range matches {
			refs = append(refs, m.Ref)
		}
		sort.Strings(refs)
		return "", fmt.Errorf("bundle version %q is ambiguous, specify <bundle>:<version> (candidates: %s)", arg, strings.Join(refs, ", "))
	}
}
