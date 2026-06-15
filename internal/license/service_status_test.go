package license

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// newTestService builds a license.Service over a real SQLite-backed
// SecretService unlocked with the default password — the same wiring the
// daemon uses, so List()/Add() round-trip through actual encryption.
func newTestService(t *testing.T) *Service {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))
	return NewService(svc)
}

// signLicense produces an Instance whose signature verifies against a fresh
// self-signed ECDSA certificate — i.e. a license that passes ValidAt within
// its [ValidFrom, ValidUntil] window, exactly like a production-signed one.
func signLicense(t *testing.T, lic Instance) Instance {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Citeck Test CA", Organization: []string{"Citeck"}},
		NotBefore:             time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	content, err := lic.ContentForSign()
	require.NoError(t, err)

	sigTime := "2025-01-01T00:00:00Z"
	// Signed data = canonical content ++ ISO8601 time bytes (Kotlin parity).
	digest := sha256.Sum256(append(append([]byte{}, content...), []byte(sigTime)...))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	require.NoError(t, err)

	lic.Signatures = []Signature{{
		Time:         sigTime,
		Issuer:       cert.Issuer.String(),
		Signature:    sig,
		Certificates: [][]byte{certDER},
	}}
	return lic
}

func validLicense(t *testing.T, id, tenant string, from, until time.Time) Instance {
	t.Helper()
	return signLicense(t, Instance{
		ID:         id,
		Tenant:     tenant,
		IssuedTo:   tenant + " Corp",
		ValidFrom:  Time{from},
		ValidUntil: Time{until},
	})
}

func TestHasValidEnterprise(t *testing.T) {
	now := time.Now()

	t.Run("empty store is community", func(t *testing.T) {
		svc := newTestService(t)
		assert.False(t, svc.HasValidEnterprise())
	})

	t.Run("unsigned license does not count", func(t *testing.T) {
		svc := newTestService(t)
		require.NoError(t, svc.Add(Instance{
			ID: "stub", Tenant: "acme",
			ValidFrom:  Time{now.Add(-time.Hour)},
			ValidUntil: Time{now.Add(24 * time.Hour)},
		}))
		assert.False(t, svc.HasValidEnterprise())
	})

	t.Run("expired signed license does not count", func(t *testing.T) {
		svc := newTestService(t)
		lic := validLicense(t, "old", "acme", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
		require.NoError(t, svc.Add(lic))
		assert.False(t, svc.HasValidEnterprise())
	})

	t.Run("valid signed license counts", func(t *testing.T) {
		svc := newTestService(t)
		lic := validLicense(t, "live", "acme", now.Add(-time.Hour), now.Add(30*24*time.Hour))
		require.NoError(t, svc.Add(lic))
		assert.True(t, svc.HasValidEnterprise())
	})
}

func TestStatusAt(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	t.Run("empty store yields zero summary", func(t *testing.T) {
		svc := newTestService(t)
		st, err := svc.StatusAt(now)
		require.NoError(t, err)
		assert.Equal(t, StatusSummary{}, st)
	})

	t.Run("valid license reports enterprise with days left", func(t *testing.T) {
		svc := newTestService(t)
		until := now.Add(36 * time.Hour) // 1.5 days → ceil → 2
		require.NoError(t, svc.Add(validLicense(t, "live", "acme", now.Add(-time.Hour), until)))
		st, err := svc.StatusAt(now)
		require.NoError(t, err)
		assert.True(t, st.Enterprise)
		assert.Equal(t, "acme", st.Tenant)
		assert.Equal(t, "acme Corp", st.IssuedTo)
		assert.True(t, st.ValidUntil.Equal(until))
		assert.Equal(t, 2, st.DaysLeft, "36h remaining rounds up to 2 whole days")
	})

	t.Run("higher-priority valid license wins", func(t *testing.T) {
		svc := newTestService(t)
		low := validLicense(t, "low", "low-tenant", now.Add(-time.Hour), now.Add(48*time.Hour))
		low.Priority = 1
		// Re-sign after the priority change — Priority is part of the signed
		// canonical form, so mutating it would invalidate the signature.
		low.Signatures = nil
		low = signLicense(t, low)
		high := validLicense(t, "high", "high-tenant", now.Add(-time.Hour), now.Add(24*time.Hour))
		high.Priority = 9
		high.Signatures = nil
		high = signLicense(t, high)
		require.NoError(t, svc.Add(low))
		require.NoError(t, svc.Add(high))

		st, err := svc.StatusAt(now)
		require.NoError(t, err)
		assert.True(t, st.Enterprise)
		assert.Equal(t, "high-tenant", st.Tenant)
	})

	t.Run("expired-only records report tenant without enterprise", func(t *testing.T) {
		svc := newTestService(t)
		until := now.Add(-24 * time.Hour)
		require.NoError(t, svc.Add(validLicense(t, "old", "acme", now.Add(-72*time.Hour), until)))
		st, err := svc.StatusAt(now)
		require.NoError(t, err)
		assert.False(t, st.Enterprise)
		assert.Equal(t, "acme", st.Tenant)
		assert.True(t, st.ValidUntil.Equal(until))
		assert.LessOrEqual(t, st.DaysLeft, 0, "expired license must not report days left")
	})

	t.Run("latest-expiring record is the effective expired one", func(t *testing.T) {
		svc := newTestService(t)
		older := validLicense(t, "older", "first", now.Add(-96*time.Hour), now.Add(-48*time.Hour))
		newer := validLicense(t, "newer", "second", now.Add(-72*time.Hour), now.Add(-24*time.Hour))
		require.NoError(t, svc.Add(older))
		require.NoError(t, svc.Add(newer))
		st, err := svc.StatusAt(now)
		require.NoError(t, err)
		assert.False(t, st.Enterprise)
		assert.Equal(t, "second", st.Tenant)
	})
}

func TestDaysLeft(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, 0, daysLeft(time.Time{}, now), "zero ValidUntil reports 0")
	assert.Equal(t, 1, daysLeft(now.Add(time.Hour), now), "partial day rounds up")
	assert.Equal(t, 14, daysLeft(now.Add(14*24*time.Hour), now))
	assert.Equal(t, -1, daysLeft(now.Add(-36*time.Hour), now), "expired goes negative")
}
