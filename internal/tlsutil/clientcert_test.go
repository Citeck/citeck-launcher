package tlsutil

import (
	"path/filepath"
	"testing"

	gopkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodePKCS12_RoundTrip(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "test.crt")

	certPEM, keyPEM, err := GenerateClientCert(certPath, "test-user", 30)
	require.NoError(t, err)
	require.NotEmpty(t, certPEM)
	require.NotEmpty(t, keyPEM)

	// Encode to PKCS12
	p12Data, err := EncodePKCS12(certPEM, keyPEM, "testpass")
	require.NoError(t, err)
	require.NotEmpty(t, p12Data)

	// Decode back
	key, cert, _, err := gopkcs12.DecodeChain(p12Data, "testpass")
	require.NoError(t, err)
	assert.NotNil(t, key)
	assert.Equal(t, "test-user", cert.Subject.CommonName)
}

func TestEncodePKCS12_EmptyPassword(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "test.crt")

	certPEM, keyPEM, err := GenerateClientCert(certPath, "admin", 30)
	require.NoError(t, err)

	p12Data, err := EncodePKCS12(certPEM, keyPEM, "")
	require.NoError(t, err)
	require.NotEmpty(t, p12Data)

	// Decode with empty password
	_, cert, _, err := gopkcs12.DecodeChain(p12Data, "")
	require.NoError(t, err)
	assert.Equal(t, "admin", cert.Subject.CommonName)
}
