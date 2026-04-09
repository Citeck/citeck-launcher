package setup

import (
	"os"
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

// --- allSettings registration ---

func TestAllSettings_RegistersAll(t *testing.T) {
	settings := allSettings()
	ids := make(map[string]bool)
	for _, s := range settings {
		ids[s.ID()] = true
	}
	expected := []string{"hostname", "tls", "port", "email", "s3", "auth", "resources", "language"}
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
