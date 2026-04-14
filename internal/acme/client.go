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

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"golang.org/x/crypto/acme"
)

const (
	letsEncryptURL        = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
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
	_ = os.MkdirAll(c.dataDir, 0o755) //nolint:gosec // G301: ACME data dirs need 0o755
	_ = os.MkdirAll(c.confDir, 0o755) //nolint:gosec // G301: ACME conf dirs need 0o755

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
	if _, regErr := client.Register(ctx, acct, acme.AcceptTOS); regErr != nil {
		if !errors.Is(regErr, acme.ErrAccountAlreadyExists) {
			return fmt.Errorf("register account: %w", regErr)
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
		authz, authzErr := client.GetAuthorization(ctx, authzURL)
		if authzErr != nil {
			return fmt.Errorf("get authorization: %w", authzErr)
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
		response, respErr := client.HTTP01ChallengeResponse(token)
		if respErr != nil {
			return fmt.Errorf("challenge response: %w", respErr)
		}
		challengePath := client.HTTP01ChallengePath(token)

		srv := &http.Server{ //nolint:gosec // ACME HTTP-01 challenge requires binding :80 on all interfaces
			Addr:         ":80",
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		}
		mux := http.NewServeMux()
		mux.HandleFunc(challengePath, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(response))
		})
		srv.Handler = mux

		// Start challenge server and wait for it to be ready
		listener, listenErr := net.Listen("tcp", ":80") //nolint:gosec // G102: ACME HTTP-01 challenge requires binding :80 on all interfaces
		if listenErr != nil {
			return fmt.Errorf("listen :80 for ACME challenge: %w", listenErr)
		}
		go func() {
			_ = srv.Serve(listener)
		}()

		// Accept the challenge
		if _, acceptErr := client.Accept(ctx, challenge); acceptErr != nil {
			_ = srv.Close()
			return fmt.Errorf("accept challenge: %w", acceptErr)
		}

		// Wait for authorization
		if _, waitErr := client.WaitAuthorization(ctx, authzURL); waitErr != nil {
			_ = srv.Close()
			return fmt.Errorf("wait authorization: %w", waitErr)
		}

		_ = srv.Close()
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

	// Atomic write: key first, then cert — cert presence signals completion.
	keyPath := filepath.Join(c.confDir, "privkey.pem")
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := fsutil.AtomicWriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write privkey: %w", err)
	}

	// Build fullchain PEM in memory, then write atomically
	chainPath := filepath.Join(c.confDir, "fullchain.pem")
	var chainBuf []byte
	for _, cert := range certs {
		chainBuf = append(chainBuf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})...)
	}
	if err := fsutil.AtomicWriteFile(chainPath, chainBuf, 0o644); err != nil {
		return fmt.Errorf("write fullchain: %w", err)
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

	data, err := os.ReadFile(keyPath) //nolint:gosec // G304: keyPath is derived from internal dataDir
	if err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			key, parseErr := x509.ParseECPrivateKey(block.Bytes)
			if parseErr == nil {
				return key, nil
			}
			slog.Warn("Failed to parse stored account key, regenerating", "err", parseErr)
		}
	}

	// Generate new key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ECDSA key: %w", err)
	}

	// Persist as PEM
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return key, nil // use in memory, persist failed
	}
	_ = os.MkdirAll(filepath.Dir(keyPath), 0o755) //nolint:gosec // G301: ACME key dir needs 0o755
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	_ = os.WriteFile(keyPath, pemData, 0o600)

	return key, nil
}

// TryStaging performs a full ACME flow against the Let's Encrypt staging server
// to verify that the hostname is reachable and HTTP-01 challenge can be completed.
// Uses ephemeral keys and does not save any state.
func TryStaging(ctx context.Context, hostname string) error {
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	client := &acme.Client{
		Key:          accountKey,
		DirectoryURL: letsEncryptStagingURL,
	}

	if _, regErr := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); regErr != nil {
		if !errors.Is(regErr, acme.ErrAccountAlreadyExists) {
			return fmt.Errorf("register: %w", regErr)
		}
	}

	// Use IPIDs + shortlived profile for IP addresses, DomainIDs for hostnames
	var ids []acme.AuthzID
	isIP := net.ParseIP(hostname) != nil
	if isIP {
		ids = acme.IPIDs(hostname)
	} else {
		ids = acme.DomainIDs(hostname)
	}
	var order *acme.Order
	if isIP {
		order, err = authorizeOrderWithProfile(ctx, client, ids, "shortlived")
	} else {
		order, err = client.AuthorizeOrder(ctx, ids)
	}
	if err != nil {
		return fmt.Errorf("order: %w", err)
	}

	for _, authzURL := range order.AuthzURLs {
		if err := tryStagingChallenge(ctx, client, authzURL); err != nil {
			return err
		}
	}

	return nil
}

func tryStagingChallenge(ctx context.Context, client *acme.Client, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("authz: %w", err)
	}

	var challenge *acme.Challenge
	for _, ch := range authz.Challenges {
		if ch.Type == "http-01" {
			challenge = ch
			break
		}
	}
	if challenge == nil {
		return fmt.Errorf("no http-01 challenge in response")
	}

	response, err := client.HTTP01ChallengeResponse(challenge.Token)
	if err != nil {
		return fmt.Errorf("challenge response: %w", err)
	}
	challengePath := client.HTTP01ChallengePath(challenge.Token)

	listener, err := net.Listen("tcp", ":80") //nolint:gosec // ACME HTTP-01 requires :80
	if err != nil {
		return fmt.Errorf("port 80 unavailable: %w", err)
	}
	srv := &http.Server{ //nolint:gosec // ACME challenge server
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(challengePath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(response))
	})
	srv.Handler = mux
	go func() { _ = srv.Serve(listener) }()
	defer func() { _ = srv.Close() }()

	if _, acceptErr := client.Accept(ctx, challenge); acceptErr != nil {
		return fmt.Errorf("accept: %w", acceptErr)
	}

	if _, waitErr := client.WaitAuthorization(ctx, authzURL); waitErr != nil {
		return fmt.Errorf("validation failed: %w", waitErr)
	}

	return nil
}
