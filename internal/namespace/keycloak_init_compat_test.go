package namespace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appfiles"
)

// Keycloak 26.5+ runs validateClientAndRealmTimeouts on every realm UPDATE
// (never on an import): a client-session timeout above the SSO-session one —
// above the larger of it and its remember-me twin when remember-me is on — is
// refused, and with it every save of the realm settings. The realm this
// launcher imports on a fresh stand must pass that rule itself, or the first
// save an operator makes fails with "Client Session Idle Timeout cannot be
// greater than Realm SSO Idle Timeout" (measured on 26.7.4).
func TestShippedEcosAppRealmPassesKeycloakSessionValidation(t *testing.T) {
	files, err := appfiles.GetFiles()
	require.NoError(t, err)
	raw, ok := files["keycloak/ecos-app-realm.json"]
	require.True(t, ok, "the realm the generator mounts is missing from appfiles")

	var realm struct {
		RememberMe                      bool  `json:"rememberMe"`
		SSOSessionIdleTimeout           int64 `json:"ssoSessionIdleTimeout"`
		SSOSessionMaxLifespan           int64 `json:"ssoSessionMaxLifespan"`
		SSOSessionIdleTimeoutRememberMe int64 `json:"ssoSessionIdleTimeoutRememberMe"`
		SSOSessionMaxLifespanRememberMe int64 `json:"ssoSessionMaxLifespanRememberMe"`
		ClientSessionIdleTimeout        int64 `json:"clientSessionIdleTimeout"`
		ClientSessionMaxLifespan        int64 `json:"clientSessionMaxLifespan"`
	}
	require.NoError(t, json.Unmarshal(raw, &realm))

	idleLimit, maxLimit := realm.SSOSessionIdleTimeout, realm.SSOSessionMaxLifespan
	if realm.RememberMe {
		idleLimit = max(idleLimit, realm.SSOSessionIdleTimeoutRememberMe)
		maxLimit = max(maxLimit, realm.SSOSessionMaxLifespanRememberMe)
	}
	assert.LessOrEqual(t, realm.ClientSessionIdleTimeout, idleLimit,
		"client session idle exceeds the SSO idle: Keycloak 26.5+ refuses every save of this realm")
	assert.LessOrEqual(t, realm.ClientSessionMaxLifespan, maxLimit,
		"client session max exceeds the SSO max: Keycloak 26.5+ refuses every save of this realm")
}

// fakeKcadm stands in for /opt/keycloak/bin/kcadm.sh: it records every call and
// answers the ones the init script makes from the FAKE_* environment. A call it
// does not recognize fails AND lands in $FAKE_UNEXPECTED, which runInitScript
// requires to stay empty — the script swallows most kcadm failures into a WARN,
// so the exit code alone would let a new, wrong call pass unnoticed.
const fakeKcadm = `#!/bin/bash
echo "$*" >> "$FAKE_LOG"
case "$*" in
  "config credentials "*) exit 0 ;;
  "get users -r master -q username=citeck "*) echo "sa-id,citeck" ;;
  "get users "*) ;;
  "set-password "*) ;;
  "get realms/ecos-app "*)
    [ -n "$FAKE_REALM_FAIL" ] && exit 1
    printf '%s\n' "$FAKE_REALM" ;;
  "update realms/ecos-app "*) ;;
  "get clients -r ecos-app -q clientId=ecos-proxy-app "*) echo "cid-1,ecos-proxy-app" ;;
  "update clients/cid-1 "*) ;;
  "get clients/cid-1/protocol-mappers/models "*) printf '%s' "$FAKE_MAPPERS" ;;
  "create clients/cid-1/protocol-mappers/models "*) ;;
  "get clients/cid-1 -r ecos-app") printf '%s\n' "$FAKE_CLIENT" ;;
  *) echo "$*" >> "$FAKE_UNEXPECTED"; exit 3 ;;
esac
`

// realmJSON is what `kcadm get realms/ecos-app --fields ...` prints: Jackson's
// pretty printer, " : " separators, the remember-me keys first — so a match on
// "ssoSessionIdleTimeout" that is not anchored at its closing quote would read
// the remember-me value instead.
func realmJSON(rememberMe bool, ssoIdle, ssoMax, rmIdle, rmMax, clientIdle, clientMax int64) string {
	return fmt.Sprintf(`{
  "rememberMe" : %t,
  "ssoSessionIdleTimeoutRememberMe" : %d,
  "ssoSessionMaxLifespanRememberMe" : %d,
  "ssoSessionIdleTimeout" : %d,
  "ssoSessionMaxLifespan" : %d,
  "clientSessionIdleTimeout" : %d,
  "clientSessionMaxLifespan" : %d
}`, rememberMe, rmIdle, rmMax, ssoIdle, ssoMax, clientIdle, clientMax)
}

