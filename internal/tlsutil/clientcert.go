package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	gopkcs12 "software.sslmate.com/src/go-pkcs12"
)

// GenerateClientCert creates a self-signed ECDSA P-256 client certificate.
// It writes ONLY the public cert to certPath; the private key is returned as PEM but never written to disk.
func GenerateClientCert(certPath, cn string, days int) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().Add(-1 * time.Minute) // backdate to avoid clock skew
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now,
		NotAfter:     now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Write ONLY the public cert to disk
	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err != nil {
		return nil, nil, fmt.Errorf("create cert dir: %w", err)
	}
	if err := fsutil.AtomicWriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, nil, fmt.Errorf("write cert: %w", err)
	}

	return certPEM, keyPEM, nil
}

// EncodePKCS12 packages a certificate and private key into a PKCS#12 (.p12) file.
// The password protects the .p12 file; use empty string for no password.
func EncodePKCS12(certPEM, keyPEM []byte, password string) ([]byte, error) {
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("decode private key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	p12Data, err := gopkcs12.Modern.Encode(key, cert, nil, password)
	if err != nil {
		return nil, fmt.Errorf("encode PKCS12: %w", err)
	}
	return p12Data, nil
}

// LoadCACertPool loads all .crt/.pem files from a directory into an x509.CertPool.
// Returns the pool and the count of loaded certs. Empty or missing dir returns an empty pool.
func LoadCACertPool(dir string) (*x509.CertPool, int, error) {
	pool := x509.NewCertPool()
	count := 0

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return pool, 0, nil
		}
		return nil, 0, fmt.Errorf("read CA dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".crt" && ext != ".pem" {
			continue
		}

		filePath := filepath.Join(dir, name)
		data, err := os.ReadFile(filePath) //nolint:gosec // filePath is constructed from directory listing, not user input
		if err != nil {
			slog.Warn("Failed to read CA cert file", "path", filePath, "err", err)
			continue
		}
		if pool.AppendCertsFromPEM(data) {
			count++
		} else {
			slog.Warn("Invalid PEM data in CA cert file", "path", filePath)
		}
	}

	return pool, count, nil
}
