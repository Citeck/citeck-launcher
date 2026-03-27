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
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Interactive server installer",
		Long:  "Set up a Citeck ECOS deployment: namespace config, TLS, systemd service, firewall.",
		RunE:  runInstall,
	}
}

func runInstall(cmd *cobra.Command, args []string) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Check Docker is available
	dockerConn, err := net.DialTimeout("unix", "/var/run/docker.sock", 2*time.Second)
	if err != nil {
		return fmt.Errorf("Docker is not reachable at /var/run/docker.sock. Install Docker first: https://docs.docker.com/engine/install/")
	}
	dockerConn.Close()

	// Check if namespace.yml already exists — skip config prompts
	nsCfgPath := config.NamespaceConfigPath()
	if _, err := os.Stat(nsCfgPath); err == nil {
		output.PrintText("Namespace config already exists: %s", nsCfgPath)
		output.PrintText("Skipping config prompts. Use 'citeck config edit' to modify.")
		setupSystemd(scanner)
		setupFirewall(scanner, 0)
		return nil
	}

	nsCfg := namespace.DefaultNamespaceConfig()

	// 1. Display name
	nsCfg.Name = prompt(scanner, "Display name", "Citeck ECOS")

	// 2. Auth type
	authType := promptChoice(scanner, "Authentication type", []string{"BASIC", "KEYCLOAK"}, "BASIC")
	nsCfg.Authentication.Type = namespace.AuthenticationType(authType)

	// 3. Users (if BASIC)
	if nsCfg.Authentication.Type == namespace.AuthBasic {
		usersStr := prompt(scanner, "Users (comma-separated user:password)", "admin:admin")
		var users []string
		for _, u := range strings.Split(usersStr, ",") {
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
		os.MkdirAll(tlsDir, 0o755)
		certPath := filepath.Join(tlsDir, "server.crt")
		keyPath := filepath.Join(tlsDir, "server.key")
		if err := tlsutil.GenerateSelfSignedCert(certPath, keyPath, []string{host}, 365); err != nil {
			return fmt.Errorf("generate self-signed cert: %w", err)
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
		if _, err := os.Stat(nsCfg.Proxy.TLS.CertPath); err != nil {
			return fmt.Errorf("certificate file not found: %s", nsCfg.Proxy.TLS.CertPath)
		}
		if _, err := os.Stat(nsCfg.Proxy.TLS.KeyPath); err != nil {
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
	resolveResult, err := resolver.Resolve(bundle.BundleRef{})
	var bundleVersions []string
	if err == nil && resolveResult.Workspace != nil {
		for _, repo := range resolveResult.Workspace.BundleRepos {
			bundlesDir := filepath.Join(config.DataDir(), "bundles", repo.ID)
			if repo.Path != "" {
				bundlesDir = filepath.Join(bundlesDir, repo.Path)
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
		idx, err := strconv.Atoi(bundleIdx)
		if err == nil && idx >= 1 && idx <= len(bundleVersions) {
			ref, _ := bundle.ParseBundleRef(bundleVersions[idx-1])
			nsCfg.BundleRef = ref
		} else if ref, err := bundle.ParseBundleRef(bundleIdx); err == nil {
			nsCfg.BundleRef = ref
		}
	} else {
		bundleStr := prompt(scanner, "Bundle ref (repo:version)", "community:LATEST")
		if ref, err := bundle.ParseBundleRef(bundleStr); err == nil {
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
	os.MkdirAll(filepath.Dir(nsCfgPath), 0o755)
	data, err := namespace.MarshalNamespaceConfig(&nsCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(nsCfgPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	output.PrintText("\nNamespace config written to: %s", nsCfgPath)

	// 10. Remote Web UI access
	remoteUI := promptYesNo(scanner, "\nEnable remote Web UI access (listen on 0.0.0.0)?", false)
	if remoteUI {
		daemonCfg := config.DefaultDaemonConfig()
		daemonCfg.Server.WebUI.Listen = fmt.Sprintf("0.0.0.0:%d", 8088)
		if err := config.SaveDaemonConfig(daemonCfg); err != nil {
			return fmt.Errorf("save daemon config: %w", err)
		}
		output.PrintText("daemon.yml written with listen: %s", daemonCfg.Server.WebUI.Listen)

		// Auto-generate first client cert for mTLS
		output.PrintText("\nGenerating mTLS client certificate for remote access...")
		certPath := filepath.Join(config.WebUICADir(), "admin.crt")
		certPEM, keyPEM, err := tlsutil.GenerateClientCert(certPath, "admin", 365)
		if err != nil {
			output.PrintText("Warning: failed to generate client cert: %v", err)
		} else {
			output.PrintText("Certificate saved to: %s", certPath)
			output.PrintText("")
			output.PrintText("=== PRIVATE KEY (save this — it will NOT be shown again) ===")
			output.PrintText("%s", strings.TrimSpace(string(keyPEM)))
			output.PrintText("")
			output.PrintText("=== CERTIFICATE ===")
			output.PrintText("%s", strings.TrimSpace(string(certPEM)))
			output.PrintText("")
			output.PrintText("To create PKCS12 for browser import, save the key above to a file, then:")
			output.PrintText("  openssl pkcs12 -export -in %s -inkey <key-file> -out admin.p12", certPath)
			output.PrintText("")
			output.PrintText("For CLI access from a remote machine, copy the server cert after first start:")
			output.PrintText("  scp server:%s ./server.crt", filepath.Join(config.WebUITLSDir(), "server.crt"))
			output.PrintText("  citeck --host <server>:8088 --tls-cert admin.crt --tls-key admin.key --server-cert server.crt status")
		}
	}

	// Systemd + Firewall
	setupSystemd(scanner)
	setupFirewall(scanner, port)

	output.PrintText("\nInstallation complete. Start the daemon with: citeck start --foreground")
	return nil
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
Description=Citeck ECOS Launcher
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

	if err := os.WriteFile(servicePath, []byte(unit), 0o644); err != nil {
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
		exec.Command("ufw", "allow", portStr+"/tcp").Run()
		output.PrintText("ufw: opened port %s/tcp", portStr)
		return
	}

	// Try firewall-cmd
	if path, err := exec.LookPath("firewall-cmd"); err == nil && path != "" {
		if os.Getuid() != 0 {
			output.PrintText("Not running as root. Run: sudo firewall-cmd --permanent --add-port=%s/tcp && sudo firewall-cmd --reload", portStr)
			return
		}
		exec.Command("firewall-cmd", "--permanent", "--add-port="+portStr+"/tcp").Run()
		exec.Command("firewall-cmd", "--reload").Run()
		output.PrintText("firewall-cmd: opened port %s/tcp", portStr)
		return
	}

	output.PrintText("No supported firewall detected (ufw/firewalld). Please open port %s manually.", portStr)
}
