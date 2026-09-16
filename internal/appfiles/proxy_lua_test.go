package appfiles

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const proxyLuaKey = "proxy/lua_oidc_full_access.lua"

// The guards below are about what the handler DOES, so they read the code with
// the prose taken out — the comments explaining the bypass necessarily quote the
// very markers the guards forbid.
func proxyLuaCode(t *testing.T) string {
	t.Helper()
	files, err := GetFiles()
	require.NoError(t, err)
	src, ok := files[proxyLuaKey]
	require.True(t, ok, "%s must be embedded", proxyLuaKey)
	return stripLuaComments(string(src))
}

// stripLuaComments drops `--` to end of line, leaving string literals alone.
// The handler has no long comments (`--[[ ]]`) and no long strings.
func stripLuaComments(src string) string {
	var out strings.Builder
	for line := range strings.SplitSeq(src, "\n") {
		var quote byte
		cut := -1
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case quote != 0:
				switch c {
				case '\\':
					i++
				case quote:
					quote = 0
				}
			case c == '"' || c == '\'':
				quote = c
			case c == '-' && i+1 < len(line) && line[i+1] == '-':
				cut = i
			}
			if cut >= 0 {
				break
			}
		}
		if cut >= 0 {
			line = line[:cut]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// The handler decides which identity ends up in X-ECOS-User /
// X-Alfresco-Remote-User, headers ecos-gateway trusts unconditionally, so a
// service exception that matches more than the path nginx actually routed IS an
// authentication bypass. The reported one was `?probe=/healthcheck/` on a
// protected gateway endpoint, which handed out `service_healthcheck`; the same
// marker also smuggles through a middle path segment and through `..%2f`
// traversal, because nginx normalizes the encoded traversal and routes the
// request to an arbitrary gateway endpoint while the handler still sees the
// marker in the raw $request_uri.
//
// These are source-level guards so they run in `make check` with no Docker. The
// behavioral suite is tests/proxy-lua/run.sh.
func TestProxyLuaMatchesTheRoutedPathAndNothingElse(t *testing.T) {
	code := proxyLuaCode(t)

	require.Contains(t, code, "local function getRequestPath()",
		"the handler must derive its own normalized path")
	require.Contains(t, code, "local reqPath = getRequestPath()",
		"the access decisions must run off the normalized path")

	// $request_uri is raw: query string included, percent-escapes intact. It
	// may be read exactly once — inside getRequestPath, which is what
	// normalizes it. Anywhere else it is the bypass.
	if n := strings.Count(code, "ngx.var.request_uri"); n != 1 {
		t.Errorf("ngx.var.request_uri is read %d times; only getRequestPath may read it, "+
			"every access check must match the normalized reqPath instead", n)
	}

	// Every string.find over the request path must be anchored: an unanchored
	// marker matches mid-path, which is one half of the bypass.
	findCall := regexp.MustCompile(`string\.find\(\s*([A-Za-z_.]+)\s*,\s*"([^"]*)"`)
	matches := findCall.FindAllStringSubmatch(code, -1)
	require.NotEmpty(t, matches, "expected the service-exception checks to be present")
	sawPathCheck := false
	for _, m := range matches {
		subject, pattern := m[1], m[2]
		if subject != "reqPath" {
			// string.find over an error message or a header is not a path check.
			continue
		}
		sawPathCheck = true
		if !strings.HasPrefix(pattern, "^") {
			t.Errorf("service marker %q is matched unanchored against the request path: "+
				"it also matches mid-path (/gateway/emodel%s...)", pattern, pattern)
		}
	}
	require.True(t, sawPathCheck, "expected at least one reqPath marker check")

	// isStaticResUri grants "guest" too, so it gets the normalized path as
	// well — matching the raw URI let a query string fake the extension
	// (/camunda/app/admin/users?probe=.css).
	require.Contains(t, code, "isStaticResUri(reqPath:lower())")
}

// The metrics/healthcheck exceptions are gone rather than anchored. Those
// locations carry their own authentication and do not run this handler (the
// proxy config has had no server-level access_by_lua_file since config v4), so
// the rules granted nothing and only widened the set of markers that could be
// smuggled into a protected request. "/healthcheck/" was the worst of them: it
// granted `service_healthcheck`, which ecos-gateway auto-provisions as a real
// user with GROUP_EVERYONE + ROLE_USER.
func TestProxyLuaHandsOutNoInfrastructureIdentity(t *testing.T) {
	// Checked against the RAW file, comments included: after a security
	// advisory the way an operator verifies a host got the fix is to grep the
	// deployed /etc/nginx/includes/lua_oidc_full_access.lua for one of these
	// names, and a comment that merely explains their removal answers that
	// grep with a false positive. Name them in AGENTS.md and tests/proxy-lua/
	// instead.
	files, err := GetFiles()
	require.NoError(t, err)
	raw, ok := files[proxyLuaKey]
	require.True(t, ok, "%s must be embedded", proxyLuaKey)
	// Lua patterns escape a literal "-" as "%-", so the same marker has two
	// spellings: the one an operator greps for and the one a re-added rule
	// would be written in. Fold them together and catch both.
	src := strings.ReplaceAll(string(raw), "%-", "-")

	if strings.Contains(src, "service_healthcheck") {
		t.Error("the handler must not name, or mint, the healthcheck identity")
	}
	for _, marker := range []string{"/healthcheck/", "/rabbitmq", "/node-exporter", "/postgres-exporter", "/cadvisor/"} {
		if strings.Contains(src, marker) {
			t.Errorf("%s is authenticated by its own location, not by this handler", marker)
		}
	}
}

// The real access decisions, run by LuaJIT against a stubbed ngx (see
// tests/proxy-lua/). Opt-in: it needs Docker and the openresty image, which is
// too much to put on every `go test` run.
//
//	CITECK_PROXY_LUA_E2E=1 go test ./internal/appfiles/ -run ProxyLuaSpec
func TestProxyLuaSpec(t *testing.T) {
	if os.Getenv("CITECK_PROXY_LUA_E2E") != "1" {
		t.Skip("set CITECK_PROXY_LUA_E2E=1 to run the LuaJIT spec (needs Docker)")
	}
	out, err := exec.Command("../../tests/proxy-lua/run.sh").CombinedOutput() //nolint:gosec // fixed path, no user input
	require.NoError(t, err, "spec failed:\n%s", out)
	t.Log("\n" + string(out))
}
