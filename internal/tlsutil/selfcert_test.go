package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := GenerateSelfSignedCert(certPath, keyPath, []string{"example.com"}, 30); err != nil {
		t.Fatal(err)
	}

	// Verify cert exists and is valid
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block in cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("example.com"); err != nil {
		t.Errorf("cert does not match hostname: %v", err)
	}
	if cert.NotAfter.Before(time.Now().Add(29 * 24 * time.Hour)) {
		t.Error("cert expires too soon")
	}

	// Verify key exists
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("key file missing: %v", err)
	}
}

func TestGenerateSelfSignedCert_IP(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := GenerateSelfSignedCert(certPath, keyPath, []string{"192.168.1.1"}, 30); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	if len(cert.IPAddresses) == 0 {
		t.Error("expected IP SAN")
	}
	if err := cert.VerifyHostname("192.168.1.1"); err != nil {
		t.Errorf("cert does not match IP: %v", err)
	}
}
