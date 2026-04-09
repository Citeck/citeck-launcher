package namespace

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSecretReader is a test double for SecretReader.
type mockSecretReader struct {
	secrets map[string]string
}

func (m *mockSecretReader) GetSecretValue(key string) (string, error) {
	v, ok := m.secrets[key]
	if !ok {
		return "", fmt.Errorf("secret %q not found", key)
	}
	return v, nil
}

func TestResolveSecret_PlainValue(t *testing.T) {
	val, err := resolveSecret(nil, "plain-value")
	require.NoError(t, err)
	assert.Equal(t, "plain-value", val)
}

func TestResolveSecret_EmptyValue(t *testing.T) {
	val, err := resolveSecret(nil, "")
	require.NoError(t, err)
	assert.Empty(t, val)
}

func TestResolveSecret_SecretRef(t *testing.T) {
	reader := &mockSecretReader{secrets: map[string]string{
		"smtp-password": "s3cret!",
	}}
	val, err := resolveSecret(reader, "secret:smtp-password")
	require.NoError(t, err)
	assert.Equal(t, "s3cret!", val)
}

func TestResolveSecret_SecretNotFound(t *testing.T) {
	reader := &mockSecretReader{secrets: map[string]string{}}
	_, err := resolveSecret(reader, "secret:missing-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-key")
}

func TestResolveSecret_NilReader(t *testing.T) {
	_, err := resolveSecret(nil, "secret:some-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret reader not available")
}
