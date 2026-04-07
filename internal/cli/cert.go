package cli

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	acmeLib "github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
	"github.com/spf13/cobra"
)

func newCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage TLS certificates",
	}
	cmd.AddCommand(
		newCertStatusCmd(),
		newCertGenerateCmd(),
		newCertListCmd(),
		newCertRevokeCmd(),
		newCertLetsEncryptCmd(),
	)
	return cmd
}

func newCertStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show certificate expiration and details",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Try namespace config cert path first, then LE fullchain, then default
			certPath := ""
			if nsCfg, err := namespace.LoadNamespaceConfig(config.NamespaceConfigPath()); err == nil && nsCfg.Proxy.TLS.CertPath != "" {
				certPath = nsCfg.Proxy.TLS.CertPath
			}
			if certPath == "" {
				lePath := filepath.Join(config.ConfDir(), "tls", "fullchain.pem")
				if _, err := os.Stat(lePath); err == nil {
					certPath = lePath
				}
			}
			if certPath == "" {
				certPath = filepath.Join(config.ConfDir(), "tls", "server.crt")
			}
			data, err := os.ReadFile(certPath) //nolint:gosec // G304: certPath is from internal config
			if err != nil {
				return fmt.Errorf("read cert: %w", err)
			}

			block, _ := pem.Decode(data)
			if block == nil {
				return fmt.Errorf("invalid PEM data in %s", certPath)
			}

			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return fmt.Errorf("parse cert: %w", err)
			}

			daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)

			result := map[string]any{
				"subject":     cert.Subject.CommonName,
				"issuer":      cert.Issuer.CommonName,
				"notBefore":   cert.NotBefore.Format(time.RFC3339),
				"notAfter":    cert.NotAfter.Format(time.RFC3339),
				"daysLeft":    daysLeft,
				"dnsNames":    cert.DNSNames,
				"ipAddresses": cert.IPAddresses,
				"selfSigned":  cert.Issuer.CommonName == cert.Subject.CommonName,
			}

			output.PrintResult(result, func() {
				output.PrintText("Subject:     %s", cert.Subject.CommonName)
				output.PrintText("Issuer:      %s", cert.Issuer.CommonName)
				output.PrintText("Not Before:  %s", cert.NotBefore.Format("2006-01-02"))
				output.PrintText("Not After:   %s", cert.NotAfter.Format("2006-01-02"))
				if daysLeft > 30 {
					output.PrintText("Expires in:  %s", output.Colorize(output.Green, fmt.Sprintf("%d days", daysLeft)))
				} else if daysLeft > 0 {
					output.PrintText("Expires in:  %s", output.Colorize(output.Yellow, fmt.Sprintf("%d days", daysLeft)))
				} else {
					output.PrintText("Expires in:  %s", output.Colorize(output.Red, "EXPIRED"))
				}
				if len(cert.DNSNames) > 0 {
					output.PrintText("DNS Names:   %v", cert.DNSNames)
				}
			})
			return nil
		},
	}
}

func newCertGenerateCmd() *cobra.Command {
	var name string
	var hosts []string
	var days int

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate TLS certificate (--name for mTLS client cert, --host for server cert)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name != "" {
				return generateClientCert(name, days)
			}
			return generateServerCert(hosts, days)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Client cert name (generates mTLS client certificate)")
	cmd.Flags().StringSliceVar(&hosts, "host", nil, "Hostnames and IPs for server certificate")
	cmd.Flags().IntVar(&days, "days", 365, "Certificate validity in days")

	return cmd
}

// absInWorkDir returns an absolute path for a filename in the current working directory.
func absInWorkDir(name string) string {
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, name)
	}
	return name
}

