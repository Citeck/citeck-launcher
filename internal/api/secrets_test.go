package api

import "testing"

// TestMaskSecretEnv pins the substring-based masking contract. The historical
// suffix-only matching (_PASSWORD/_SECRET/_TOKEN/_KEY) let RABBITMQ_DEFAULT_PASS
// (the shared admin password) and BASIC_AUTH_ACCESS (proxy user:pass pairs)
// leak into GET /apps/{name}/inspect and `citeck describe` — these cases are
// the load-bearing regressions here. Over-masking is acceptable, under-masking
// is not.
func TestMaskSecretEnv(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Legacy suffix matches must keep masking.
		{"DB_PASSWORD=hunter2", "DB_PASSWORD=***"},
		{"JWT_SECRET=abc123", "JWT_SECRET=***"},
		{"API_TOKEN=xyz", "API_TOKEN=***"},
		{"TLS_KEY=private", "TLS_KEY=***"},
		{"db_password=lower", "db_password=***"}, // case-insensitive
		{"EMPTY_PASSWORD=", "EMPTY_PASSWORD=***"},

		// Regression: substring matches the suffix rule used to miss.
		{"RABBITMQ_DEFAULT_PASS=admin-pw", "RABBITMQ_DEFAULT_PASS=***"},
		{"BASIC_AUTH_ACCESS=admin:pw,user:pw2", "BASIC_AUTH_ACCESS=***"},
		{"PASSWORD_FILE=/run/secrets/pw", "PASSWORD_FILE=***"},
		{"SECRET_KEY_BASE=abc", "SECRET_KEY_BASE=***"},
		{"TOKEN_TTL=300", "TOKEN_TTL=***"}, // over-masked, acceptable
		{"PGPWD=x", "PGPWD=***"},
		{"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME=ak", "ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME=***"},

		// Non-secrets stay visible.
		{"NORMAL_VAR=visible", "NORMAL_VAR=visible"},
		{"MONKEY=banana", "MONKEY=banana"}, // KEY suffix only as _KEY
		{"KEYCLOAK_URL=http://kc:8080", "KEYCLOAK_URL=http://kc:8080"}, // KEY substring must NOT mask
		{"CLASSPATH=/opt/app", "CLASSPATH=/opt/app"},
		{"PATH=/usr/bin", "PATH=/usr/bin"},
		{"no-equals", "no-equals"},
	}
	for _, tt := range tests {
		if got := MaskSecretEnv(tt.input); got != tt.want {
			t.Errorf("MaskSecretEnv(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
