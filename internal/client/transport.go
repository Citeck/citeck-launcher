package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
)

// TransportType indicates the daemon connection mechanism.
type TransportType int

// Transport type constants.
const (
	TransportUnix TransportType = iota
	TransportTCP
)

// TransportConfig holds resolved connection parameters for the daemon.
type TransportConfig struct {
	Type       TransportType
	SocketPath string
	Host       string
	TLSCert    string // client cert path for mTLS
	TLSKey     string // client key path for mTLS
	ServerCert string // pin server cert (adds to TLS root CAs)
	Insecure   bool   // skip server cert verification
}

// DetectTransport probes Unix socket or TCP to determine how to reach the daemon.
func DetectTransport(host, tlsCert, tlsKey, serverCert string, insecure bool) (*TransportConfig, error) {
	// Explicit host flag or CITECK_HOST env var → TCP
	if host == "" {
		host = os.Getenv("CITECK_HOST")
	}
	if host != "" {
		tc := &TransportConfig{
			Type:       TransportTCP,
			Host:       host,
			TLSCert:    tlsCert,
			TLSKey:     tlsKey,
			ServerCert: serverCert,
			Insecure:   insecure,
		}
		tc.autoDiscoverTLS()
		return tc, nil
	}

	// Local Unix socket
	socketPath := config.SocketPath()
	// Verify the socket is live by actually connecting, not just checking the file.
	// A stale socket (e.g. after kill -9) would pass os.Stat but fail dial.
	if conn, err := net.DialTimeout("unix", socketPath, 2*time.Second); err == nil {
		_ = conn.Close()
		return &TransportConfig{
			Type:       TransportUnix,
			SocketPath: socketPath,
		}, nil
	}

	// Dial failed — distinguish three cases for a truthful message (B7-07):
	//   1. socket path does not exist            → "no socket"
	//   2. path exists but is NOT a unix socket  → "stale socket file"
	//      (e.g. 0-byte regular file left behind after a crash)
	//   3. path exists and IS a socket but nobody is listening
	//                                            → "stale socket"
	if fi, err := os.Stat(socketPath); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("daemon is not running (stale socket file at %s — not a unix socket; remove it and retry)", socketPath)
		}
		return nil, fmt.Errorf("daemon is not running (stale socket at %s — no listener)", socketPath)
	}
	return nil, fmt.Errorf("daemon is not running (no socket at %s)", socketPath)
}

// autoDiscoverTLS fills in TLS fields from env vars and local config.
func (tc *TransportConfig) autoDiscoverTLS() {
	if tc.TLSCert == "" {
		tc.TLSCert = os.Getenv("CITECK_TLS_CERT")
	}
	if tc.TLSKey == "" {
		tc.TLSKey = os.Getenv("CITECK_TLS_KEY")
	}
	// Auto-load server cert from local confdir only for non-localhost hosts.
	// Avoids accidentally promoting localhost connections to HTTPS.
	if tc.ServerCert == "" && !tc.Insecure && !isLocalhostHost(tc.Host) {
		localCert := filepath.Join(config.WebUITLSDir(), "server.crt")
		if _, err := os.Stat(localCert); err == nil {
			tc.ServerCert = localCert
		}
	}
}

// useTLS returns true if the transport should use HTTPS.
func (tc *TransportConfig) useTLS() bool {
	return tc.TLSCert != "" || tc.Insecure || tc.ServerCert != ""
}

// buildTLSConfig creates a tls.Config for the client.
func (tc *TransportConfig) buildTLSConfig() (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load client certificate for mTLS
	if tc.TLSCert != "" && tc.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(tc.TLSCert, tc.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// Pin server certificate
	if tc.ServerCert != "" {
		data, err := os.ReadFile(tc.ServerCert)
		if err != nil {
			return nil, fmt.Errorf("read server cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("invalid server cert PEM: %s", tc.ServerCert)
		}
		tlsCfg.RootCAs = pool
	}

	if tc.Insecure {
		tlsCfg.InsecureSkipVerify = true
	}

	return tlsCfg, nil
}

// newTransport creates an http.Transport with optional TLS config.
// timeout=0 means no overall client timeout (for streaming).
func (tc *TransportConfig) newHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	switch tc.Type {
	case TransportUnix:
		return &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", tc.SocketPath, 5*time.Second)
				},
			},
		}
	case TransportTCP:
		if tc.useTLS() {
			tlsCfg, err := tc.buildTLSConfig()
			if err != nil {
				// Caller should have validated via buildTLSConfig already;
				// this is a safety fallback that will fail on first request.
				return &http.Client{Timeout: timeout}
			}
			return &http.Client{
				Timeout: timeout,
				Transport: &http.Transport{
					TLSClientConfig: tlsCfg,
				},
			}
		}
		return &http.Client{Timeout: timeout}
	default:
		return &http.Client{Timeout: timeout}
	}
}

// NewHTTPClient creates an HTTP client with a 120-second timeout.
// Operations like admin password change involve kcadm.sh exec inside
// Keycloak (Java cold-start) and can take 30-60 seconds on slow hardware.
func NewHTTPClient(tc *TransportConfig) *http.Client {
	return tc.newHTTPClientWithTimeout(120 * time.Second)
}

// NewStreamingHTTPClient creates an HTTP client without an overall timeout,
// suitable for long-lived streaming connections (log follow, SSE events).
func NewStreamingHTTPClient(tc *TransportConfig) *http.Client {
	return tc.newHTTPClientWithTimeout(0)
}

// BaseURL returns the HTTP(S) base URL for API calls.
func (tc *TransportConfig) BaseURL() string {
	switch tc.Type {
	case TransportUnix:
		return "http://localhost"
	case TransportTCP:
		if tc.useTLS() {
			return "https://" + tc.Host
		}
		return "http://" + tc.Host
	default:
		return "http://localhost"
	}
}

// isLocalhostHost returns true if the host is localhost or a loopback IP.
func isLocalhostHost(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
