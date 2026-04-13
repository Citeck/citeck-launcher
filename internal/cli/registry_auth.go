package cli

import (
	"strings"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/output"
)

// dockerRegistryLoginFunc is a package-level seam so tests can stub the Docker
// registry login probe without a live Docker daemon.
var dockerRegistryLoginFunc = dockerRegistryLogin

// checkRegistryAuthForBundle verifies credentials for all authType:BASIC image
// registries used by the given bundle ref. On missing or invalid credentials
// it prompts the user (interactive TTY only). Saved credentials are probed
// via dockerRegistryLoginFunc — if the probe fails the user is re-prompted.
//
// Non-TTY behavior: when credentials are missing or invalid, returns an
// ExitConfigError with a clear message (never silently proceeds).
//
// Scope restriction: only repos used by the given bundle ref are checked.
func checkRegistryAuthForBundle(ref bundle.Ref) error {
	resolver := bundle.NewResolver(config.DataDir())
	resolver.SetOffline(true) // don't trigger git pull
	wsCfg := resolver.ResolveWorkspaceOnly()

	usedIDs := bundleImageRepoIDs(ref, wsCfg)
	authRepos := findAuthRepos(wsCfg, usedIDs)
	if len(authRepos) == 0 {
		return nil
	}

	svc, svcErr := openSecretService()
	if svcErr != nil {
		// Can't access the secret store — daemon will surface the problem
		// at runtime. Don't block the CLI path here.
		return nil
	}

	ensureI18n()

	// Classify each auth repo: OK / missing / invalid.
	type repoState struct {
		repo   bundle.ImageRepo
		reason string // "" = OK; otherwise a short reason for prompt header
	}
	states := make([]repoState, 0, len(authRepos))
	for _, repo := range authRepos {
		sec, _ := svc.GetSecret("registry-" + repo.ID)
		if sec == nil || sec.Value == "" {
			states = append(states, repoState{repo: repo, reason: "missing"})
			continue
		}
		user, pass, ok := strings.Cut(sec.Value, ":")
		if !ok || user == "" || pass == "" {
			states = append(states, repoState{repo: repo, reason: "malformed"})
			continue
		}
		host := registryHost(repo.URL)
		if loginErr := dockerRegistryLoginFunc(host, user, pass); loginErr != nil {
			output.PrintText("Saved credentials for %s no longer work (login failed: %v). Please re-enter.", host, loginErr)
			states = append(states, repoState{repo: repo, reason: "invalid"})
			continue
		}
	}

	// Collect repos that still need credentials.
	var needPrompt []bundle.ImageRepo
	for _, st := range states {
		if st.reason != "" {
			needPrompt = append(needPrompt, st.repo)
		}
	}
	if len(needPrompt) == 0 {
		return nil
	}

	// Non-TTY: refuse with a clear error.
	if !output.IsTTY() {
		hosts := make([]string, 0, len(needPrompt))
		for _, r := range needPrompt {
			hosts = append(hosts, registryHost(r.URL))
		}
		return exitWithCode(
			ExitConfigError,
			"registry credentials required for %s (missing or invalid).\n"+
				"Run this command in an interactive terminal, or pre-configure credentials via `citeck install`.",
			strings.Join(hosts, ", "),
		)
	}

	for _, repo := range needPrompt {
		host := registryHost(repo.URL)
		output.PrintText("%s: %s", t("install.registry.host"), host)
		for {
			username := promptInput(t("install.registry.username"), "", "")
			if username == "" {
				return exitWithCode(ExitConfigError, "registry credentials required for %s\n\nConfigure via 'citeck install' or provide credentials", host)
			}
			password := promptPassword(t("install.registry.password"))
			if password == "" {
				continue
			}

			output.PrintText("  %s", t("install.registry.checking"))
			if loginErr := dockerRegistryLoginFunc(host, username, password); loginErr != nil {
				output.Errf("  %s: %v", t("install.registry.failed"), loginErr)
				continue // retry
			}
			output.PrintText("  %s", t("install.registry.success"))

			if saveErr := saveRegistrySecret(svc, repo, username, password); saveErr != nil {
				output.Errf("  %s: %v", t("install.registry.saveFailed"), saveErr)
			}
			break
		}
	}
	return nil
}
