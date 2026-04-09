package namespace

import (
	"fmt"
	"strings"
)

// SecretReader resolves secret values by key.
type SecretReader interface {
	GetSecretValue(key string) (string, error)
}

// resolveSecret returns the actual value for a config field.
// If the value has "secret:" prefix, it's resolved via SecretReader.
// Otherwise returned as-is.
func resolveSecret(reader SecretReader, value string) (string, error) {
	if !strings.HasPrefix(value, "secret:") {
		return value, nil
	}
	if reader == nil {
		return "", fmt.Errorf("secret reader not available, cannot resolve %q", value)
	}
	key := strings.TrimPrefix(value, "secret:")
	val, err := reader.GetSecretValue(key)
	if err != nil {
		return "", fmt.Errorf("resolve secret %q: %w", key, err)
	}
	return val, nil
}
