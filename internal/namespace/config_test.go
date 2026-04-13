package namespace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNamespaceConfig_BasicAuth(t *testing.T) {
	yaml := `
id: "test"
name: "Test Namespace"
authentication:
  type: BASIC
  users:
    - "admin"
    - "user1"
bundleRef: "community:2025.12"
proxy:
  port: 80
  host: "localhost"
`
	cfg, err := ParseNamespaceConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ID != "test" {
		t.Errorf("expected id=test, got %s", cfg.ID)
	}
	if cfg.Name != "Test Namespace" {
		t.Errorf("expected name='Test Namespace', got %s", cfg.Name)
	}
	if cfg.Authentication.Type != AuthBasic {
		t.Errorf("expected auth type BASIC, got %s", cfg.Authentication.Type)
	}
	if len(cfg.Authentication.Users) != 2 {
		t.Errorf("expected 2 users, got %d", len(cfg.Authentication.Users))
	}
	if cfg.BundleRef.Repo != "community" {
		t.Errorf("expected bundle repo=community, got %s", cfg.BundleRef.Repo)
	}
	if cfg.BundleRef.Key != "2025.12" {
		t.Errorf("expected bundle key=2025.12, got %s", cfg.BundleRef.Key)
	}
}

func TestParseNamespaceConfig_KeycloakTLS(t *testing.T) {
	yaml := `
id: "prod"
name: "Production"
authentication:
  type: KEYCLOAK
  users:
    - "admin"
bundleRef: "community:2025.12"
proxy:
  port: 443
  host: "custom.launcher.ru"
  tls:
    enabled: true
    certPath: "/opt/citeck/conf/tls/server.crt"
    keyPath: "/opt/citeck/conf/tls/server.key"
`
	cfg, err := ParseNamespaceConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Authentication.Type != AuthKeycloak {
		t.Errorf("expected KEYCLOAK, got %s", cfg.Authentication.Type)
	}
	if cfg.Proxy.Port != 443 {
		t.Errorf("expected port 443, got %d", cfg.Proxy.Port)
	}
	if cfg.Proxy.Host != "custom.launcher.ru" {
		t.Errorf("expected host custom.launcher.ru, got %s", cfg.Proxy.Host)
	}
	if !cfg.Proxy.TLS.Enabled {
		t.Error("expected TLS enabled")
	}
	if cfg.Proxy.TLS.CertPath != "/opt/citeck/conf/tls/server.crt" {
		t.Errorf("unexpected cert path: %s", cfg.Proxy.TLS.CertPath)
	}
}

func TestEmailConfig_MarshalRoundTrip(t *testing.T) {
	cfg := &Config{
		ID:    "test",
		Proxy: ProxyProps{Port: 80},
		Authentication: AuthenticationProps{
			Type:  AuthBasic,
			Users: []string{"admin"},
		},
		Email: &EmailConfig{
			Host:     "smtp.example.com",
			Port:     587,
			Username: "user@example.com",
			Password: "secret:email.password",
			From:     "noreply@example.com",
			TLS:      true,
		},
	}
	data, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(data), "email:")
	assert.Contains(t, string(data), "host: smtp.example.com")
	assert.Contains(t, string(data), "secret:email.password")

	parsed, err := ParseNamespaceConfig(data)
	require.NoError(t, err)
	require.NotNil(t, parsed.Email)
	assert.Equal(t, "smtp.example.com", parsed.Email.Host)
	assert.Equal(t, 587, parsed.Email.Port)
	assert.Equal(t, "secret:email.password", parsed.Email.Password)
	assert.True(t, parsed.Email.TLS)
}

func TestS3Config_MarshalRoundTrip(t *testing.T) {
	cfg := &Config{
		ID:    "test",
		Proxy: ProxyProps{Port: 80},
		Authentication: AuthenticationProps{
			Type:  AuthBasic,
			Users: []string{"admin"},
		},
		S3: &S3Config{
			Endpoint:  "https://s3.example.com",
			Bucket:    "citeck-content",
			AccessKey: "AKIAIOSFODNN7",
			SecretKey: "secret:s3.secretKey",
			Region:    "us-east-1",
		},
	}
	data, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(data), "s3:")
	assert.Contains(t, string(data), "bucket: citeck-content")

	parsed, err := ParseNamespaceConfig(data)
	require.NoError(t, err)
	require.NotNil(t, parsed.S3)
	assert.Equal(t, "https://s3.example.com", parsed.S3.Endpoint)
	assert.Equal(t, "secret:s3.secretKey", parsed.S3.SecretKey)
	assert.Equal(t, "us-east-1", parsed.S3.Region)
}

func TestEmailConfig_NilOmitted(t *testing.T) {
	cfg := &Config{ID: "test"}
	data, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "email:")
	assert.NotContains(t, string(data), "s3:")
}

func TestEmailConfig_OptionalAuth(t *testing.T) {
	cfg := &Config{
		ID:    "test",
		Proxy: ProxyProps{Port: 80},
		Authentication: AuthenticationProps{
			Type:  AuthBasic,
			Users: []string{"admin"},
		},
		Email: &EmailConfig{
			Host: "relay.internal",
			Port: 25,
			From: "noreply@company.com",
		},
	}
	data, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "username:")
	assert.NotContains(t, string(data), "password:")

	parsed, err := ParseNamespaceConfig(data)
	require.NoError(t, err)
	assert.Empty(t, parsed.Email.Username)
	assert.Empty(t, parsed.Email.Password)
}

func TestParseNamespaceConfig_Defaults(t *testing.T) {
	yaml := `
name: "Minimal"
bundleRef: "community:LATEST"
`
	cfg, err := ParseNamespaceConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ID != "default" {
		t.Errorf("expected default ID, got %s", cfg.ID)
	}
	if cfg.Authentication.Type != AuthBasic {
		t.Errorf("expected default auth type BASIC, got %s", cfg.Authentication.Type)
	}
	if !cfg.PgAdmin.Enabled {
		t.Error("expected PgAdmin enabled by default")
	}
}
