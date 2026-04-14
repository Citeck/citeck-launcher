package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/bundlepicker"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
	"github.com/docker/docker/api/types/registry"
	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
)

func newInstallCmd(info BuildInfo) *cobra.Command {
	var workspaceZip string
	var offline bool
	var rollback bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Interactive server installer",
		Long: `Set up a Citeck platform deployment: namespace config, TLS, systemd service, firewall.

When invoked from a binary outside /usr/local/bin/citeck, this command also
handles the binary lifecycle itself: fresh install, upgrade (with zero-downtime
platform preservation), and rollback. The install.sh one-liner is a thin
wrapper that just fetches the latest release and hands off to this command.

Use --workspace to import a workspace zip archive (e.g. downloaded from GitHub/GitLab).
This extracts workspace config and bundle definitions for offline operation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rollback {
				ensureI18n()
				return runRollback()
			}
			err := runInstall(info, workspaceZip, offline)
			if errors.Is(err, ErrInstallCancelled) {
				// Unified cancel path — whether the user pressed Esc on the
				// welcome Note, the hostname Input, the bundle picker, or
				// any other prompt, we exit with SIGINT's conventional 130
				// so wrapping shell scripts can reliably distinguish
				// "user aborted" from "install completed".
				ensureI18n()
				fmt.Fprintln(os.Stderr, t("install.canceled")) //nolint:forbidigo // terminal exit message
				os.Exit(130)
			}
			return err
		},
	}

	cmd.Flags().StringVar(&workspaceZip, "workspace", "", "Path to workspace zip archive (offline bundle import)")
	cmd.Flags().BoolVar(&offline, "offline", false, "Offline mode: skip network checks (Let's Encrypt), use only local data")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "Restore the previous binary from .bak and restart the daemon")

	return cmd
}

func runInstall(info BuildInfo, workspaceZip string, offline bool) (retErr error) { //nolint:gocyclo // interactive wizard with sequential steps
	// On a clean exit (no error returned anywhere below), remove the
	// installer cache file that install.sh handed us. On error we leave
	// it in place so the next install.sh run reuses the already-downloaded
	// binary instead of re-fetching from GitHub.
	//
	// NOTE: deferred cleanup does NOT run on syscall.Exec (reExecAtTarget)
	// because exec replaces the process image. For the fresh-install path,
	// the re-execed process inherits CITECK_INSTALLER_CACHE via os.Environ()
	// and its own defer here fires after the wizard completes.
	defer func() {
		if retErr == nil {
			cleanupInstallerCacheOnSuccess()
		}
	}()

	// Installer bootstrap: if self is outside /usr/local/bin/citeck, we're
	// the installer running from a download/temp location. handleInstallerLifecycle
	// takes over for fresh install, upgrade, or no-op depending on the
	// installed version; handled=false means we fall through to the wizard
	// below (the normal "already at target" case).
	if handled, err := handleInstallerLifecycle(info); handled {
		return err
	}

	// Import workspace zip if provided
	if workspaceZip != "" {
		if _, statErr := os.Stat(workspaceZip); statErr != nil {
			return fmt.Errorf("workspace archive not found: %s", workspaceZip)
		}
		destDir := filepath.Join(config.DataDir(), "repo")
		if err := os.MkdirAll(filepath.Dir(destDir), 0o750); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
		// Remove existing repo dir if present
		_ = os.RemoveAll(destDir)
		if err := os.MkdirAll(destDir, 0o750); err != nil {
			return fmt.Errorf("create repo dir: %w", err)
		}
		count, err := extractZip(workspaceZip, destDir)
		if err != nil {
			return fmt.Errorf("extract workspace: %w", err)
		}
		output.PrintText("Workspace imported: %d files extracted to %s", count, destDir)
	}

	// Check Docker is available
	dockerConn, err := net.DialTimeout("unix", "/var/run/docker.sock", 2*time.Second)
	if err != nil {
		return fmt.Errorf("docker is not reachable at /var/run/docker.sock — install Docker first: https://docs.docker.com/engine/install/")
	}
	dockerConn.Close()

	// Check if already installed (both config files must exist — partial install is re-runnable)
	nsCfgPath := config.NamespaceConfigPath()
	_, nsExists := os.Stat(nsCfgPath)
	_, daemonExists := os.Stat(config.DaemonConfigPath())
	if nsExists == nil && daemonExists == nil {
		ensureI18n()
		ver := info.Version
		if ver == "" {
			ver = "dev"
		}
		label := ver
		if ver != "dev" && !strings.HasPrefix(ver, "v") {
			label = "v" + ver
		}
		if info.BuildDate != "" && info.BuildDate != "unknown" {
			label += " (" + info.BuildDate + ")"
		}
		fmt.Println() //nolint:forbidigo // CLI output
		output.PrintText("   %s %s", output.Colorize(output.Green, t("install.alreadyInstalled")),
			output.Colorize(output.Dim, label))
		fmt.Println() //nolint:forbidigo // CLI output
		output.PrintText("   %s %s", t("install.setupHint"), output.Colorize(output.Cyan, "citeck setup"))
		fmt.Println() //nolint:forbidigo // CLI output
		return nil
	}

	// --- Step 1: Language (first, so welcome is localized; numbering matches quick_start.rst) ---
	langOptions := make([]string, len(SupportedLocales))
	for i, loc := range SupportedLocales {
		langOptions[i] = loc.Code + " (" + loc.Name + ")"
	}
	fmt.Println() //nolint:forbidigo // CLI output
	locale := promptSelect("Language / Язык / 语言", langOptions)
	localeCode := strings.SplitN(locale, " ", 2)[0]
	initI18n(localeCode)

	// --- Step 2: Welcome (in selected language) ---
	//
	// Use prompt.Note for the "press Enter to continue" gate rather than
	// fmt.Scanln — the latter sometimes reads a phantom newline left over
	// from the Enter keypress that confirmed the preceding Language Select,
	// and the welcome screen is skipped without user input. prompt.Note
	// shares the same raw-mode input discipline as the Select above and
	// waits reliably for a fresh Enter.
	welcomeBody := fmt.Sprintf(
		"%s\n\n%s\n  1. %s  -> %s\n  2. %s  -> %s\n  3. %s\n  4. %s\n\n%s",
		t("install.welcome.subtitle"),
		t("install.welcome.whatWillHappen"),
		t("install.welcome.stepConfig"), nsCfgPath,
		t("install.welcome.stepDaemon"), config.DaemonConfigPath(),
		t("install.welcome.stepService"),
		t("install.welcome.stepStart"),
		t("install.welcome.canChange"),
	)
	if err := (&prompt.Note{
		Title:       t("install.welcome.title"),
		Description: welcomeBody,
		NextLabel:   t("install.welcome.pressEnter"),
		Hints:       i18nHints(),
	}).Run(); err != nil {
		if errors.Is(err, prompt.ErrCanceled) {
			return ErrInstallCancelled
		}
		return fmt.Errorf("welcome: %w", err)
	}

	nsCfg := namespace.DefaultNamespaceConfig()
	nsCfg.Template = "default"    // links to workspace template for detachedApps on first start
	nsCfg.PgAdmin.Enabled = false // default off (use pgAdmin separately if needed)
	isOffline := offline || workspaceZip != ""

	defaultHost := config.DetectOutboundIP(isOffline)
	var hostname string

hostStep:
	// --- Step 3: Hostname --- (1. Language + 2. Welcome precede this; numbering matches quick_start.rst)
	for {
		hostname = promptInput(t("install.hostname.label"), t("install.hostname.hint"), defaultHost)
		if hostname != "" {
			break
		}
	}
	if hostname == "localhost" || hostname == "127.0.0.1" {
		output.PrintText("   %s", t("install.hostname.localOnly"))
	}
	nsCfg.Proxy.Host = hostname
	nsCfg.Name = "Citeck"

	// --- Step 4: TLS ---
	isLocalhost := hostname == "localhost" || hostname == "127.0.0.1"
	var tlsOptions []string
	if !isLocalhost && !isOffline {
		tlsOptions = []string{t("install.tls.auto"), t("install.tls.leTrusted"), t("install.tls.httpsAutoGen"), t("install.tls.custom"), t("install.tls.httpOnly")}
	} else {
		tlsOptions = []string{t("install.tls.httpsAutoGen"), t("install.tls.custom"), t("install.tls.httpOnly")}
	}
	for {
		result := configureTLS(&nsCfg, promptSelect(t("install.tls.label"), tlsOptions))
		if result == tlsOK {
			break
		}
		if result == tlsChangeHost {
			defaultHost = hostname // pre-fill with current value
			goto hostStep
		}
		// tlsBack — re-show TLS menu
	}

	// Port: 443 for HTTPS, 80 for HTTP
	port := 443
	if !nsCfg.Proxy.TLS.Enabled {
		port = 80
	}
	nsCfg.Proxy.Port = port

	// Authentication: always Keycloak
	nsCfg.Authentication.Type = namespace.AuthKeycloak

	// --- Step 5: Release + registry auth (registry is a conditional sub-step) ---
	for {
		if err := resolveRelease(&nsCfg, isOffline); err != nil {
			return err
		}

		// Registry credentials sub-step (only for registries used by the selected bundle)
		wsResolver := bundle.NewResolver(config.DataDir())
		wsResolver.SetOffline(true)
		wsCfg := wsResolver.ResolveWorkspaceOnly()
		if wsCfg != nil {
			usedPrefixes := bundleImageRepoIDs(nsCfg.BundleRef, wsCfg)
			if err := configureRegistryAuth(wsCfg, usedPrefixes); err != nil {
				if errors.Is(err, errBackToRelease) {
					continue // re-show release selection
				}
				return err
			}
		}

		// --- Step 6: Snapshot selection (optional demo data) ---
		if wsCfg != nil && len(wsCfg.Snapshots) > 0 {
			nsCfg.Snapshot = selectSnapshot(wsCfg.Snapshots)
		}

		break
	}

	// --- Step 7: Save configuration (automatic — no prompt) ---
	printDoneTitle(t("install.config.label"))
	if err := os.MkdirAll(filepath.Dir(nsCfgPath), 0o755); err != nil { //nolint:gosec // G301: namespace config dir needs 0o755
		return fmt.Errorf("create config dir: %w", err)
	}
	data, marshalErr := namespace.MarshalNamespaceConfig(&nsCfg)
	if marshalErr != nil {
		return fmt.Errorf("marshal config: %w", marshalErr)
	}
	if writeErr := fsutil.AtomicWriteFile(nsCfgPath, data, 0o600); writeErr != nil {
		return fmt.Errorf("write config: %w", writeErr)
	}
	fmt.Println() //nolint:forbidigo // CLI separator
	output.PrintText("   %s", t("install.config.nsWritten", "path", nsCfgPath))

	// --- Write daemon.yml ---
	daemonCfg := config.DefaultDaemonConfig()
	daemonCfg.Locale = localeCode

	// Web UI is disabled in server mode for now.
	daemonCfg.Server.WebUI.Enabled = false

	if saveErr := config.SaveDaemonConfig(daemonCfg); saveErr != nil {
		return fmt.Errorf("save daemon config: %w", saveErr)
	}
	output.PrintText("   %s", t("install.config.daemonWritten", "path", config.DaemonConfigPath()))

	// --- Step 8: System service ---
	printDoneTitle(t("install.systemd.label"))
	installSystemdAndFirewall(port)

	// --- Step 9: Start ---
	startNow := promptConfirm(t("install.start.label"), true)
	if !startNow {
		printAccessInfo(hostname, port, nsCfg.Proxy.TLS.Enabled, nsCfg.Proxy.TLS.LetsEncrypt)
		output.PrintText("\n   %s", t("install.start.manual"))
		return nil
	}

	output.PrintText("   %s", t("install.start.starting"))

	// Prefer systemctl if the service was just installed — this ensures
	// the daemon is managed by systemd from the start (auto-restart on
	// crash, correct status in systemctl, clean process lifecycle).
	if startedViaSystemd := startViaSystemd(); !startedViaSystemd {
		password, pwdErr := resolvePassword(false)
		if pwdErr != nil {
			output.Errf("Could not resolve password: %v. Start manually: citeck start", pwdErr)
			return nil
		}
		if forkErr := forkDaemon(password, false, false, isOffline); forkErr != nil {
			output.Errf("Failed to start: %v. Start manually: citeck start", forkErr)
			return nil
		}
	}
	c, waitErr := waitForDaemon(30 * time.Second)
	if waitErr != nil {
		output.Errf("Daemon did not become ready: %v", waitErr)
		return nil
	}
	defer c.Close()
	streamErr := streamLiveStatus(c, liveStatusOpts{waitAll: true})
	if streamErr != nil {
		if errors.Is(streamErr, errInterrupted) {
			return nil // Ctrl+C during status streaming — daemon keeps running
		}
		return streamErr
	}
	printAccessInfo(hostname, port, nsCfg.Proxy.TLS.Enabled, nsCfg.Proxy.TLS.LetsEncrypt)
	return nil
}

// printAccessInfo shows the platform URL and login instructions after install.
// The admin password is looked up from the encrypted secret store so the
// user sees the same password the freshly generated realm.json references.
func printAccessInfo(hostname string, port int, tls, le bool) {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	addr := hostname
	if (tls && port != 443) || (!tls && port != 80) {
		addr = fmt.Sprintf("%s:%d", hostname, port)
	}
	url := fmt.Sprintf("%s://%s", scheme, addr)

	adminPassword := readAdminPasswordFromStore()
	if adminPassword == "" {
		// Fall back to the template default so the message is still
		// sensible if the daemon hasn't generated the secret yet.
		adminPassword = "admin"
	}

	fmt.Println() //nolint:forbidigo // CLI output
	title := t("install.ready.title")

	// Access info: pad the two labels to equal display width so the URL and
	// login values line up regardless of locale (CJK-aware).
	openLabel := t("install.ready.openBrowserLabel")
	loginLabel := t("install.ready.loginLabel")
	accessWidth := max(displayWidth(openLabel), displayWidth(loginLabel))
	urlLine := padRight(openLabel, accessWidth) + "  " + url
	loginLine := padRight(loginLabel, accessWidth) + "  " +
		t("install.ready.loginFormat", "user", "admin", "password", adminPassword)
	passwordNote := t("install.ready.passwordNote")

	// Useful commands: pad command names to the widest so the "— description"
	// markers line up across languages.
	cmdStatus := "citeck status -w"
	cmdSetup := "citeck setup"
	cmdWidth := max(displayWidth(cmdStatus), displayWidth(cmdSetup))
	cmdStatusLine := "  " + padRight(cmdStatus, cmdWidth) + "   — " + t("install.ready.commandStatusDesc")
	cmdSetupLine := "  " + padRight(cmdSetup, cmdWidth) + "   — " + t("install.ready.commandSetupDesc")

	// Collect lines for the box
	lines := []string{title, "", urlLine, loginLine, "", passwordNote}
	lines = append(lines, "", t("install.ready.commandsTitle"),
		cmdStatusLine, cmdSetupLine)
	if tls && !le {
		lines = append(lines, "")
		for line := range strings.SplitSeq(t("install.ready.certWarning"), "\n") {
			lines = append(lines, line)
		}
	}

	// Determine box width from longest line (terminal display width)
	maxLen := 0
	for _, l := range lines {
		if n := displayWidth(l); n > maxLen {
			maxLen = n
		}
	}
	separator := strings.Repeat("═", maxLen+4)

	output.PrintText("   %s", separator)
	for _, l := range lines {
		output.PrintText("   %s", l)
	}
	output.PrintText("   %s", separator)
}

type tlsResult int

const (
	tlsOK         tlsResult = iota // configured successfully
	tlsBack                        // go back to TLS menu
	tlsChangeHost                  // go back to hostname step
)

// configureTLS sets TLS config based on the user's choice.
func configureTLS(nsCfg *namespace.Config, choice string) tlsResult {
	nsCfg.Proxy.TLS = namespace.TlsConfig{} // reset stale state from previous choice
	switch choice {
	case t("install.tls.auto"):
		// "Auto" is a wizard-time decision only — we resolve it here into a
		// concrete choice (LE trusted OR self-signed) and store that. No
		// lingering "auto" marker in namespace.yml; `citeck setup` will
		// display whichever concrete mode actually applies.
		nsCfg.Proxy.TLS.Enabled = true
		output.PrintText("   %s", t("install.tls.leChecking", "host", nsCfg.Proxy.Host))
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		leErr := acme.TryStaging(ctx, nsCfg.Proxy.Host)
		cancel()
		leAvailable := leErr == nil
		rateLimited := false
		if leAvailable {
			// Staging passed, but production LE may still be rate-limited from a
			// previous run on this machine. Check the persisted marker — the
			// daemon would otherwise silently fall back to self-signed and the
			// user gets ERR_CERT_AUTHORITY_INVALID.
			if limited, retryAfter, err := acme.IsRateLimited(config.DataDir(), nsCfg.Proxy.Host); err == nil && limited {
				output.PrintText("   %s", t("install.tls.leRateLimited", "retryAfter", retryAfter.Format(time.RFC3339)))
				rateLimited = true
			}
		}
		if leAvailable && !rateLimited {
			nsCfg.Proxy.TLS.LetsEncrypt = true
			output.PrintText("   %s", t("install.tls.leStagingOK"))
		} else {
			// Fall through to self-signed. LetsEncrypt stays false (zero value)
			// so the stored config is unambiguously "self-signed". User can
			// re-run the wizard or `citeck setup tls` to switch to LE later.
			if leErr != nil {
				for line := range strings.SplitSeq(t("install.tls.leAutoFallback"), "\n") {
					output.PrintText("   %s", line)
				}
			}
			generateSelfSignedCert(nsCfg)
		}
	case t("install.tls.leTrusted"):
		nsCfg.Proxy.TLS.Enabled = true
		nsCfg.Proxy.TLS.LetsEncrypt = true
		return tryLEWithRecovery(nsCfg)
	case t("install.tls.httpsAutoGen"):
		nsCfg.Proxy.TLS.Enabled = true
		generateSelfSignedCert(nsCfg)
	case t("install.tls.custom"):
		if configureCustomCert(nsCfg) {
			return tlsBack
		}
	default:
		// HTTP only
	}
	return tlsOK
}

// tryLEWithRecovery validates LE, on failure offers retry / change host / back to TLS.
func tryLEWithRecovery(nsCfg *namespace.Config) tlsResult {
	for {
		output.PrintText("   %s", t("install.tls.leChecking", "host", nsCfg.Proxy.Host))
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		leErr := acme.TryStaging(ctx, nsCfg.Proxy.Host)
		cancel()
		if leErr == nil {
			output.PrintText("   %s", t("install.tls.leAvailable"))
			return tlsOK
		}
		output.PrintText("   %s", t("install.tls.leNotAvailable", "error", leErr.Error()))
		fmt.Println() //nolint:forbidigo // CLI separator
		options := []string{
			t("install.tls.leRetry"),
			t("install.tls.leChangeHost"),
			t("install.tls.leBackToTLS"),
		}
		recovery := promptSelect(t("install.tls.leRecovery"), options)
		switch recovery {
		case t("install.tls.leRetry"):
			continue
		case t("install.tls.leChangeHost"):
			return tlsChangeHost
		default:
			return tlsBack
		}
	}
}

// configureCustomCert asks for a directory with cert+key files.
// Returns true if the user chose to go back to the TLS menu.
func configureCustomCert(nsCfg *namespace.Config) bool {
	for {
		dir := promptInput(t("install.tls.customDir"), "", "")
		if dir == "" {
			return true // back
		}
		info, err := os.Stat(dir) //nolint:gosec // G703: dir is from interactive user prompt, not HTTP input
		if err != nil || !info.IsDir() {
			output.Errf("   %s: %s", t("install.tls.customDirNotFound"), dir)
			continue
		}

		certPath, certBack := pickFileByExt(dir, []string{".crt", ".pem", ".cer"}, t("install.tls.customCert"), "")
		if certBack {
			continue // re-ask for directory
		}
		keyPath, keyBack := pickFileByExt(dir, []string{".key", ".pem"}, t("install.tls.customKey"), certPath)
		if keyBack {
			continue
		}

		nsCfg.Proxy.TLS.Enabled = true
		nsCfg.Proxy.TLS.CertPath = certPath
		nsCfg.Proxy.TLS.KeyPath = keyPath
		output.PrintText("   %s: %s", t("install.tls.customCert"), certPath)
		output.PrintText("   %s: %s", t("install.tls.customKey"), keyPath)
		return false
	}
}

// pickFileByExt finds files with given extensions in dir, excluding excludePath.
// 1 match → auto-select. 0 → error + return back. 2+ → prompt user to pick.
// Returns (path, back). back=true means go back.
func pickFileByExt(dir string, exts []string, label, excludePath string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		output.Errf("   %s: %v", t("install.tls.customDirNotFound"), err)
		return "", true
	}
	extSet := make(map[string]bool, len(exts))
	for _, e := range exts {
		extSet[e] = true
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		if fullPath == excludePath {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if extSet[ext] {
			matches = append(matches, fullPath)
		}
	}

	switch len(matches) {
	case 0:
		output.Errf("   %s (%s)", t("install.tls.customNoFiles"), strings.Join(exts, ", "))
		return "", true
	case 1:
		return matches[0], false
	default:
		// Multiple matches — let user pick
		options := make([]string, 0, len(matches)+1)
		for _, m := range matches {
			options = append(options, filepath.Base(m))
		}
		options = append(options, t("install.release.back"))
		choice := promptSelect(label, options)
		if choice == t("install.release.back") {
			return "", true
		}
		for _, m := range matches {
			if filepath.Base(m) == choice {
				return m, false
			}
		}
		return matches[0], false // fallback
	}
}

// generateSelfSignedCert creates a self-signed TLS certificate and updates the config.
func generateSelfSignedCert(nsCfg *namespace.Config) {
	host := nsCfg.Proxy.Host
	if host == "" {
		host = "localhost"
	}
	tlsDir := filepath.Join(config.ConfDir(), "tls")
	_ = os.MkdirAll(tlsDir, 0o755) //nolint:gosec // G301: TLS dir needs 0o755
	certPath := filepath.Join(tlsDir, "server.crt")
	keyPath := filepath.Join(tlsDir, "server.key")
	if certErr := tlsutil.GenerateSelfSignedCert(certPath, keyPath, []string{host}, 365); certErr != nil {
		output.Errf("Warning: failed to generate self-signed cert: %v", certErr)
		return
	}
	nsCfg.Proxy.TLS.CertPath = certPath
	nsCfg.Proxy.TLS.KeyPath = keyPath
	output.PrintText("   %s", t("install.tls.selfSignedGenerated", "certPath", certPath))
	output.PrintText("   %s", t("install.tls.selfSignedWarning"))
}

// configureRegistryAuth checks if any imageRepo requires auth (authType: BASIC)
// and prompts for credentials, validates via Docker registry login, and saves to encrypted store.
// errBackToRelease signals that the user wants to go back to release selection.
var errBackToRelease = fmt.Errorf("back to release selection")

func configureRegistryAuth(wsCfg *bundle.WorkspaceConfig, usedPrefixes map[string]bool) error {
	authRepos := findAuthRepos(wsCfg, usedPrefixes)
	if len(authRepos) == 0 {
		return nil
	}

	svc, svcErr := openSecretService()
	if svcErr != nil {
		output.Errf("   %s: %v", t("install.registry.saveFailed"), svcErr)
		return nil // non-fatal — daemon will handle auth at runtime
	}

	// Sub-step of step 5 (Release) — shown only when the selected bundle uses
	// an auth-required registry. Rendered in the same ✓/○ style as other
	// wizard steps; the registry sub-flow is essentially a mini-form and
	// the printDoneTitle-style header keeps the visual rhythm.
	printDoneTitle(t("install.registry.label"))

	for _, repo := range authRepos {
		host := registryHost(repo.URL)
		output.PrintText("   %s: %s", t("install.registry.host"), host)
		for {
			username := promptInput(t("install.registry.username"), "", "")
			if username == "" {
				continue
			}
			password := promptPassword(t("install.registry.password"))
			if password == "" {
				continue
			}

			output.PrintText("   %s", t("install.registry.checking"))
			if err := dockerRegistryLogin(host, username, password); err != nil {
				output.Errf("   %s: %v", t("install.registry.failed"), err)
				options := []string{t("install.registry.retry"), t("install.registry.backToRelease")}
				choice := promptSelect(t("install.registry.recovery"), options)
				if choice == t("install.registry.backToRelease") {
					return errBackToRelease
				}
				continue
			}
			output.PrintText("   %s", t("install.registry.success"))

			if err := saveRegistrySecret(svc, repo, username, password); err != nil {
				output.Errf("   %s: %v", t("install.registry.saveFailed"), err)
			}
			break
		}
	}
	return nil
}

// dockerRegistryLogin validates credentials against a Docker registry.
func dockerRegistryLogin(registryURL, username, password string) error {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, loginErr := cli.RegistryLogin(ctx, registry.AuthConfig{
		Username:      username,
		Password:      password,
		ServerAddress: "https://" + registryURL,
	})
	if loginErr != nil {
		return fmt.Errorf("registry login %s: %w", registryURL, loginErr)
	}
	return nil
}

// readAdminPasswordFromStore looks up the generated ecos-app realm admin
// password from the encrypted secret store. Returns "" on any error so the
// install wizard can fall back to the hardcoded default. Best-effort —
// the wizard must not hard-fail if the daemon is still initializing the
// store on its first startup.
func readAdminPasswordFromStore() string {
	svc, err := openSecretService()
	if err != nil {
		return ""
	}
	if svc.IsLocked() {
		return ""
	}
	sec, err := svc.GetSecret("_admin_password")
	if err != nil || sec == nil {
		return ""
	}
	return sec.Value
}

// openSecretService creates a FileStore + SecretService and unlocks with default password.
// Encryption is set up by the daemon on first start — this function only unlocks.
// Returns (service, error). Caller should not use if error is non-nil.
func openSecretService() (*storage.SecretService, error) {
	store, err := storage.NewFileStore(config.ConfDir())
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	svc, err := storage.NewSecretService(store)
	if err != nil {
		return nil, fmt.Errorf("secret service: %w", err)
	}
	if svc.IsEncrypted() && svc.IsDefaultPassword() {
		if unlockErr := svc.Unlock(storage.DefaultMasterPassword); unlockErr != nil {
			return nil, fmt.Errorf("unlock: %w", unlockErr)
		}
	}
	// Not encrypted yet (daemon hasn't started) or custom password: svc stays locked
	return svc, nil
}

// registryHost extracts the host from a registry URL (strips path if present).
func registryHost(repoURL string) string {
	if idx := strings.Index(repoURL, "/"); idx > 0 {
		return repoURL[:idx]
	}
	return repoURL
}

// findAuthRepos returns image repos that require authentication and are used by the bundle.
// If usedPrefixes is nil, all auth repos are returned (used by start.go which checks all).
func findAuthRepos(wsCfg *bundle.WorkspaceConfig, usedPrefixes map[string]bool) []bundle.ImageRepo {
	if wsCfg == nil {
		return nil
	}
	repos := make([]bundle.ImageRepo, 0, len(wsCfg.ImageRepos))
	for _, repo := range wsCfg.ImageRepos {
		if repo.AuthType == "" {
			continue
		}
		if usedPrefixes != nil && !usedPrefixes[repo.ID] {
			continue // not used by selected bundle
		}
		repos = append(repos, repo)
	}
	return repos
}

// saveRegistrySecret saves registry credentials to the encrypted file store.
func saveRegistrySecret(svc *storage.SecretService, repo bundle.ImageRepo, username, password string) error {
	host := registryHost(repo.URL)
	if err := svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:    "registry-" + repo.ID,
			Name:  host + " credentials",
			Type:  storage.SecretRegistryAuth,
			Scope: host,
		},
		Value: username + ":" + password,
	}); err != nil {
		return fmt.Errorf("save secret: %w", err)
	}
	return nil
}

// bundleImageRepoIDs resolves the selected bundle and returns the set of imageRepo IDs it uses.
// Images are resolved to full URLs (e.g. "nexus.citeck.ru/ecos-model:1.0"), so we build a
// reverse map from registry host → repo ID using workspace config to map back.
func bundleImageRepoIDs(ref bundle.Ref, wsCfg *bundle.WorkspaceConfig) map[string]bool {
	if ref.IsEmpty() || wsCfg == nil {
		return nil
	}
	resolver := bundle.NewResolver(config.DataDir())
	resolver.SetOffline(true)
	result, err := resolver.Resolve(ref)
	if err != nil || result == nil || result.Bundle == nil {
		return nil // fallback: nil means "check all" in findAuthRepos
	}

	// Build reverse map: registry host → imageRepo ID.
	// Last-writer-wins if two repos share the same host (same pattern as ImageReposByHost).
	hostToID := make(map[string]string, len(wsCfg.ImageRepos))
	for _, repo := range wsCfg.ImageRepos {
		hostToID[registryHost(repo.URL)] = repo.ID
	}

	// Extract hosts from resolved images and map back to repo IDs
	ids := make(map[string]bool)
	addImage := func(image string) {
		host := registryHost(image)
		if id, ok := hostToID[host]; ok {
			ids[id] = true
		}
	}
	for _, app := range result.Bundle.Applications {
		addImage(app.Image)
	}
	for _, app := range result.Bundle.CiteckApps {
		addImage(app.Image)
	}
	return ids
}

// selectSnapshot shows an optional snapshot picker. Returns selected snapshot ID or "".
func selectSnapshot(snapshots []bundle.SnapshotDef) string {
	options := make([]string, 0, len(snapshots)+1)
	for _, snap := range snapshots {
		label := snap.Name
		if snap.Size != "" {
			label += " (" + snap.Size + ")"
		}
		options = append(options, label)
	}
	options = append(options, t("install.snapshot.skip"))

	selected := promptSelect(t("install.snapshot.prompt"), options)

	// Last option = skip.
	for i, snap := range snapshots {
		if selected == options[i] {
			fmt.Printf("   %s: %s\n", t("install.snapshot.selected"), snap.Name) //nolint:forbidigo // CLI output
			return snap.ID
		}
	}
	return ""
}

// repoVersions holds a bundle repo and its discovered versions.
type repoVersions struct {
	repo     bundle.BundlesRepo
	versions []string // sorted newest-first by ListBundleVersions
}

// displayName returns the human-readable name for this repo.
func (rv repoVersions) displayName() string {
	if rv.repo.Name != "" {
		return rv.repo.Name
	}
	return rv.repo.ID
}

// resolveRelease resolves available platform releases and lets the user pick one.
// Returns an error if no releases are available (offline without workspace data).
func resolveRelease(nsCfg *namespace.Config, offline bool) error {
	output.PrintText("   %s", t("install.release.fetching"))
	repos := discoverRepos(offline)

	// Filter to repos that have at least one version
	var withVersions []repoVersions
	for _, r := range repos {
		if len(r.versions) > 0 {
			withVersions = append(withVersions, r)
		}
	}

	if len(withVersions) == 0 {
		return fmt.Errorf("%s\n\n%s", t("install.release.notFound"), t("install.release.notFoundHelp"))
	}

	ref, err := pickRelease(withVersions)
	if err != nil {
		return err
	}
	nsCfg.BundleRef = ref
	return nil
}

// ErrInstallCancelled signals that the user aborted an install-time picker
// (Esc / Ctrl+C). Callers may want to treat this as a normal exit rather
// than an error.
var ErrInstallCancelled = errors.New("install canceled by user")

// pickRelease shows the tabbed bundle picker (one tab per repo with
// versions). Returns the selected ref or ErrInstallCancelled if the user
// cancels the picker.
func pickRelease(withVersions []repoVersions) (bundle.Ref, error) {
	tabs := buildInstallTabs(withVersions)
	title := t("install.release.label")
	refStr, ok, err := bundlepicker.Pick(title, tabs, pickerHints())
	if err != nil {
		return bundle.Ref{}, fmt.Errorf("show picker: %w", err)
	}
	if !ok {
		return bundle.Ref{}, ErrInstallCancelled
	}
	ref, parseErr := bundle.ParseRef(refStr)
	if parseErr != nil {
		return bundle.Ref{}, fmt.Errorf("parse bundle ref %q: %w", refStr, parseErr)
	}
	return ref, nil
}

// buildInstallTabs converts discovered repoVersions into picker tabs,
// preserving the order returned by discoverRepos. Repos without versions
// are skipped (the picker filters them anyway).
func buildInstallTabs(repos []repoVersions) []bundlepicker.Tab {
	tabs := make([]bundlepicker.Tab, 0, len(repos))
	for _, rv := range repos {
		if len(rv.versions) == 0 {
			continue
		}
		vs := make([]bundlepicker.Version, 0, len(rv.versions))
		for i, v := range rv.versions {
			vs = append(vs, bundlepicker.Version{
				Ref:    rv.repo.ID + ":" + v,
				Label:  v,
				Latest: i == 0,
			})
		}
		tabs = append(tabs, bundlepicker.Tab{
			ID:       rv.repo.ID,
			Name:     rv.displayName(),
			Versions: vs,
		})
	}
	return tabs
}

// discoverRepos loads workspace config and scans for available bundle versions per repo.
// When online, fetches the workspace repo from GitHub to discover releases.
// Uses a discard logger so resolver bookkeeping does not break wizard output.
func discoverRepos(offline bool) []repoVersions {
	silent := slog.New(slog.DiscardHandler)
	resolver := bundle.NewResolver(config.DataDir()).WithLogger(silent)
	resolver.SetOffline(offline)
	wsCfg := resolver.ResolveWorkspaceOnly()
	if wsCfg == nil || len(wsCfg.BundleRepos) == 0 {
		return nil
	}
	repos := make([]repoVersions, 0, len(wsCfg.BundleRepos))
	for _, repo := range wsCfg.BundleRepos {
		dir := resolveBundleDir(repo)
		repos = append(repos, repoVersions{
			repo:     repo,
			versions: bundle.ListBundleVersions(dir),
		})
	}
	return repos
}

// resolveBundleDir finds the bundle directory for a repo, checking local workspace first.
func resolveBundleDir(repo bundle.BundlesRepo) string {
	// 1. Local workspace import (--workspace zip)
	dir := filepath.Join(config.DataDir(), "repo")
	if repo.Path != "" {
		dir = filepath.Join(dir, repo.Path)
	}
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	// 2. Workspace repo clone (data/bundles/workspace)
	dir = filepath.Join(config.DataDir(), "bundles", "workspace")
	if repo.Path != "" {
		dir = filepath.Join(dir, repo.Path)
	}
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	// 3. Separate repo clone (data/bundles/{repoID})
	dir = filepath.Join(config.DataDir(), "bundles", repo.ID)
	if repo.Path != "" {
		dir = filepath.Join(dir, repo.Path)
	}
	return dir
}

// installSystemdAndFirewall handles the combined systemd + firewall step.
func installSystemdAndFirewall(platformPort int) {
	fmt.Println() //nolint:forbidigo // CLI output
	if _, lookErr := exec.LookPath("systemctl"); lookErr != nil {
		output.PrintText("   %s", t("install.systemd.notAvailable"))
		return
	}
	execPath, err := os.Executable()
	if err != nil {
		output.PrintText("   Could not determine executable path: %v", err)
		return
	}

	unit := fmt.Sprintf(`[Unit]
Description=Citeck Launcher
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=%s start --foreground
Restart=on-failure
RestartSec=10
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, execPath)

	servicePath := "/etc/systemd/system/citeck.service"
	if os.Getuid() != 0 {
		output.PrintText("   %s", t("install.systemd.notRoot"))
		output.PrintText("      sudo tee %s << 'EOF'\n%sEOF", servicePath, unit)
		output.PrintText("      sudo systemctl daemon-reload")
		output.PrintText("      sudo systemctl enable --now citeck")
	} else {
		if writeErr := os.WriteFile(servicePath, []byte(unit), 0o644); writeErr != nil { //nolint:gosec // G306: systemd unit files require 0o644
			output.PrintText("   Failed to write service file: %v", writeErr)
			return
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", "enable", "citeck").Run()
		output.PrintText("   %s", t("install.systemd.installed", "path", servicePath))
	}

	// Firewall sub-prompt: only if non-standard port and platform port is set
	if platformPort > 0 && platformPort != 80 && platformPort != 443 {
		if promptConfirm(t("install.firewall.open", "port", strconv.Itoa(platformPort)), true) {
			openFirewallPort(platformPort)
		}
	}
}

