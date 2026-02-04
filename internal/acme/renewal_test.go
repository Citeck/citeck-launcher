package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func writeTempCert(t *testing.T, dir string, notBefore, notAfter time.Time) {
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
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
}

func TestRenewalInterval_NormalCert(t *testing.T) {
	dir := t.TempDir()
	// 90 days total validity → should use 12h interval
	writeTempCert(t, dir, time.Now().Add(-30*24*time.Hour), time.Now().Add(60*24*time.Hour))
	client := &Client{confDir: dir}
	svc := NewRenewalService(client, nil)
	interval := svc.renewalInterval()
	if interval != 12*time.Hour {
		t.Errorf("expected 12h for 90-day cert, got %v", interval)
	}
}

func TestRenewalInterval_ShortLivedCert(t *testing.T) {
	dir := t.TempDir()
	// 6 days total validity → should use 6h interval (IP cert pattern)
	writeTempCert(t, dir, time.Now().Add(-1*24*time.Hour), time.Now().Add(5*24*time.Hour))
	client := &Client{confDir: dir}
	svc := NewRenewalService(client, nil)
	interval := svc.renewalInterval()
	if interval != 6*time.Hour {
		t.Errorf("expected 6h for 6-day cert, got %v", interval)
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

func TestCheckAndRenew_SkipsWhenMoreThanHalfValid(t *testing.T) {
	dir := t.TempDir()
	// Cert valid for 90 days, 80 days remaining → >50%, should NOT renew
	writeTempCert(t, dir, time.Now().Add(-10*24*time.Hour), time.Now().Add(80*24*time.Hour))
	client := &Client{confDir: dir}

	var renewed atomic.Bool
	svc := NewRenewalService(client, func() { renewed.Store(true) })

	ctx := context.Background()
	svc.checkAndRenew(ctx) // should be a no-op (cert is still >50% valid)

	if renewed.Load() {
		t.Error("restartFn should not be called when cert has >50% validity remaining")
	}
}

// writeTempCertWithSAN creates a cert with a DNS SAN for hostname verification tests.
func writeTempCertWithSAN(t *testing.T, dir, hostname string, notBefore, notAfter time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "fullchain.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
}

func TestCertMatchesHost_Valid(t *testing.T) {
	dir := t.TempDir()
	writeTempCertWithSAN(t, dir, "example.com", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	client := &Client{confDir: dir, hostname: "example.com"}
	if !client.CertMatchesHost() {
		t.Error("expected CertMatchesHost=true for matching hostname")
	}
}

func TestCertMatchesHost_WrongHost(t *testing.T) {
	dir := t.TempDir()
	writeTempCertWithSAN(t, dir, "example.com", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	client := &Client{confDir: dir, hostname: "other.com"}
	if client.CertMatchesHost() {
		t.Error("expected CertMatchesHost=false for wrong hostname")
	}
}

func TestCertMatchesHost_Expired(t *testing.T) {
	dir := t.TempDir()
	writeTempCertWithSAN(t, dir, "example.com", time.Now().Add(-48*time.Hour), time.Now().Add(-1*time.Hour))
	client := &Client{confDir: dir, hostname: "example.com"}
	if client.CertMatchesHost() {
		t.Error("expected CertMatchesHost=false for expired cert")
	}
}

func TestCertMatchesHost_NoCert(t *testing.T) {
	dir := t.TempDir()
	client := &Client{confDir: dir, hostname: "example.com"}
	if client.CertMatchesHost() {
		t.Error("expected CertMatchesHost=false when no cert exists")
	}
}