func validateCertName(name string) error {
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.ContainsAny(name, `/\`) || name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("invalid certificate name %q: must not contain path separators or '..'", name)
	}
	return nil
}

func generateClientCert(name string, days int) error {
	if err := validateCertName(name); err != nil {
		return err
	}

	certPath := filepath.Join(config.WebUICADir(), name+".crt")
	p12Path := absInWorkDir("citeck-webui-" + name + ".p12")

	certPEM, keyPEM, err := tlsutil.GenerateClientCert(certPath, name, days)
	if err != nil {
		return fmt.Errorf("generate client cert: %w", err)
	}

	// Generate PKCS12 for browser import (empty password — browser will prompt on import)
	p12Data, p12Err := tlsutil.EncodePKCS12(certPEM, keyPEM, "")
	if p12Err != nil {
		output.Errf("Warning: could not generate .p12 file: %v", p12Err)
	} else if writeErr := fsutil.AtomicWriteFile(p12Path, p12Data, 0o600); writeErr != nil {
		output.Errf("Warning: could not write .p12 file: %v", writeErr)
	}

	output.PrintResult(map[string]any{
		"name":     name,
		"certPath": certPath,
		"p12Path":  p12Path,
	}, func() {
		output.PrintText("Client certificate generated for %q", name)
		output.PrintText("")
		output.PrintText("  Certificate: %s", certPath)
		if p12Err == nil {
			output.PrintText("  Browser P12: %s", p12Path)
			output.PrintText("")
			output.PrintText("Import %s into your browser to access the Web UI.", filepath.Base(p12Path))
			output.PrintText("Delete it from the server after copying.")
		}
	})
	return nil
}

func generateServerCert(hosts []string, days int) error {
	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}

	tlsDir := filepath.Join(config.ConfDir(), "tls")
	if err := os.MkdirAll(tlsDir, 0o755); err != nil { //nolint:gosec // G301: TLS dir needs 0o755 for server access
		return fmt.Errorf("create TLS dir: %w", err)
	}

	certPath := filepath.Join(tlsDir, "server.crt")
	keyPath := filepath.Join(tlsDir, "server.key")

	if err := tlsutil.GenerateSelfSignedCert(certPath, keyPath, hosts, days); err != nil {
		return fmt.Errorf("generate server cert: %w", err)
	}

	output.PrintResult(map[string]string{"certPath": certPath, "keyPath": keyPath}, func() {
		output.PrintText("Certificate generated:")
		output.PrintText("  Cert: %s", certPath)
		output.PrintText("  Key:  %s", keyPath)
		output.PrintText("  Hosts: %v", hosts)
		output.PrintText("  Valid: %d days", days)
	})
	return nil
}

func newCertListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List trusted client certificates (mTLS)",
		RunE: func(cmd *cobra.Command, args []string) error {
			caDir := config.WebUICADir()
			entries, err := os.ReadDir(caDir)
			if err != nil {
				if os.IsNotExist(err) {
					output.PrintText("No client certificates found in %s", caDir)
					return nil
				}
				return fmt.Errorf("read CA dir: %w", err)
			}

			type certInfo struct {
				Name    string `json:"name"`
				CN      string `json:"cn"`
				Expires string `json:"expires"`
				Days    int    `json:"daysLeft"`
			}
			var certs []certInfo

			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				ext := strings.ToLower(filepath.Ext(entry.Name()))
				if ext != ".crt" && ext != ".pem" {
					continue
				}

				data, err := os.ReadFile(filepath.Join(caDir, entry.Name())) //nolint:gosec // G304: caDir is from internal config
				if err != nil {
					continue
				}
				block, _ := pem.Decode(data)
				if block == nil {
					continue
				}
				cert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					continue
				}

				daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
				certs = append(certs, certInfo{
					Name:    strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
					CN:      cert.Subject.CommonName,
					Expires: cert.NotAfter.Format("2006-01-02"),
					Days:    daysLeft,
				})
			}

			if len(certs) == 0 {
				output.PrintText("No client certificates found in %s", caDir)
				return nil
			}

			output.PrintResult(certs, func() {
				output.PrintText("%-20s %-20s %-12s %s", "NAME", "CN", "EXPIRES", "DAYS LEFT")
				for _, c := range certs {
					daysStr := fmt.Sprintf("%d", c.Days)
					if c.Days <= 0 {
						daysStr = output.Colorize(output.Red, "EXPIRED")
					} else if c.Days <= 30 {
						daysStr = output.Colorize(output.Yellow, daysStr)
					}
					output.PrintText("%-20s %-20s %-12s %s", c.Name, c.CN, c.Expires, daysStr)
				}
			})
			return nil
		},
	}
}

func newCertRevokeCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke a client certificate (remove from trusted list)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateCertName(name); err != nil {
				return err
			}

			certPath := filepath.Join(config.WebUICADir(), name+".crt")
			if _, err := os.Stat(certPath); os.IsNotExist(err) {
				return fmt.Errorf("client cert %q not found at %s", name, certPath)
			}

			if err := os.Remove(certPath); err != nil {
				return fmt.Errorf("remove cert: %w", err)
			}

			output.PrintResult(map[string]string{"name": name, "status": "revoked"}, func() {
				output.PrintText("Client certificate %q revoked (removed from %s)", name, config.WebUICADir())
			})
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name of the client cert to revoke")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func newCertLetsEncryptCmd() *cobra.Command {
	var host string

	cmd := &cobra.Command{
		Use:   "letsencrypt",
		Short: "Obtain a Let's Encrypt certificate via ACME HTTP-01 challenge",
		Long:  "Obtains a free TLS certificate from Let's Encrypt. Requires port 80 to be available and the hostname to resolve to this server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if host == "" {
				// Try to read from namespace config
				nsCfgPath := config.NamespaceConfigPath()
				if nsCfg, err := namespace.LoadNamespaceConfig(nsCfgPath); err == nil && nsCfg.Proxy.Host != "" {
					host = nsCfg.Proxy.Host
				}
				if host == "" || host == "localhost" {
					return fmt.Errorf("--host is required (Let's Encrypt cannot issue certs for localhost or IPs)")
				}
			}

			if ip := net.ParseIP(host); ip != nil {
				output.PrintText("Note: IP certificates from Let's Encrypt are short-lived (~6 days)")
			}

			acmeClient := acmeLib.NewClient(config.DataDir(), config.ConfDir(), host)

			// Check existing cert — require --yes to overwrite
			if _, err := os.Stat(acmeClient.CertPath()); err == nil {
				if !flagYes {
					return fmt.Errorf("certificate already exists at %s (use --yes to overwrite)", acmeClient.CertPath())
				}
				// Backup existing cert
				backupPath := acmeClient.CertPath() + ".bak"
				if data, err := os.ReadFile(acmeClient.CertPath()); err == nil {
					_ = os.WriteFile(backupPath, data, 0o644) //nolint:gosec // G306: backup cert needs 0o644 for readability
					output.PrintText("Existing cert backed up to %s", backupPath)
				}
			}

			output.PrintText("Obtaining Let's Encrypt certificate for %s...", host)
			output.PrintText("Port 80 must be available for HTTP-01 challenge")

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			if err := acmeClient.ObtainCertificate(ctx); err != nil {
				return fmt.Errorf("certificate obtainment failed: %w", err)
			}

			output.PrintResult(map[string]string{
				"certPath": acmeClient.CertPath(),
				"keyPath":  acmeClient.KeyPath(),
			}, func() {
				output.PrintText("Certificate obtained:")
				output.PrintText("  Cert: %s", acmeClient.CertPath())
				output.PrintText("  Key:  %s", acmeClient.KeyPath())
				output.PrintText("\nTo use this certificate, update namespace.yml:")
				output.PrintText("  proxy:")
				output.PrintText("    tls:")
				output.PrintText("      enabled: true")
				output.PrintText("      letsEncrypt: true")
			})
			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "Hostname for the certificate")
	return cmd
}
