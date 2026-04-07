package cli

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Interactive server installer",
		Long: `Set up a Citeck platform deployment: namespace config, TLS, systemd service, firewall.

Use --workspace to import a workspace zip archive (e.g. downloaded from GitHub/GitLab).
This extracts workspace config and bundle definitions for offline operation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args, workspaceZip)
		},
	}

	cmd.Flags().StringVar(&workspaceZip, "workspace", "", "Path to workspace zip archive (offline bundle import)")

	return cmd
}

func runInstall(_ *cobra.Command, _ []string, workspaceZip string) error { //nolint:gocyclo // interactive wizard with sequential steps
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
		setupSystemd(scanner)
		setupFirewall(scanner, 0)
		return nil
	}

	// 0. Language selection
	output.PrintText("Select language / Выберите язык:")
	locale := promptChoice(scanner, "Language", []string{
		"en (English)",
		"ru (Русский)",
		"zh (简体中文)",
		"es (Español)",
		"de (Deutsch)",
		"fr (Français)",
		"pt (Português)",
		"ja (日本語)",
	}, "en (English)")
	// Extract locale code from selection (e.g., "en (English)" -> "en")
	localeCode := strings.SplitN(locale, " ", 2)[0]

	nsCfg := namespace.DefaultNamespaceConfig()

	// 1. Display name
	nsCfg.Name = prompt(scanner, "Display name", "Citeck")

	// 2. Auth type
	authType := promptChoice(scanner, "Authentication type", []string{"BASIC", "KEYCLOAK"}, "BASIC")
	nsCfg.Authentication.Type = namespace.AuthenticationType(authType)

	// 3. Users (if BASIC)
	if nsCfg.Authentication.Type == namespace.AuthBasic {
		usersStr := prompt(scanner, "Users (comma-separated user:password)", "admin:admin")
		var users []string
		for u := range strings.SplitSeq(usersStr, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				// If no colon, use user:user format
				if !strings.Contains(u, ":") {
					u = u + ":" + u
				}
				users = append(users, u) // store full user:password
			}
		}
		if len(users) > 0 {
			nsCfg.Authentication.Users = users
		}
	}

	// 4. Hostname
	hostnameChoice := promptChoice(scanner, "Hostname", []string{"localhost", "auto-detect", "manual"}, "localhost")
	switch hostnameChoice {
	case "auto-detect":
		ip := detectPublicIP()
		if ip != "" {
			output.PrintText("Detected IP: %s", ip)
			nsCfg.Proxy.Host = ip
		} else {
			output.PrintText("Could not detect public IP, using localhost")
		}
	case "manual":
		nsCfg.Proxy.Host = prompt(scanner, "Enter hostname or IP", "localhost")
	}

	// 5. TLS
	tlsChoice := promptChoice(scanner, "TLS", []string{"none", "self-signed", "letsencrypt", "custom"}, "none")
	switch tlsChoice {
	case "self-signed":
		nsCfg.Proxy.TLS.Enabled = true
		host := nsCfg.Proxy.Host
		if host == "" {
			host = "localhost"
		}
		tlsDir := filepath.Join(config.ConfDir(), "tls")
		os.MkdirAll(tlsDir, 0o755) //nolint:gosec // G301: TLS dir needs 0o755
		certPath := filepath.Join(tlsDir, "server.crt")
		keyPath := filepath.Join(tlsDir, "server.key")
		if certErr := tlsutil.GenerateSelfSignedCert(certPath, keyPath, []string{host}, 365); certErr != nil {
			return fmt.Errorf("generate self-signed cert: %w", certErr)
		}
		nsCfg.Proxy.TLS.CertPath = certPath
		nsCfg.Proxy.TLS.KeyPath = keyPath
		output.PrintText("Self-signed certificate generated")
	case "letsencrypt":
		nsCfg.Proxy.TLS.Enabled = true
		nsCfg.Proxy.TLS.LetsEncrypt = true
		output.PrintText("Let's Encrypt will be configured after install")
	case "custom":
		nsCfg.Proxy.TLS.Enabled = true
		nsCfg.Proxy.TLS.CertPath = prompt(scanner, "Certificate file path", "")
		nsCfg.Proxy.TLS.KeyPath = prompt(scanner, "Private key file path", "")
		if _, certStatErr := os.Stat(nsCfg.Proxy.TLS.CertPath); certStatErr != nil {
			return fmt.Errorf("certificate file not found: %s", nsCfg.Proxy.TLS.CertPath)
		}
		if _, keyStatErr := os.Stat(nsCfg.Proxy.TLS.KeyPath); keyStatErr != nil {
			return fmt.Errorf("key file not found: %s", nsCfg.Proxy.TLS.KeyPath)
		}
	}

	// 6. Port
	defaultPort := 80
	if nsCfg.Proxy.TLS.Enabled {
		defaultPort = 443
	}
	portStr := prompt(scanner, "Port", strconv.Itoa(defaultPort))
	port, portErr := strconv.Atoi(portStr)
	if portErr != nil || port < 1 || port > 65535 {
		port = defaultPort
	}
	// Check port availability
	if ln, listenErr := net.Listen("tcp", fmt.Sprintf(":%d", port)); listenErr != nil {
		output.PrintText("Warning: port %d is already in use", port)
	} else {
		ln.Close()
	}
	nsCfg.Proxy.Port = port

	// 7. PgAdmin
	pgAdmin := promptYesNo(scanner, "Enable PgAdmin?", true)
	nsCfg.PgAdmin.Enabled = pgAdmin

	// 8. Bundle
	resolver := bundle.NewResolver(config.DataDir())
	wsCfg := resolver.ResolveWorkspaceOnly()
	var bundleVersions []string
	if wsCfg != nil {
		for _, repo := range wsCfg.BundleRepos {
			// Check local workspace repo first, then cloned bundles dir
			bundlesDir := filepath.Join(config.DataDir(), "repo")
			if repo.Path != "" {
				bundlesDir = filepath.Join(bundlesDir, repo.Path)
			}
			if _, statErr := os.Stat(bundlesDir); statErr != nil {
				bundlesDir = filepath.Join(config.DataDir(), "bundles", repo.ID)
				if repo.Path != "" {
					bundlesDir = filepath.Join(bundlesDir, repo.Path)
				}
			}
			versions := bundle.ListBundleVersions(bundlesDir)
			for _, v := range versions {
				bundleVersions = append(bundleVersions, repo.ID+":"+v)
			}
		}
	}

	if len(bundleVersions) > 0 {
		output.PrintText("\nAvailable bundles:")
		for i, v := range bundleVersions {
			output.PrintText("  %d) %s", i+1, v)
		}
		bundleIdx := prompt(scanner, "Select bundle (number or repo:version)", "1")
		idx, atoiErr := strconv.Atoi(bundleIdx)
		if atoiErr == nil && idx >= 1 && idx <= len(bundleVersions) {
			ref, _ := bundle.ParseRef(bundleVersions[idx-1])
			nsCfg.BundleRef = ref
		} else if ref, parseErr := bundle.ParseRef(bundleIdx); parseErr == nil {
			nsCfg.BundleRef = ref
		}
	} else {
		bundleStr := prompt(scanner, "Bundle ref (repo:version)", "community:LATEST")
		if ref, parseErr := bundle.ParseRef(bundleStr); parseErr == nil {
			nsCfg.BundleRef = ref
		}
	}

	// 9. Snapshot
	output.PrintText("\nSnapshot: enter snapshot ID for initial data, or press Enter for clean install")
	snapshotID := prompt(scanner, "Snapshot ID", "")
	if snapshotID != "" {
		nsCfg.Snapshot = snapshotID
	}

	// Write namespace.yml
	os.MkdirAll(filepath.Dir(nsCfgPath), 0o755) //nolint:gosec // G301: namespace config dir needs 0o755
	data, err := namespace.MarshalNamespaceConfig(&nsCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := fsutil.AtomicWriteFile(nsCfgPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	output.PrintText("\nNamespace config written to: %s", nsCfgPath)

	// 10. Remote Web UI access + daemon config
	daemonCfg := config.DefaultDaemonConfig()
	daemonCfg.Locale = localeCode
	remoteUI := promptYesNo(scanner, "\nEnable remote Web UI access (listen on 0.0.0.0)?", false)

	// Check if default Web UI port is available, offer to change if busy
	webuiHost := "127.0.0.1"
	if remoteUI {
		webuiHost = "0.0.0.0"
	}
	webuiPort := 7088
	for {
		addr := fmt.Sprintf("%s:%d", webuiHost, webuiPort)
		ln, listenErr := net.Listen("tcp", addr)
		if listenErr == nil {
			ln.Close()
			break
		}
		output.PrintText("Warning: port %d is already in use", webuiPort)
		portStr := prompt(scanner, "Web UI port", strconv.Itoa(webuiPort+1))
		p, err := strconv.Atoi(portStr)
		if err == nil && p > 0 && p <= 65535 {
			webuiPort = p
		}
	}
	daemonCfg.Server.WebUI.Listen = fmt.Sprintf("%s:%d", webuiHost, webuiPort)
	if err := config.SaveDaemonConfig(daemonCfg); err != nil {
		return fmt.Errorf("save daemon config: %w", err)
	}
	output.PrintText("daemon.yml written (locale: %s, listen: %s)", localeCode, daemonCfg.Server.WebUI.Listen)
	if remoteUI {
		generateInstallClientCert()
	}

	// Systemd + Firewall
	setupSystemd(scanner)
	setupFirewall(scanner, port)

	output.PrintText("\nInstallation complete. Start the daemon with: citeck start --foreground")
	return nil
}

func generateInstallClientCert() {
	output.PrintText("\nGenerating mTLS client certificate for remote access...")
	certPath := filepath.Join(config.WebUICADir(), "admin.crt")
	p12Path := absInWorkDir("citeck-webui-admin.p12")
	certPEM, keyPEM, err := tlsutil.GenerateClientCert(certPath, "admin", 365)
	if err != nil {
		output.PrintText("Warning: failed to generate client cert: %v", err)
		return
	}

	// Generate .p12 for browser import
	p12OK := false
	if p12Data, p12Err := tlsutil.EncodePKCS12(certPEM, keyPEM, ""); p12Err == nil {
		if writeErr := fsutil.AtomicWriteFile(p12Path, p12Data, 0o600); writeErr == nil {
			p12OK = true
		} else {
			output.Errf("Warning: could not write .p12 file: %v", writeErr)
		}
	}

	output.PrintText("  Certificate: %s", certPath)
	if p12OK {
		output.PrintText("  Browser P12: %s", p12Path)
		output.PrintText("")
		output.PrintText("Import %s into your browser to access the Web UI remotely.", filepath.Base(p12Path))
		output.PrintText("Delete it from the server after copying.")
	}
	output.PrintText("")
	output.PrintText("For CLI access from a remote machine, copy the server cert after first start:")
	output.PrintText("  scp server:%s ./server.crt", filepath.Join(config.WebUITLSDir(), "server.crt"))
	output.PrintText("  citeck --host <server>:7088 --tls-cert admin.crt --tls-key admin.key --server-cert server.crt status")
}

func prompt(scanner *bufio.Scanner, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultVal
	}
	return val
}

func promptChoice(scanner *bufio.Scanner, label string, choices []string, defaultVal string) string {
	for {
		fmt.Printf("%s (%s) [%s]: ", label, strings.Join(choices, "/"), defaultVal)
		scanner.Scan()
		val := strings.TrimSpace(scanner.Text())
		if val == "" {
			return defaultVal
		}
		for _, c := range choices {
			if strings.EqualFold(val, c) {
				return c
			}
		}
		fmt.Printf("Invalid choice %q. Please enter one of: %s\n", val, strings.Join(choices, ", "))
	}
}

func promptYesNo(scanner *bufio.Scanner, label string, defaultYes bool) bool {
	def := "Y/n"
	if !defaultYes {
		def = "y/N"
	}
	fmt.Printf("%s [%s]: ", label, def)
	scanner.Scan()
	val := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if val == "" {
		return defaultYes
	}
	return val == "y" || val == "yes"
}

func detectPublicIP() string {
	services := []string{
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
		"https://checkip.amazonaws.com",
	}
	httpClient := &http.Client{Timeout: 5 * time.Second}
	for _, svc := range services {
		resp, err := httpClient.Get(svc)
		if err != nil {
			continue
		}
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		ip := strings.TrimSpace(string(buf[:n]))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}

func setupSystemd(scanner *bufio.Scanner) {
	if !promptYesNo(scanner, "\nSet up systemd service?", true) {
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		output.PrintText("Could not determine executable path: %v", err)
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
		output.PrintText("Not running as root. To install the systemd service, run:")
		output.PrintText("  sudo tee %s << 'EOF'\n%sEOF", servicePath, unit)
		output.PrintText("  sudo systemctl daemon-reload")
		output.PrintText("  sudo systemctl enable --now citeck")
		return
	}

	if err := os.WriteFile(servicePath, []byte(unit), 0o644); err != nil { //nolint:gosec // G306: systemd unit files require 0o644
		output.PrintText("Failed to write service file: %v", err)
		return
	}

	exec.Command("systemctl", "daemon-reload").Run()
	exec.Command("systemctl", "enable", "citeck").Run()
	output.PrintText("Systemd service installed and enabled: %s", servicePath)
}

func setupFirewall(scanner *bufio.Scanner, port int) {
	if port <= 0 {
		return
	}
	if !promptYesNo(scanner, "Configure firewall?", false) {
		return
	}

	portStr := strconv.Itoa(port)

	// Try ufw
	if path, err := exec.LookPath("ufw"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("Not running as root. Run: sudo ufw allow %s/tcp", portStr)
			return
		}
		_ = exec.Command("ufw", "allow", portStr+"/tcp").Run() //nolint:gosec // G204: portStr is validated numeric input
		output.PrintText("ufw: opened port %s/tcp", portStr)
		return
	}

	// Try firewall-cmd
	if path, err := exec.LookPath("firewall-cmd"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("Not running as root. Run: sudo firewall-cmd --permanent --add-port=%s/tcp && sudo firewall-cmd --reload", portStr)
			return
		}
		_ = exec.Command("firewall-cmd", "--permanent", "--add-port="+portStr+"/tcp").Run() //nolint:gosec // G204: portStr is validated numeric input
		exec.Command("firewall-cmd", "--reload").Run()
		output.PrintText("firewall-cmd: opened port %s/tcp", portStr)
		return
	}

	output.PrintText("No supported firewall detected (ufw/firewalld). Please open port %s manually.", portStr)
}
