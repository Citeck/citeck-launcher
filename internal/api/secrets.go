package api

import "strings"

// MaskSecretEnv masks values of env vars whose keys end with _PASSWORD, _SECRET, _TOKEN, _KEY.
// Input format: "KEY=VALUE". Returns "KEY=***" for sensitive keys.
func MaskSecretEnv(envLine string) string {
	eqIdx := strings.Index(envLine, "=")
	if eqIdx < 0 {
		return envLine
	}
	key := strings.ToUpper(envLine[:eqIdx])
	for _, suffix := range []string{"_PASSWORD", "_SECRET", "_TOKEN", "_KEY"} {
		if strings.HasSuffix(key, suffix) {
			return envLine[:eqIdx+1] + "***"
		}
	}
	return envLine
}