// startViaSystemd attempts to start the daemon via systemctl.
// Returns true if successful, false if systemd is not available or the service isn't installed.
func startViaSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	if _, err := os.Stat("/etc/systemd/system/citeck.service"); err != nil {
		return false
	}
	return exec.Command("systemctl", "start", "citeck").Run() == nil
}

// openFirewallPort opens a TCP port in the system firewall.
func openFirewallPort(port int) {
	portStr := strconv.Itoa(port)

	// Try ufw
	if path, err := exec.LookPath("ufw"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("   Not running as root. Run: sudo ufw allow %s/tcp", portStr)
			return
		}
		_ = exec.Command("ufw", "allow", portStr+"/tcp").Run() //nolint:gosec // G204: portStr is validated numeric input
		output.PrintText("   ufw: opened port %s/tcp", portStr)
		return
	}

	// Try firewall-cmd
	if path, err := exec.LookPath("firewall-cmd"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("   Not running as root. Run: sudo firewall-cmd --permanent --add-port=%s/tcp && sudo firewall-cmd --reload", portStr)
			return
		}
		_ = exec.Command("firewall-cmd", "--permanent", "--add-port="+portStr+"/tcp").Run() //nolint:gosec // G204: portStr is validated numeric input
		_ = exec.Command("firewall-cmd", "--reload").Run()
		output.PrintText("   firewall-cmd: opened port %s/tcp", portStr)
		return
	}

	output.PrintText("   No supported firewall detected (ufw/firewalld). Please open port %s manually.", portStr)
}