type kcadmWorld struct {
	realm     string
	realmFail bool
	mappers   string // `--fields name --format csv --noquotes`: one name per line
	client    string // the full client representation
}

// runInitScript renders the real init script, points it at the fake kcadm and
// runs it, returning the kcadm calls it made and its stderr.
func runInitScript(t *testing.T, w kcadmWorld) (calls []string, stderr string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	script, err := appfiles.RenderKeycloakInitScript(appfiles.KeycloakInitParams{
		SAUser: "citeck", LegacySAUser: "citeck-launcher", SAPassword: "sa-pass",
		AdminPassword: "strong-admin-pass", BaseURL: "http://localhost", OIDCSecret: "oidc",
		DBUrl: KeycloakDBJDBCURL(), DBUser: KeycloakDBName, DBPass: KeycloakDBName,
	})
	require.NoError(t, err)

	dir := t.TempDir()
	kcadm := filepath.Join(dir, "kcadm.sh")
	require.NoError(t, os.WriteFile(kcadm, []byte(fakeKcadm), 0o755)) //nolint:gosec // a test double that must be executable
	const realKcadm = "KCADM=/opt/keycloak/bin/kcadm.sh"
	require.Contains(t, script, realKcadm)
	script = strings.Replace(script, realKcadm, "KCADM="+kcadm, 1)
	scriptPath := filepath.Join(dir, "init.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))

	logPath := filepath.Join(dir, "calls.log")
	unexpectedPath := filepath.Join(dir, "unexpected.log")
	realmFail := ""
	if w.realmFail {
		realmFail = "1"
	}
	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(),
		"FAKE_LOG="+logPath, "FAKE_UNEXPECTED="+unexpectedPath, "FAKE_REALM_FAIL="+realmFail,
		"FAKE_REALM="+w.realm, "FAKE_MAPPERS="+w.mappers, "FAKE_CLIENT="+w.client)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	require.NoError(t, cmd.Run(), "the init script failed: %s", errBuf.String())

	unexpected, err := os.ReadFile(unexpectedPath)
	if !os.IsNotExist(err) {
		require.NoError(t, err)
		require.Empty(t, string(unexpected), "the init script made kcadm calls the fake does not know")
	}

	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(raw)), "\n"), errBuf.String()
}

