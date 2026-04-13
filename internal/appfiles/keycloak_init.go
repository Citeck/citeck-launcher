package appfiles

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed embedded/keycloak/init.sh.tmpl
var keycloakInitFS embed.FS

// KeycloakInitParams holds the variables passed to the Keycloak init.sh
// template. All string fields are rendered via the bash-safe `shquote`
// template function, so they can contain arbitrary bytes (including $,
// backtick, backslash, newline, single quote, etc.) without risk of shell
// injection. ProxyPublic toggles the redirectUris update (non-localhost or
// TLS-enabled proxy). The ecos-app admin password reset block is emitted
// whenever AdminPassword differs from the default "admin".
type KeycloakInitParams struct {
	SAUser        string
	SAPassword    string
	// LegacySAUser is the pre-rename service-account username. When set and
	// different from SAUser, the init script attempts authentication with
	// this name (using SAPassword) for upgrade scenarios, and deletes the
	// legacy user from the master realm once the current SA is in place.
	// Safe to leave empty on fresh installs.
	LegacySAUser  string
	AdminPassword string
	BaseURL       string
	OIDCSecret    string
	ProxyPublic   bool
}

// shquote returns a bash single-quoted literal that safely represents s,
// suitable for interpolation into a POSIX-sh / bash script. The returned
// string is always wrapped in single quotes; any embedded single quote is
// rendered using the classic `'\''` idiom (close, escape, reopen). Because
// bash performs no expansion inside single quotes, the result is safe for
// all bytes — including $, backtick, backslash, newline, carriage return,
// and null — without the Go-specific escaping that fmt.Sprintf("%q", ...)
// would introduce (and which bash does NOT interpret inside "..." double
// quotes).
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var keycloakInitTemplate = template.Must(
	template.New("init.sh.tmpl").
		Option("missingkey=error").
		Funcs(template.FuncMap{
			// shquote renders a bash single-quoted literal. Safe for all
			// bytes inside bash (no expansion of $, backtick, etc.).
			"shquote": shquote,
		}).
		ParseFS(keycloakInitFS, "embedded/keycloak/init.sh.tmpl"),
)

// RenderKeycloakInitScript renders the Keycloak init.sh script from the
// embedded template using the supplied parameters. Returns an error on any
// template execution failure (including typos caught by missingkey=error).
func RenderKeycloakInitScript(p KeycloakInitParams) (string, error) {
	var buf bytes.Buffer
	if err := keycloakInitTemplate.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("render keycloak init script: %w", err)
	}
	return buf.String(), nil
}
