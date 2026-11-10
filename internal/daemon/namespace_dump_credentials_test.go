package daemon

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
)

func assertDumpCredentialsMasked(t *testing.T, data string) {
	t.Helper()
	for _, secret := range []string{"smtp-literal-unique", "s3-literal-unique", "auth-literal-unique"} {
		require.NotContains(t, data, secret)
	}
	require.Contains(t, data, "***")
	require.Contains(t, data, "smtp.example")
	require.Contains(t, data, "mailer")
	require.Contains(t, data, "https://s3.example")
	require.Contains(t, data, "identity")
}

func TestSystemDumpMasksNamespaceCredentials(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	for _, value := range []string{"literal", "secret:stored", ""} {
		t.Run(value, func(t *testing.T) {
			d := mcpFixture(t)
			cfg := d.activeNs.nsConfig
			email := &namespace.EmailConfig{Host: "smtp.example", Username: "mailer", Password: value}
			s3 := &namespace.S3Config{Endpoint: "https://s3.example", AccessKey: "identity", SecretKey: value}
			if value == "literal" {
				email.Password = "smtp-literal-unique"
				s3.SecretKey = "s3-literal-unique"
			}
			originalEmail, originalS3 := *email, *s3
			cfg.Email, cfg.S3 = email, s3
			cfg.Authentication.Users = []string{"admin:auth-literal-unique"}
			// Exercise the HTTP handler, with a loaded stopped namespace and no Docker.
			rec := httptest.NewRecorder()
			d.handleSystemDump(rec, httptest.NewRequest(http.MethodGet, "/system/dump?format=zip", http.NoBody))
			entries := readDumpZip(t, rec.Body.Bytes())
			data := entries["namespace.yml"]
			require.NotEmpty(t, data)
			assertDumpCredentialsMasked(t, data)
			if value == "secret:stored" {
				require.Contains(t, data, value)
			}
			masked := maskNamespaceConfigSecrets(cfg)
			expected := value
			if value == "literal" {
				expected = "***"
			}
			require.Equal(t, expected, masked.Email.Password)
			require.Equal(t, expected, masked.S3.SecretKey)
			require.Same(t, email, cfg.Email)
			require.Same(t, s3, cfg.S3)
			require.Equal(t, originalEmail, *email)
			require.Equal(t, originalS3, *s3)
			require.Equal(t, []string{"admin:auth-literal-unique"}, cfg.Authentication.Users)
		})
	}
	cfg := &namespace.Config{}
	masked := maskNamespaceConfigSecrets(cfg)
	require.Nil(t, masked.Email)
	require.Nil(t, masked.S3)
}

func readDumpZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	entries := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		require.NoError(t, err)
		b, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		entries[f.Name] = string(b)
	}
	return entries
}
