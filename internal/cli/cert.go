package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/niceteck/citeck-launcher/internal/config"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage TLS certificates",
	}
	cmd.AddCommand(newCertStatusCmd(), newCertGenerateCmd())
	return cmd
}

func newCertStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show certificate expiration and details",
		RunE: func(cmd *cobra.Command, args []string) error {
			certPath := filepath.Join(config.ConfDir(), "tls", "server.crt")
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

			if err := generateSelfSignedCert(certPath, keyPath, hosts, days); err != nil {
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

func generateSelfSignedCert(certPath, keyPath string, hosts []string, days int) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer keyFile.Close()
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return nil
}
