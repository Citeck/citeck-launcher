package api

import "strings"

// maskedEnvSubstrings: an env key CONTAINING any of these (case-insensitive)
// is masked. "PASS" deliberately covers both PASSWORD and the _PASS family
// (RABBITMQ_DEFAULT_PASS carries the shared admin password). Substring — not
// suffix — matching, so keys like KC_BOOTSTRAP_ADMIN_PASSWORD or
// ECOS_SECRET_..._CREDENTIALS_USERNAME mask too. Over-masking a non-secret
// is acceptable; under-masking is not.
var maskedEnvSubstrings = []string{"PASS", "PWD", "SECRET", "TOKEN", "CREDENTIALS"}

// maskedEnvExact: env keys masked by exact name. BASIC_AUTH_ACCESS carries
// the proxy's "user:password" pairs and matches no generic secret pattern.
var maskedEnvExact = map[string]bool{"BASIC_AUTH_ACCESS": true}

// MaskSecretEnv masks values of sensitive env vars. A key is sensitive when
// it contains PASS / PWD / SECRET / TOKEN / CREDENTIALS, ends with _KEY
// (suffix only — substring "KEY" would over-mask KEYCLOAK_*-style keys), or
// is an exact known-secret name (BASIC_AUTH_ACCESS).
// Input format: "KEY=VALUE". Returns "KEY=***" for sensitive keys.
func MaskSecretEnv(envLine string) string {
	eqIdx := strings.Index(envLine, "=")
	if eqIdx < 0 {
		return envLine
	}
	key := strings.ToUpper(envLine[:eqIdx])
	if isSecretEnvKey(key) {
		return envLine[:eqIdx+1] + "***"
	}
	return envLine
}

// isSecretEnvKey reports whether an (upper-cased) env key is sensitive.
func isSecretEnvKey(key string) bool {
	if maskedEnvExact[key] {
		return true
	}
	if strings.HasSuffix(key, "_KEY") {
		return true
	}
	for _, sub := range maskedEnvSubstrings {
		if strings.Contains(key, sub) {
			return true
		}
	}
	return false
}
