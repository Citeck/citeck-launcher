package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempCert(t *testing.T, dir string, notBefore, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	f, err := os.Create(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	f.Close()
	return certPath
}

func TestRenewalInterval_NormalCert(t *testing.T) {
	dir := t.TempDir()
	writeTempCert(t, dir, time.Now().Add(-30*24*time.Hour), time.Now().Add(60*24*time.Hour))
	client := &Client{confDir: dir}
	svc := NewRenewalService(client, nil)
	interval := svc.renewalInterval()
	if interval != 12*time.Hour {
		t.Errorf("expected 12h for normal cert, got %v", interval)
	}
}

func TestRenewalInterval_ShortLivedCert(t *testing.T) {
	dir := t.TempDir()
	writeTempCert(t, dir, time.Now().Add(-1*24*time.Hour), time.Now().Add(5*24*time.Hour))
	client := &Client{confDir: dir}
	svc := NewRenewalService(client, nil)
	interval := svc.renewalInterval()
	if interval != 6*time.Hour {
		t.Errorf("expected 6h for short-lived cert, got %v", interval)
	}
}

func TestRenewalInterval_NoCert(t *testing.T) {
	dir := t.TempDir()
	client := &Client{confDir: dir}
	svc := NewRenewalService(client, nil)
	interval := svc.renewalInterval()
	if interval != 12*time.Hour {
		t.Errorf("expected 12h default when cert missing, got %v", interval)
	}
}
