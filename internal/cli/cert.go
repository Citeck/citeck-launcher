package cli

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	acmeLib "github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/config"
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
	cmd.AddCommand(newCertStatusCmd(), newCertGenerateCmd(), newCertLetsEncryptCmd())
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
			data, err := os.ReadFile(certPath)
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
				"subject":    cert.Subject.CommonName,
				"issuer":     cert.Issuer.CommonName,
				"notBefore":  cert.NotBefore.Format(time.RFC3339),
				"notAfter":   cert.NotAfter.Format(time.RFC3339),
				"daysLeft":   daysLeft,
				"dnsNames":   cert.DNSNames,
				"ipAddresses": cert.IPAddresses,
				"selfSigned": cert.Issuer.CommonName == cert.Subject.CommonName,
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
	var hosts []string
	var days int

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate self-signed TLS certificate",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(hosts) == 0 {
				hosts = []string{"localhost"}
			}

			tlsDir := filepath.Join(config.ConfDir(), "tls")
			if err := os.MkdirAll(tlsDir, 0o755); err != nil {
				return err
			}

			certPath := filepath.Join(tlsDir, "server.crt")
			keyPath := filepath.Join(tlsDir, "server.key")

			if err := tlsutil.GenerateSelfSignedCert(certPath, keyPath, hosts, days); err != nil {
				return err
			}

			output.PrintResult(map[string]string{"certPath": certPath, "keyPath": keyPath}, func() {
				output.PrintText("Certificate generated:")
				output.PrintText("  Cert: %s", certPath)
				output.PrintText("  Key:  %s", keyPath)
				output.PrintText("  Hosts: %v", hosts)
				output.PrintText("  Valid: %d days", days)
			})
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "host", nil, "Hostnames and IPs for the certificate")
	cmd.Flags().IntVar(&days, "days", 365, "Certificate validity in days")

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
					os.WriteFile(backupPath, data, 0o644)
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

