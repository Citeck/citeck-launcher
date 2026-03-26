package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme"
)

const (
	letsEncryptURL = "https://acme-v02.api.letsencrypt.org/directory"
)

// Client handles ACME certificate provisioning via Let's Encrypt.
type Client struct {
	dataDir  string // directory for account key and state
	confDir  string // directory for TLS certs (fullchain.pem, privkey.pem)
	hostname string
}

// NewClient creates a new ACME client.
func NewClient(dataDir, confDir, hostname string) *Client {
	return &Client{
		dataDir:  filepath.Join(dataDir, "acme"),
		confDir:  filepath.Join(confDir, "tls"),
		hostname: hostname,
	}
}

// ObtainCertificate performs the full ACME flow: account registration, order, HTTP-01 challenge, CSR, certificate download.
func (c *Client) ObtainCertificate(ctx context.Context) error {
	os.MkdirAll(c.dataDir, 0o755)
	os.MkdirAll(c.confDir, 0o755)

	// Load or create account key
	accountKey, err := c.loadOrCreateAccountKey()
	if err != nil {
		return fmt.Errorf("account key: %w", err)
	}

	client := &acme.Client{
		Key:          accountKey,
		DirectoryURL: letsEncryptURL,
	}

	// Register account (idempotent — auto-accepts TOS)
	acct := &acme.Account{}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil {
		if !errors.Is(err, acme.ErrAccountAlreadyExists) {
			return fmt.Errorf("register account: %w", err)
		}
	}

	// Create order — use IPIDs for IP addresses, DomainIDs for hostnames.
	var ids []acme.AuthzID
	isIP := net.ParseIP(c.hostname) != nil
	if isIP {
		ids = acme.IPIDs(c.hostname)
		slog.Info("Requesting short-lived IP certificate (~6 days)", "ip", c.hostname)
	} else {
		ids = acme.DomainIDs(c.hostname)
	}

	var order *acme.Order
	if isIP {
		// IP certs require "shortlived" ACME profile — use custom JWS request
		order, err = authorizeOrderWithProfile(ctx, client, ids, "shortlived")
	} else {
		order, err = client.AuthorizeOrder(ctx, ids)
	}
	if err != nil {
		return fmt.Errorf("authorize order: %w", err)
	}

	// Process HTTP-01 challenges
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return fmt.Errorf("get authorization: %w", err)
		}

		var challenge *acme.Challenge
		for _, ch := range authz.Challenges {
			if ch.Type == "http-01" {
				challenge = ch
				break
			}
		}
		if challenge == nil {
			return fmt.Errorf("no http-01 challenge found for %s", c.hostname)
		}

		// Start temporary HTTP server on :80 for challenge
		token := challenge.Token
		response, err := client.HTTP01ChallengeResponse(token)
		if err != nil {
			return fmt.Errorf("challenge response: %w", err)
		}
		challengePath := client.HTTP01ChallengePath(token)

		srv := &http.Server{Addr: ":80"}
		mux := http.NewServeMux()
		mux.HandleFunc(challengePath, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(response))
		})
		srv.Handler = mux

		// Start challenge server and wait for it to be ready
		listener, listenErr := net.Listen("tcp", ":80")
		if listenErr != nil {
			return fmt.Errorf("listen :80 for ACME challenge: %w", listenErr)
		}
		go func() {
			srv.Serve(listener)
		}()

		// Accept the challenge
		if _, err := client.Accept(ctx, challenge); err != nil {
			srv.Close()
			return fmt.Errorf("accept challenge: %w", err)
		}

		// Wait for authorization
		if _, err := client.WaitAuthorization(ctx, authzURL); err != nil {
			srv.Close()
			return fmt.Errorf("wait authorization: %w", err)
		}

		srv.Close()
	}

	// Generate certificate key
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate cert key: %w", err)
	}

	// Create CSR — for IP addresses, use only SAN (no CommonName with IP per LE rules)
	csrTemplate := &x509.CertificateRequest{}
	if ip := net.ParseIP(c.hostname); ip != nil {
		csrTemplate.IPAddresses = []net.IP{ip}
	} else {
		csrTemplate.Subject = pkix.Name{CommonName: c.hostname}
		csrTemplate.DNSNames = []string{c.hostname}
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, certKey)
	if err != nil {
		return fmt.Errorf("create CSR: %w", err)
	}

	// Wait for order to be ready
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return fmt.Errorf("wait order: %w", err)
	}

	// Finalize order with CSR
	certs, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}

	// Write fullchain.pem
	chainPath := filepath.Join(c.confDir, "fullchain.pem")
	chainFile, err := os.OpenFile(chainPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create fullchain: %w", err)
	}
	defer chainFile.Close()
	for _, cert := range certs {
		if err := pem.Encode(chainFile, &pem.Block{Type: "CERTIFICATE", Bytes: cert}); err != nil {
			return fmt.Errorf("write certificate: %w", err)
		}
	}

	// Write privkey.pem
	keyPath := filepath.Join(c.confDir, "privkey.pem")
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create privkey: %w", err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	slog.Info("ACME certificate obtained", "hostname", c.hostname, "cert", chainPath)
	return nil
}

// CertPath returns the path to the fullchain PEM file.
func (c *Client) CertPath() string {
	return filepath.Join(c.confDir, "fullchain.pem")
}

// KeyPath returns the path to the private key PEM file.
func (c *Client) KeyPath() string {
	return filepath.Join(c.confDir, "privkey.pem")
}

// CertMatchesHost returns true if the existing cert at CertPath() is valid
// for the configured hostname and has not expired.
func (c *Client) CertMatchesHost() bool {
	data, err := os.ReadFile(c.CertPath())
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return cert.VerifyHostname(c.hostname) == nil && time.Now().Before(cert.NotAfter)
}

// loadOrCreateAccountKey loads the account key from disk, or generates a new one.
// Uses PEM-encoded ECDSA key for safe serialization/deserialization.
func (c *Client) loadOrCreateAccountKey() (*ecdsa.PrivateKey, error) {
	keyPath := filepath.Join(c.dataDir, "account-key.pem")

	data, err := os.ReadFile(keyPath)
	if err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err == nil {
				return key, nil
			}
			slog.Warn("Failed to parse stored account key, regenerating", "err", err)
		}
	}

	// Generate new key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	// Persist as PEM
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return key, nil // use in memory, persist failed
	}
	os.MkdirAll(filepath.Dir(keyPath), 0o755)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	os.WriteFile(keyPath, pemData, 0o600)

	return key, nil
}
