package cli

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	var workspaceZip string
	var offline bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Interactive server installer",
		Long: `Set up a Citeck platform deployment: namespace config, TLS, systemd service, firewall.

Use --workspace to import a workspace zip archive (e.g. downloaded from GitHub/GitLab).
This extracts workspace config and bundle definitions for offline operation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(workspaceZip, offline)
		},
	}

	cmd.Flags().StringVar(&workspaceZip, "workspace", "", "Path to workspace zip archive (offline bundle import)")
	cmd.Flags().BoolVar(&offline, "offline", false, "Offline mode: skip network checks (Let's Encrypt), use only local data")

	return cmd
}

func runInstall(workspaceZip string, offline bool) error { //nolint:gocyclo // interactive wizard with sequential steps
	scanner := bufio.NewScanner(os.Stdin)

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

	// Check if namespace.yml already exists — skip config prompts
	nsCfgPath := config.NamespaceConfigPath()
	if _, statErr := os.Stat(nsCfgPath); statErr == nil {
		output.PrintText("Namespace config already exists: %s", nsCfgPath)
		output.PrintText("Skipping config prompts. Use 'citeck config edit' to modify.")
		installSystemdAndFirewall(scanner, 0)
		return nil
	}

	// --- Step 1: Language (first, so welcome is localized) ---
	langOptions := make([]string, len(SupportedLocales))
	for i, loc := range SupportedLocales {
		langOptions[i] = loc.Code + " (" + loc.Name + ")"
	}
	fmt.Println() //nolint:forbidigo // CLI output
	locale := promptNumber(scanner, "Language / Язык / 语言", langOptions, 0)
	localeCode := strings.SplitN(locale, " ", 2)[0]
	initI18n(localeCode)

	// --- Step 0: Welcome (in selected language) ---
	fmt.Println()                                                                     //nolint:forbidigo // CLI output
	fmt.Printf("  %s\n", t("install.welcome.title"))                                  //nolint:forbidigo // CLI output
	fmt.Println()                                                                     //nolint:forbidigo // CLI output
	fmt.Printf("  %s\n", t("install.welcome.subtitle"))                               //nolint:forbidigo // CLI output
	fmt.Println()                                                                     //nolint:forbidigo // CLI output
	fmt.Printf("  %s\n", t("install.welcome.whatWillHappen"))                          //nolint:forbidigo // CLI output
	fmt.Printf("    1. %s  -> %s\n", t("install.welcome.stepConfig"), nsCfgPath)       //nolint:forbidigo // CLI output
	fmt.Printf("    2. %s  -> %s\n", t("install.welcome.stepDaemon"), config.DaemonConfigPath()) //nolint:forbidigo // CLI output
	fmt.Printf("    3. %s\n", t("install.welcome.stepService"))                        //nolint:forbidigo // CLI output
	fmt.Printf("    4. %s\n", t("install.welcome.stepStart"))                          //nolint:forbidigo // CLI output
	fmt.Println()                                                                     //nolint:forbidigo // CLI output
	fmt.Printf("  %s\n", t("install.welcome.canChange"))                               //nolint:forbidigo // CLI output
	fmt.Println()                                                                     //nolint:forbidigo // CLI output
	fmt.Printf("  %s", t("install.welcome.pressEnter"))                                //nolint:forbidigo // CLI prompt
	scanner.Scan()
	fmt.Println() //nolint:forbidigo // CLI output

	nsCfg := namespace.DefaultNamespaceConfig()
	nsCfg.PgAdmin.Enabled = false // default off (use pgAdmin separately if needed)

	// --- Step 2: Hostname ---
	defaultHost := config.DetectOutboundIP()
	hostname := promptText(scanner, t("install.hostname.label"), t("install.hostname.hint"), defaultHost)
	if hostname == "" {
		hostname = defaultHost
	}
	if hostname == "localhost" || hostname == "127.0.0.1" {
		output.PrintText("  %s", t("install.hostname.localOnly"))
	}
	nsCfg.Proxy.Host = hostname
	if hostname == "localhost" || hostname == "127.0.0.1" {
		nsCfg.Name = "Citeck"
	} else {
		nsCfg.Name = hostname
	}

	// --- Step 3: TLS ---
	isOffline := offline || workspaceZip != ""
	isLocalhost := hostname == "localhost" || hostname == "127.0.0.1"
	if !isLocalhost && !isOffline {
		configureTLSAuto(&nsCfg, hostname)
	} else {
		tlsOptions := []string{t("install.tls.httpsAutoGen"), t("install.tls.httpOnly")}
		tlsChoice := promptNumber(scanner, t("install.tls.label"), tlsOptions, 0)
		configureTLS(&nsCfg, tlsChoice)
	}

	// --- Step 4: Port ---
	defaultPort := 443
	portLabel := t("install.port.https")
	if !nsCfg.Proxy.TLS.Enabled {
		defaultPort = 80
		portLabel = t("install.port.http")
	}
	portStr := promptText(scanner, portLabel, t("install.port.hint"), strconv.Itoa(defaultPort))
	port, portErr := strconv.Atoi(portStr)
	if portErr != nil || port < 1 || port > 65535 {
		port = defaultPort
	}
	if ln, listenErr := net.Listen("tcp", fmt.Sprintf(":%d", port)); listenErr != nil {
		output.PrintText("  %s", t("install.port.inUse", "port", strconv.Itoa(port)))
	} else {
		ln.Close()
	}
	nsCfg.Proxy.Port = port

	// --- Step 5: Authentication ---
	keycloakLabel := t("install.auth.keycloak")
	authOptions := []string{keycloakLabel, t("install.auth.basic")}
	authChoice := promptNumber(scanner, t("install.auth.label"), authOptions, 0)
	if authChoice == keycloakLabel {
		nsCfg.Authentication.Type = namespace.AuthKeycloak
	} else {
		nsCfg.Authentication.Type = namespace.AuthBasic
		usersStr := promptText(scanner, t("install.auth.users.label"), t("install.auth.users.hint"), "admin")
		nsCfg.Authentication.Users = parseUsers(usersStr)
	}

	// --- Step 6: Release ---
	if err := resolveRelease(scanner, &nsCfg, isOffline); err != nil {
		return err
	}

	// --- Write namespace.yml ---
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
	output.PrintText("\n  %s", t("install.config.nsWritten", "path", nsCfgPath))

	// --- Write daemon.yml ---
	daemonCfg := config.DefaultDaemonConfig()
	daemonCfg.Locale = localeCode

	// Step 7: Remote Web UI — automatic based on hostname
	webuiHost := "127.0.0.1"
	isRemote := hostname != "localhost" && hostname != "127.0.0.1"
	if isRemote {
		webuiHost = "0.0.0.0"
	}
	webuiPort := findAvailablePort(webuiHost, 7088)
	daemonCfg.Server.WebUI.Listen = fmt.Sprintf("%s:%d", webuiHost, webuiPort)

	if saveErr := config.SaveDaemonConfig(daemonCfg); saveErr != nil {
		return fmt.Errorf("save daemon config: %w", saveErr)
	}
	output.PrintText("  %s", t("install.config.daemonWritten", "path", config.DaemonConfigPath()))

	if isRemote {
		generateInstallClientCert()
	}

	// --- Step 8: System service ---
	installSystemdAndFirewall(scanner, port)

	// --- Step 9: Start ---
	fmt.Println() //nolint:forbidigo // CLI output
	startNow := flagYes || promptYesNo(scanner, t("install.start.label"), "", true)
	if !startNow {
		printAccessInfo(hostname, port, nsCfg.Proxy.TLS.Enabled)
		output.PrintText("\n  %s", t("install.start.manual"))
		return nil
	}

	output.PrintText("  %s", t("install.start.starting"))
	password, pwdErr := resolvePassword(false)
	if pwdErr != nil {
		output.Errf("Could not resolve password: %v. Start manually: citeck start", pwdErr)
		return nil
	}
	if forkErr := forkDaemon(password, false, false, isOffline); forkErr != nil {
		output.Errf("Failed to start: %v. Start manually: citeck start", forkErr)
		return nil
	}
	c, waitErr := waitForDaemon(30 * time.Second)
	if waitErr != nil {
		output.Errf("Daemon did not become ready: %v", waitErr)
		return nil
	}
	defer c.Close()
	if streamErr := streamLiveStatus(c, false); streamErr != nil {
		return streamErr
	}
	printAccessInfo(hostname, port, nsCfg.Proxy.TLS.Enabled)
	return nil
}

// printAccessInfo shows the platform URL and login instructions after install.
func printAccessInfo(hostname string, port int, tls bool) {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	addr := hostname
	if (tls && port != 443) || (!tls && port != 80) {
		addr = fmt.Sprintf("%s:%d", hostname, port)
	}
	url := fmt.Sprintf("%s://%s", scheme, addr)

	fmt.Println() //nolint:forbidigo // CLI output
	output.PrintText("  ========================================")
	output.PrintText("  %s", t("install.ready.title"))
	output.PrintText("")
	output.PrintText("  %s", t("install.ready.openBrowser", "url", url))
	output.PrintText("  %s", t("install.ready.login"))
	// Warn about browser certificate warning for self-signed certs
	if tls && (net.ParseIP(hostname) != nil || hostname == "localhost") {
		output.PrintText("")
		for line := range strings.SplitSeq(t("install.ready.certWarning"), "\n") {
			output.PrintText("  %s", line)
		}
	}
	output.PrintText("  ========================================")
}

// configureTLS sets TLS config based on the user's choice.
func configureTLS(nsCfg *namespace.Config, choice string) {
	switch {
	case choice == t("install.tls.leTrusted"):
		nsCfg.Proxy.TLS.Enabled = true
		nsCfg.Proxy.TLS.LetsEncrypt = true
	case choice == t("install.tls.httpsAutoGen"):
		nsCfg.Proxy.TLS.Enabled = true
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
		output.PrintText("  %s", t("install.tls.selfSignedGenerated"))
	default:
		// None — HTTP only
	}
}

// configureTLSAuto tries Let's Encrypt staging for domain hostnames.
// If staging succeeds → Let's Encrypt (production). If fails → self-signed fallback.
// Skips LE entirely if the server has no internet connectivity.
func configureTLSAuto(nsCfg *namespace.Config, hostname string) {
	// Quick connectivity check — if we can't reach LE at all, skip immediately.
	conn, dialErr := net.DialTimeout("tcp", "acme-staging-v02.api.letsencrypt.org:443", 5*time.Second)
	if dialErr != nil {
		output.PrintText("  %s", t("install.tls.noInternet"))
		configureTLS(nsCfg, t("install.tls.httpsAutoGen"))
		return
	}
	conn.Close()

	output.PrintText("  %s", t("install.tls.leChecking", "host", hostname))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := acme.TryStaging(ctx, hostname); err != nil {
		output.PrintText("  %s", t("install.tls.leNotAvailable", "error", err.Error()))
		output.PrintText("  %s", t("install.tls.leFallback"))
		configureTLS(nsCfg, t("install.tls.httpsAutoGen"))
		return
	}

	output.PrintText("  %s", t("install.tls.leAvailable"))
	nsCfg.Proxy.TLS.Enabled = true
	nsCfg.Proxy.TLS.LetsEncrypt = true
}

// parseUsers parses a comma-separated list of usernames.
// If a user:password pair is provided, only the username part is kept
// (the generator creates password = username pairs).
func parseUsers(usersStr string) []string {
	parts := strings.Split(usersStr, ",")
	users := make([]string, 0, len(parts))
	for _, u := range parts {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		// Strip password part if present — generator uses username as password
		if idx := strings.IndexByte(u, ':'); idx > 0 {
			u = u[:idx]
		}
		users = append(users, u)
	}
	if len(users) == 0 {
		return []string{"admin"}
	}
	return users
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
func resolveRelease(scanner *bufio.Scanner, nsCfg *namespace.Config, offline bool) error {
	output.PrintText("  %s", t("install.release.fetching"))
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

	ref := pickRelease(scanner, withVersions, repos)
	nsCfg.BundleRef = ref
	return nil
}

// pickRelease shows a top-level menu: latest from each repo + "other" for version browsing.
func pickRelease(scanner *bufio.Scanner, withVersions, allRepos []repoVersions) bundle.Ref {
	for {
		// Build top-level options: latest from each repo with versions + "other"
		options := make([]string, 0, len(withVersions)+1)
		for _, rv := range withVersions {
			options = append(options, fmt.Sprintf("%s — %s (%s)", rv.displayName(), rv.versions[0], t("install.release.latest")))
		}
		otherLabel := t("install.release.otherVersion")
		if len(allRepos) > 1 || len(withVersions[0].versions) > 1 {
			options = append(options, otherLabel)
		}

		selected := promptNumber(scanner, t("install.release.label"), options, 0)

		// "Other version..." selected → drill-down menu
		if selected == otherLabel {
			if ref, ok := pickOtherRelease(scanner, allRepos); ok {
				return ref
			}
			continue // back pressed → re-show top-level menu
		}

		// Parse "RepoName — version (latest)" → repo:version
		for _, rv := range withVersions {
			if strings.HasPrefix(selected, rv.displayName()+" — ") {
				ref, _ := bundle.ParseRef(rv.repo.ID + ":" + rv.versions[0])
				return ref
			}
		}

		// Fallback (shouldn't happen)
		ref, _ := bundle.ParseRef(withVersions[0].repo.ID + ":" + withVersions[0].versions[0])
		return ref
	}
}

// pickOtherRelease shows repo list → version list with back navigation.
// Returns (ref, true) on selection, (_, false) on back.
func pickOtherRelease(scanner *bufio.Scanner, repos []repoVersions) (bundle.Ref, bool) {
	for {
		// Step 1: pick repo
		repoOptions := make([]string, 0, len(repos)+1)
		for _, rv := range repos {
			repoOptions = append(repoOptions, rv.displayName())
		}
		backLabel := t("install.release.back")
		repoOptions = append(repoOptions, backLabel)

		repoChoice := promptNumber(scanner, t("install.release.source"), repoOptions, 0)
		if repoChoice == backLabel {
			return bundle.Ref{}, false
		}

		// Find selected repo
		var selected *repoVersions
		for i := range repos {
			if repoChoice == repos[i].displayName() {
				selected = &repos[i]
				break
			}
		}
		if selected == nil {
			continue
		}

		// Step 2: pick version from selected repo
		if len(selected.versions) == 0 {
			output.PrintText("  %s", t("install.release.noVersions"))
			continue // back to repo selection
		}

		latestLabel := t("install.release.latest")
		verBackLabel := t("install.release.back")
		verOptions := make([]string, 0, len(selected.versions)+1)
		for i, v := range selected.versions {
			if i == 0 {
				verOptions = append(verOptions, v+" ("+latestLabel+")")
			} else {
				verOptions = append(verOptions, v)
			}
		}
		verOptions = append(verOptions, verBackLabel)

		verChoice := promptNumber(scanner, t("install.release.version"), verOptions, 0)
		if verChoice == verBackLabel {
			continue // back to repo selection
		}

		verChoice = strings.TrimSuffix(verChoice, " ("+latestLabel+")")
		ref, _ := bundle.ParseRef(selected.repo.ID + ":" + verChoice)
		return ref, true
	}
}

// discoverRepos loads workspace config and scans for available bundle versions per repo.
// When online, fetches the workspace repo from GitHub to discover releases.
func discoverRepos(offline bool) []repoVersions {
	resolver := bundle.NewResolver(config.DataDir())
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
	dir := filepath.Join(config.DataDir(), "repo")
	if repo.Path != "" {
		dir = filepath.Join(dir, repo.Path)
	}
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	dir = filepath.Join(config.DataDir(), "bundles", repo.ID)
	if repo.Path != "" {
		dir = filepath.Join(dir, repo.Path)
	}
	return dir
}

// findAvailablePort finds an available port starting from startPort.
func findAvailablePort(host string, startPort int) int {
	port := startPort
	for range 10 { // try up to 10 ports
		addr := fmt.Sprintf("%s:%d", host, port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return port
		}
		port++
	}
	return startPort // fallback
}

func generateInstallClientCert() {
	certPath := filepath.Join(config.WebUICADir(), "admin.crt")
	p12Path := absInWorkDir("citeck-webui-admin.p12")
	certPEM, keyPEM, err := tlsutil.GenerateClientCert(certPath, "admin", 365)
	if err != nil {
		output.PrintText("  Warning: failed to generate management UI certificate: %v", err)
		return
	}

	// Generate .p12 for browser import
	if p12Data, p12Err := tlsutil.EncodePKCS12(certPEM, keyPEM, ""); p12Err == nil {
		if writeErr := fsutil.AtomicWriteFile(p12Path, p12Data, 0o600); writeErr == nil {
			output.PrintText("  %s", t("install.cert.mgmtUiCert", "path", p12Path))
			output.PrintText("  %s", t("install.cert.mgmtUiHint"))
		}
	}
}

// installSystemdAndFirewall handles the combined systemd + firewall step.
func installSystemdAndFirewall(scanner *bufio.Scanner, platformPort int) {
	fmt.Println() //nolint:forbidigo // CLI output
	if !promptYesNo(scanner, t("install.systemd.label"), t("install.systemd.hint"), true) {
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		output.PrintText("  Could not determine executable path: %v", err)
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
		output.PrintText("  %s", t("install.systemd.notRoot"))
		output.PrintText("    sudo tee %s << 'EOF'\n%sEOF", servicePath, unit)
		output.PrintText("    sudo systemctl daemon-reload")
		output.PrintText("    sudo systemctl enable --now citeck")
	} else {
		if writeErr := os.WriteFile(servicePath, []byte(unit), 0o644); writeErr != nil { //nolint:gosec // G306: systemd unit files require 0o644
			output.PrintText("  Failed to write service file: %v", writeErr)
			return
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", "enable", "citeck").Run()
		output.PrintText("  %s", t("install.systemd.installed", "path", servicePath))
	}

	// Firewall sub-prompt: only if non-standard port and platform port is set
	if platformPort > 0 && platformPort != 80 && platformPort != 443 {
		if promptYesNo(scanner, t("install.firewall.open", "port", strconv.Itoa(platformPort)), t("install.firewall.hint"), true) {
			openFirewallPort(platformPort)
		}
	}
}

// openFirewallPort opens a TCP port in the system firewall.
func openFirewallPort(port int) {
	portStr := strconv.Itoa(port)

	// Try ufw
	if path, err := exec.LookPath("ufw"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("  Not running as root. Run: sudo ufw allow %s/tcp", portStr)
			return
		}
		_ = exec.Command("ufw", "allow", portStr+"/tcp").Run() //nolint:gosec // G204: portStr is validated numeric input
		output.PrintText("  ufw: opened port %s/tcp", portStr)
		return
	}

	// Try firewall-cmd
	if path, err := exec.LookPath("firewall-cmd"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("  Not running as root. Run: sudo firewall-cmd --permanent --add-port=%s/tcp && sudo firewall-cmd --reload", portStr)
			return
		}
		_ = exec.Command("firewall-cmd", "--permanent", "--add-port="+portStr+"/tcp").Run() //nolint:gosec // G204: portStr is validated numeric input
		_ = exec.Command("firewall-cmd", "--reload").Run()
		output.PrintText("  firewall-cmd: opened port %s/tcp", portStr)
		return
	}

	output.PrintText("  No supported firewall detected (ufw/firewalld). Please open port %s manually.", portStr)
}

