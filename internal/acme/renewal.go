package acme

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"os"
	"time"
)

// RenewalService checks certificate expiry periodically and renews if needed.
type RenewalService struct {
	client    *Client
	restartFn func() // called after successful renewal to restart proxy
	cancel    context.CancelFunc
}

// NewRenewalService creates a renewal service.
// restartFn is called after successful cert renewal (e.g., to restart the proxy container).
func NewRenewalService(client *Client, restartFn func()) *RenewalService {
	return &RenewalService{
		client:    client,
		restartFn: restartFn,
	}
}

// Start begins the renewal check loop: immediate check on start, then periodically.
// For short-lived certs (< 30 days), checks every 6 hours; otherwise every 12 hours.
func (s *RenewalService) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	interval := s.renewalInterval()
	slog.Info("ACME renewal service started", "interval", interval)

	go func() {
		s.checkAndRenew(ctx) // immediate check on startup

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.checkAndRenew(ctx)
			}
		}
	}()
}

// Stop stops the renewal service.
func (s *RenewalService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// renewalInterval returns the check interval based on current cert validity.
// Short-lived certs (< 30 days) get 6h interval, otherwise 12h.
func (s *RenewalService) renewalInterval() time.Duration {
	certPath := s.client.CertPath()
	data, err := os.ReadFile(certPath)
	if err != nil {
		return 12 * time.Hour
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return 12 * time.Hour
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 12 * time.Hour
	}
	total := cert.NotAfter.Sub(cert.NotBefore)
	if total < 30*24*time.Hour {
		return 6 * time.Hour // short-lived cert (IP certs ~6 days)
	}
	return 12 * time.Hour
}

func (s *RenewalService) checkAndRenew(ctx context.Context) {
	certPath := s.client.CertPath()
	data, err := os.ReadFile(certPath)
	if err != nil {
		slog.Warn("ACME renewal: cannot read cert", "err", err)
		return
	}

	block, _ := pem.Decode(data)
	if block == nil {
		slog.Warn("ACME renewal: invalid PEM")
		return
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		slog.Warn("ACME renewal: parse cert failed", "err", err)
		return
	}

	// Renew if remaining validity < 50% of total validity
	total := cert.NotAfter.Sub(cert.NotBefore)
	remaining := time.Until(cert.NotAfter)

	if remaining > total/2 {
		slog.Debug("ACME cert valid", "daysLeft", int(remaining.Hours()/24))
		return
	}

	slog.Info("ACME cert renewal needed", "daysLeft", int(remaining.Hours()/24))

	if err := s.client.ObtainCertificate(ctx); err != nil {
		slog.Error("ACME renewal failed", "err", err)
		return
	}

	slog.Info("ACME cert renewed successfully")
	if s.restartFn != nil {
		s.restartFn()
	}
}
