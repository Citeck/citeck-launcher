package setup

import (
	"os"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	i18n.InitI18n("en")
	os.Exit(m.Run())
}

// --- hostname ---

func TestHostnameSetting_CurrentValue(t *testing.T) {
	s := &hostnameSetting{}
	cfg := &namespace.Config{Proxy: namespace.ProxyProps{Host: "app.example.com"}}
	assert.Equal(t, "app.example.com", s.CurrentValue(cfg, nil))

	cfg.Proxy.Host = ""
	assert.Equal(t, i18n.T("setup.value.not_configured"), s.CurrentValue(cfg, nil))
}

func TestHostnameSetting_Metadata(t *testing.T) {
	s := &hostnameSetting{}
	assert.Equal(t, "hostname", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
	assert.True(t, s.Available(nil, nil))
}

// TestValidateHostname verifies the regex-free, metacharacter-based
// validation rejects shell-injection vectors (quotes, $, backticks,
// pipes, semicolons, ampersands, whitespace) while still accepting
// plain DNS names and IPs. A permissive validator here would defeat
// the shquote hardening in the keycloak init script for callers that
// inspect cfg.Proxy.Host without re-quoting.
func TestValidateHostname(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"plain_dns", "example.com", false},
		{"subdomain", "app.example.com", false},
		{"ipv4", "192.168.1.1", false},
		{"ipv6", "::1", false},
		{"trimmed_whitespace", "  example.com  ", false},
		{"empty", "", true},
		{"all_whitespace", "   ", true},
		{"embedded_space", "ex ample.com", true},
		{"tab", "ex\tample.com", true},
		{"newline", "ex\nample.com", true},
		{"single_quote", "ex'ample.com", true},
		{"double_quote", `ex"ample.com`, true},
		{"backslash", `ex\ample.com`, true},
		{"dollar", "ex$(curl evil)ample.com", true},
		{"backtick", "ex`cmd`ample.com", true},
		{"semicolon", "example.com;ls", true},
		{"ampersand", "example.com&bg", true},
		{"pipe", "example.com|nc evil", true},
		{"too_long", strings.Repeat("a", 254), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHostname(tc.in)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// --- port ---

func TestPortSetting_CurrentValue(t *testing.T) {
	s := &portSetting{}
	cfg := &namespace.Config{Proxy: namespace.ProxyProps{Port: 443}}
	assert.Equal(t, "443", s.CurrentValue(cfg, nil))

	cfg.Proxy.Port = 0
	assert.Equal(t, i18n.T("setup.value.not_configured"), s.CurrentValue(cfg, nil))
}

func TestPortSetting_Metadata(t *testing.T) {
	s := &portSetting{}
	assert.Equal(t, "port", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
	assert.True(t, s.Available(nil, nil))
}

// --- language ---

func TestLanguageSetting_CurrentValue(t *testing.T) {
	s := &languageSetting{}
	dcfg := &config.DaemonConfig{Locale: "ru"}
	assert.Equal(t, "Русский (ru)", s.CurrentValue(nil, dcfg))

	dcfg.Locale = ""
	assert.Equal(t, "English (en)", s.CurrentValue(nil, dcfg))

	dcfg.Locale = "unknown"
	assert.Equal(t, "unknown", s.CurrentValue(nil, dcfg))
}

func TestLanguageSetting_Metadata(t *testing.T) {
	s := &languageSetting{}
	assert.Equal(t, "language", s.ID())
	assert.Equal(t, DaemonFile, s.TargetFile())
	assert.True(t, s.Available(nil, nil))
}

// --- tls ---

func TestTlsSetting_CurrentValue(t *testing.T) {
	s := &tlsSetting{}
	cfg := &namespace.Config{}
	assert.Equal(t, i18n.T("setup.value.disabled"), s.CurrentValue(cfg, nil))

	cfg.Proxy.TLS = namespace.TlsConfig{Enabled: true, LetsEncrypt: true}
	assert.Equal(t, "Let's Encrypt", s.CurrentValue(cfg, nil))

	cfg.Proxy.TLS = namespace.TlsConfig{Enabled: true, CertPath: "/cert.pem"}
	assert.Equal(t, i18n.T("setup.value.custom_cert"), s.CurrentValue(cfg, nil))

	cfg.Proxy.TLS = namespace.TlsConfig{Enabled: true}
	assert.Equal(t, i18n.T("setup.value.self_signed"), s.CurrentValue(cfg, nil))
}

func TestTlsSetting_Metadata(t *testing.T) {
	s := &tlsSetting{}
	assert.Equal(t, "tls", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
	assert.True(t, s.Available(nil, nil))
}

// --- auth ---

func TestAuthSetting_CurrentValue(t *testing.T) {
	s := &authSetting{}
	cfg := &namespace.Config{Authentication: namespace.AuthenticationProps{Type: namespace.AuthKeycloak}}
	assert.Equal(t, "KEYCLOAK", s.CurrentValue(cfg, nil))

	cfg.Authentication.Type = namespace.AuthBasic
	cfg.Authentication.Users = []string{"admin", "user1"}
	assert.Equal(t, "BASIC (2 users)", s.CurrentValue(cfg, nil))

	cfg.Authentication.Type = ""
	cfg.Authentication.Users = nil
	assert.Equal(t, "BASIC", s.CurrentValue(cfg, nil))
}

func TestAuthSetting_Metadata(t *testing.T) {
	s := &authSetting{}
	assert.Equal(t, "auth", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
	assert.True(t, s.Available(nil, nil))
}

// --- resources ---

func TestResourcesSetting_CurrentValue(t *testing.T) {
	s := &resourcesSetting{}
	cfg := &namespace.Config{}
	assert.Equal(t, i18n.T("setup.value.defaults"), s.CurrentValue(cfg, nil))

	cfg.Webapps = map[string]namespace.WebappProps{
		"emodel": {HeapSize: "2g", MemoryLimit: "4g"},
	}
	assert.Equal(t, i18n.T("setup.value.apps_customized", "count", "1"), s.CurrentValue(cfg, nil))

	cfg.Webapps["gateway"] = namespace.WebappProps{HeapSize: "1g"}
	assert.Equal(t, i18n.T("setup.value.apps_customized", "count", "2"), s.CurrentValue(cfg, nil))
}

func TestResourcesSetting_Available(t *testing.T) {
	s := &resourcesSetting{}
	assert.True(t, s.Available(nil, []string{"emodel", "gateway"}))
	assert.False(t, s.Available(nil, nil))
	assert.False(t, s.Available(nil, []string{}))
}

func TestResourcesSetting_Metadata(t *testing.T) {
	s := &resourcesSetting{}
	assert.Equal(t, "resources", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
}

// --- email ---

func TestEmailSetting_CurrentValue(t *testing.T) {
	s := &emailSetting{}
	cfg := &namespace.Config{}
	assert.Equal(t, i18n.T("setup.value.not_configured"), s.CurrentValue(cfg, nil))

	cfg.Email = &namespace.EmailConfig{Host: "smtp.example.com", Port: 587}
	assert.Equal(t, "smtp.example.com:587", s.CurrentValue(cfg, nil))
}

func TestEmailSetting_Available(t *testing.T) {
	s := &emailSetting{}
	assert.True(t, s.Available(nil, nil))
	assert.True(t, s.Available(nil, []string{"emodel"}))
}

func TestEmailSetting_Metadata(t *testing.T) {
	s := &emailSetting{}
	assert.Equal(t, "email", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
}

// --- s3 ---

func TestS3Setting_CurrentValue(t *testing.T) {
	s := &s3Setting{}
	cfg := &namespace.Config{}
	assert.Equal(t, i18n.T("setup.value.not_configured"), s.CurrentValue(cfg, nil))

	cfg.S3 = &namespace.S3Config{Endpoint: "https://s3.example.com", Bucket: "my-bucket"}
	assert.Equal(t, "s3.example.com / my-bucket", s.CurrentValue(cfg, nil))

	// Endpoint without scheme.
	cfg.S3 = &namespace.S3Config{Endpoint: "minio.local:9000", Bucket: "data"}
	assert.Equal(t, "minio.local:9000 / data", s.CurrentValue(cfg, nil))
}

func TestS3Setting_Available(t *testing.T) {
	s := &s3Setting{}
	assert.False(t, s.Available(nil, []string{"emodel", "gateway"}))
	assert.True(t, s.Available(nil, []string{"emodel", "content"}))
	assert.False(t, s.Available(nil, nil))
	assert.False(t, s.Available(nil, []string{}))
}

func TestS3Setting_Metadata(t *testing.T) {
	s := &s3Setting{}
	assert.Equal(t, "s3", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
}

// --- secret ref persistence ---

// TestApplyS3Setting_SecretRef ensures applyS3Setting never writes a plain
// secret value to cfg.S3.SecretKey — only the "secret:s3.secretKey" ref.
// Plain values go to PendingSecrets, which the orchestrator then persists
// via SecretService. The generator resolves the ref to the real value at
// container-start time (applyS3Config in generator.go).
func TestApplyS3Setting_SecretRef(t *testing.T) {
	sctx := &setupContext{PendingSecrets: map[string]string{}}
	cfg := &namespace.Config{}
	applyS3Setting(sctx, cfg, nil,
		"https://s3.example.com", "ecos-content", "AKIA...", "plain-secret-value", "us-east-1")

	require.NotNil(t, cfg.S3)
	assert.Equal(t, "secret:s3.secretKey", cfg.S3.SecretKey,
		"namespace.yml must reference the secret, not the plain value")
	assert.NotEqual(t, "plain-secret-value", cfg.S3.SecretKey,
		"plain secret must never leak into cfg.S3.SecretKey")
	assert.Equal(t, "plain-secret-value", sctx.PendingSecrets["s3.secretKey"],
		"plain value goes into PendingSecrets (written to SecretService)")
	assert.Equal(t, "https://s3.example.com", cfg.S3.Endpoint)
	assert.Equal(t, "ecos-content", cfg.S3.Bucket)
	assert.Equal(t, "us-east-1", cfg.S3.Region)

	// Round-trip through YAML: on-disk form must be the ref, not the plain value.
	data, err := namespace.MarshalNamespaceConfig(&namespace.Config{
		ID:             "test",
		Proxy:          namespace.ProxyProps{Port: 80},
		Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
		S3:             cfg.S3,
	})
	require.NoError(t, err)
	assert.Contains(t, string(data), "secretKey: secret:s3.secretKey")
	assert.NotContains(t, string(data), "plain-secret-value")
}

// TestApplyS3Setting_PreservesExistingRef ensures that when the user leaves
// the secret key field blank during an edit, the existing secret ref is
// preserved — we don't clobber it and we don't require re-entering the value.
func TestApplyS3Setting_PreservesExistingRef(t *testing.T) {
	sctx := &setupContext{PendingSecrets: map[string]string{}}
	cfg := &namespace.Config{}
	prev := &namespace.S3Config{SecretKey: "secret:s3.secretKey"}
	applyS3Setting(sctx, cfg, prev,
		"https://s3.example.com", "ecos-content", "AKIA...", "", "us-east-1")

	assert.Equal(t, "secret:s3.secretKey", cfg.S3.SecretKey)
	assert.Empty(t, sctx.PendingSecrets, "no new secret should be written when field is empty")
}

// TestApplyEmailSetting_SecretRef: same invariant for email password.
func TestApplyEmailSetting_SecretRef(t *testing.T) {
	sctx := &setupContext{PendingSecrets: map[string]string{}}
	cfg := &namespace.Config{}
	applyEmailSetting(sctx, cfg, nil,
		"smtp.example.com", 587, "user@example.com", "from@example.com", "plain-password", true)

	require.NotNil(t, cfg.Email)
	assert.Equal(t, "secret:email.password", cfg.Email.Password,
		"namespace.yml must reference the secret, not the plain value")
	assert.NotEqual(t, "plain-password", cfg.Email.Password,
		"plain password must never leak into cfg.Email.Password")
	assert.Equal(t, "plain-password", sctx.PendingSecrets["email.password"])
	assert.Equal(t, "smtp.example.com", cfg.Email.Host)
	assert.Equal(t, 587, cfg.Email.Port)
	assert.True(t, cfg.Email.TLS)

	// Round-trip through YAML: on-disk form must be the ref, not the plain value.
	data, err := namespace.MarshalNamespaceConfig(&namespace.Config{
		ID:             "test",
		Proxy:          namespace.ProxyProps{Port: 80},
		Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
		Email:          cfg.Email,
	})
	require.NoError(t, err)
	assert.Contains(t, string(data), "password: secret:email.password")
	assert.NotContains(t, string(data), "plain-password")
}

// TestApplyEmailSetting_PreservesExistingRef: same invariant on edit with
// blank password field.
func TestApplyEmailSetting_PreservesExistingRef(t *testing.T) {
	sctx := &setupContext{PendingSecrets: map[string]string{}}
	cfg := &namespace.Config{}
	prev := &namespace.EmailConfig{Password: "secret:email.password"}
	applyEmailSetting(sctx, cfg, prev,
		"smtp.example.com", 587, "user@example.com", "from@example.com", "", true)

	assert.Equal(t, "secret:email.password", cfg.Email.Password)
	assert.Empty(t, sctx.PendingSecrets, "no new secret should be written when field is empty")
}

// TestApplyEmailSetting_OptionalPassword: an SMTP relay without password
// should not leak the `password:` key into namespace.yml at all.
func TestApplyEmailSetting_OptionalPassword(t *testing.T) {
	sctx := &setupContext{PendingSecrets: map[string]string{}}
	cfg := &namespace.Config{}
	applyEmailSetting(sctx, cfg, nil,
		"relay.internal", 25, "", "noreply@company.com", "", false)

	assert.Empty(t, cfg.Email.Password)
	assert.Empty(t, sctx.PendingSecrets)
}

// --- allSettings registration ---

func TestAllSettings_RegistersAll(t *testing.T) {
	settings := allSettings()
	ids := make(map[string]bool)
	for _, s := range settings {
		ids[s.ID()] = true
	}
	expected := []string{"hostname", "tls", "port", "email", "s3", "auth", "resources", "language", "admin-password"}
	for _, id := range expected {
		assert.True(t, ids[id], "setting %q not registered in allSettings()", id)
	}
	assert.Len(t, settings, len(expected))
}

// --- shared validators ---

func TestNotEmpty(t *testing.T) {
	require.Error(t, notEmpty(""))
	require.Error(t, notEmpty("   "))
	assert.NoError(t, notEmpty("hello"))
}

func TestValidatePort(t *testing.T) {
	assert.NoError(t, validatePort("80"))
	assert.NoError(t, validatePort("443"))
	assert.NoError(t, validatePort("65535"))
	require.Error(t, validatePort("0"))
	require.Error(t, validatePort("65536"))
	require.Error(t, validatePort("abc"))
	require.Error(t, validatePort(""))
}
