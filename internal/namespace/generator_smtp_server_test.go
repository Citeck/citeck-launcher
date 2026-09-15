package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the full server generator, including the built-in mailpit fallback.
func TestGenerateServerEmail(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(config.ResetDesktopMode)
	for _, tc := range []struct {
		name  string
		email *EmailConfig
		auth  string
	}{
		{name: "built-in mail"},
		{name: "unauthenticated relay", email: &EmailConfig{Host: "relay.example.com", Port: 25, From: "noreply@example.com"}, auth: "false"},
		{name: "authenticated SMTP", email: &EmailConfig{Host: "smtp.example.com", Port: 587, Username: "user", Password: "password", TLS: true, From: "noreply@example.com"}, auth: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
				Proxy:          ProxyProps{Port: 80},
				Email:          tc.email,
			}
			bun := &bundle.Def{Applications: map[string]bundle.AppDef{
				appdef.AppNotifications: {Image: "notifications:test"},
				appdef.AppGateway:       {Image: "gateway:test"},
			}}
			ws := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppNotifications}, {ID: appdef.AppGateway}}}
			resp, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
			require.NoError(t, err)
			app := findGeneratedApp(resp, appdef.AppNotifications)
			require.NotNil(t, app)
			assert.Equal(t, tc.auth, envGet(app.Environments, "SPRING_MAIL_PROPERTIES_MAIL_SMTP_AUTH"))
			if tc.email == nil {
				assert.NotNil(t, findGeneratedApp(resp, appdef.AppMailpit))
				assert.False(t, app.Environments.Has("SPRING_MAIL_PROPERTIES_MAIL_SMTP_AUTH"))
				return
			}
			assert.Nil(t, findGeneratedApp(resp, appdef.AppMailpit))
			assert.Equal(t, tc.email.Host, envGet(app.Environments, "SPRING_MAIL_HOST"))
			assert.Equal(t, tc.email.From, envGet(app.Environments, "ECOS_NOTIFICATIONS_EMAIL_FROM_DEFAULT"))
			assert.Equal(t, tc.email.Username, envGet(app.Environments, "SPRING_MAIL_USERNAME"))
			assert.Equal(t, tc.email.Password, envGet(app.Environments, "SPRING_MAIL_PASSWORD"))
			assert.Equal(t, tc.email.Username != "", app.Environments.Has("SPRING_MAIL_USERNAME"))
			assert.Equal(t, tc.email.Password != "", app.Environments.Has("SPRING_MAIL_PASSWORD"))
		})
	}
}