func callsStartingWith(calls []string, prefix string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// A client already carrying the audience mapper and the compatibility switch,
// so the realm cases below see only the realm half of the script.
const settledClient = `{
  "clientId" : "ecos-proxy-app",
  "attributes" : {
    "allow.token.introspection.without.audience.check" : "true"
  }
}`

const settledMappers = "ecos-proxy-app-audience\n"

// The realm half mirrors Keycloak's own rule: only a timeout the rule refuses
// is reset, to 0 ("use the SSO value"), and a timeout within the limit — the
// remember-me one included — is the operator's and is left alone.
func TestKeycloakInitResetsOnlyTheSessionTimeoutsKeycloakRefuses(t *testing.T) {
	const day = 86400
	for name, tc := range map[string]struct {
		realm string
		want  []string
	}{
		"the realm this launcher shipped": {
			realm: realmJSON(false, 3600, 30*day, 0, 0, 30*day, 60*day),
			want:  []string{"update realms/ecos-app -s clientSessionIdleTimeout=0 -s clientSessionMaxLifespan=0"},
		},
		"a realm within the limit": {
			realm: realmJSON(false, 3600, 30*day, 0, 0, 0, 0),
		},
		"equal to the limit is within it": {
			realm: realmJSON(false, 3600, 30*day, 0, 0, 3600, 30*day),
		},
		"remember-me raises the limit": {
			realm: realmJSON(true, 3600, 30*day, 30*day, 0, day, 60*day),
			want:  []string{"update realms/ecos-app -s clientSessionMaxLifespan=0"},
		},
		"longer SSO timeouts admit longer client ones": {
			realm: realmJSON(false, 2*day, 90*day, 0, 0, day, 60*day),
		},
		"remember-me raises the max limit too": {
			realm: realmJSON(true, 3600, 30*day, 0, 90*day, 0, 60*day),
		},
		"remember-me values do not count while remember-me is off": {
			realm: realmJSON(false, 3600, 30*day, 30*day, 90*day, day, 60*day),
			want:  []string{"update realms/ecos-app -s clientSessionIdleTimeout=0 -s clientSessionMaxLifespan=0"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _ := runInitScript(t, kcadmWorld{realm: tc.realm, mappers: settledMappers, client: settledClient})
			assert.Equal(t, tc.want, callsStartingWith(calls, "update realms/ecos-app"))
		})
	}
}

// A realm the script cannot read is left exactly as it is, and the script goes
// on: the stand works either way, only saving the realm settings would not.
func TestKeycloakInitLeavesAnUnreadableRealmAlone(t *testing.T) {
	calls, stderr := runInitScript(t, kcadmWorld{realmFail: true, mappers: settledMappers, client: settledClient})
	assert.Empty(t, callsStartingWith(calls, "update realms/ecos-app"))
	assert.Contains(t, stderr, "could not read the ecos-app session timeouts")
	assert.NotEmpty(t, callsStartingWith(calls, "update clients/cid-1 -r ecos-app -s secret="),
		"the rest of the script still runs")
}

// Keycloak 26.6.2+ introspects a token only for a client in its audience, and
// the proxy introspects as ecos-proxy-app. The script gives ecos-proxy-app's
// own tokens that audience, and turns on the compatibility switch for other
// clients' tokens — each only while it is missing, so a restart changes
// nothing and an operator's explicit "false" survives.
func TestKeycloakInitKeepsTheProxyAbleToIntrospect(t *testing.T) {
	realm := realmJSON(false, 3600, 2592000, 0, 0, 0, 0)
	const createMapper = "create clients/cid-1/protocol-mappers/models -r ecos-app"
	const setSwitch = `update clients/cid-1 -r ecos-app -s attributes."allow.token.introspection.without.audience.check"=true`

	t.Run("a stand that has neither gets both", func(t *testing.T) {
		calls, _ := runInitScript(t, kcadmWorld{realm: realm, mappers: "", client: `{ "clientId" : "ecos-proxy-app", "attributes" : { } }`})
		created := callsStartingWith(calls, createMapper)
		require.Len(t, created, 1)
		for _, arg := range []string{
			"name=ecos-proxy-app-audience", "protocolMapper=oidc-audience-mapper",
			`config."included.client.audience"=ecos-proxy-app`, `config."access.token.claim"=true`,
			`config."introspection.token.claim"=true`, `config."id.token.claim"=false`,
		} {
			assert.Contains(t, created[0], arg)
		}
		assert.Equal(t, []string{setSwitch}, callsStartingWith(calls, setSwitch))
	})

	t.Run("a stand that has both is left alone", func(t *testing.T) {
		calls, _ := runInitScript(t, kcadmWorld{realm: realm, mappers: "some-other-mapper\n" + settledMappers, client: settledClient})
		assert.Empty(t, callsStartingWith(calls, createMapper))
		assert.Empty(t, callsStartingWith(calls, setSwitch))
	})

	t.Run("an operator's false is kept", func(t *testing.T) {
		client := strings.Replace(settledClient, `: "true"`, `: "false"`, 1)
		calls, _ := runInitScript(t, kcadmWorld{realm: realm, mappers: settledMappers, client: client})
		assert.Empty(t, callsStartingWith(calls, setSwitch))
	})

	// The presence check reads the FULL representation: kcadm's
	// `--fields attributes` answers with an empty map (measured on 26.4.5), so
	// a check through it would re-set the switch on every start and overwrite
	// an operator's false.
	t.Run("the switch is looked up in the full client representation", func(t *testing.T) {
		calls, _ := runInitScript(t, kcadmWorld{realm: realm, mappers: settledMappers, client: settledClient})
		assert.Contains(t, calls, "get clients/cid-1 -r ecos-app")
		assert.Empty(t, callsStartingWith(calls, "get clients/cid-1 -r ecos-app --fields attributes"))
	})

	t.Run("a mapper name that merely contains ours is not ours", func(t *testing.T) {
		calls, _ := runInitScript(t, kcadmWorld{realm: realm, mappers: "ecos-proxy-app-audience-old\n", client: settledClient})
		assert.Len(t, callsStartingWith(calls, createMapper), 1)
	})
}
