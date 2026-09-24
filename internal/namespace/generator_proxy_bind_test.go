package namespace

import (
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A port spec without an address is published on 127.0.0.1, so the proxy —
// the app that exists to be reached from other machines — must name its
// address. Server mode: every interface, always. Desktop: every interface only
// for a namespace addressed by a non-local host (opened from other devices);
// on localhost it keeps the loopback default like everything else there.
func TestProxyNamesItsAddressWhereItMustBeReachedFromOutside(t *testing.T) {
	wasDesktop := config.IsDesktopMode()
	t.Cleanup(func() { config.SetDesktopMode(wasDesktop) })

	gen := func(desktop bool, host string) []string {
		config.SetDesktopMode(desktop)
		cfg := basicCfg()
		cfg.Proxy.Host = host
		bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppGateway: {Image: "nexus.citeck.ru/ecos-gateway:1"}}}
		resp, err := Generate(cfg, bun, &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppGateway}}},
			SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		proxy := findGeneratedApp(resp, appdef.AppProxy)
		require.NotNil(t, proxy)
		return proxy.Ports
	}

	for _, host := range []string{"", "localhost", "citeck.example.com", "10.0.0.5"} {
		ports := gen(false, host)
		require.Len(t, ports, 1, "server %q", host)
		assert.Regexp(t, `^\*:\d+:\d+$`, ports[0], "server mode, host %q", host)
	}
	for _, host := range []string{"", "localhost", "127.0.0.1"} {
		ports := gen(true, host)
		require.Len(t, ports, 1)
		assert.Regexp(t, `^\d+:\d+$`, ports[0], "desktop on %q keeps the loopback default", host)
	}
	for _, host := range []string{"citeck.lan", "192.168.1.20"} {
		ports := gen(true, host)
		require.Len(t, ports, 1)
		assert.Regexp(t, `^\*:\d+:\d+$`, ports[0], "desktop on %q is opened from other devices", host)
	}
}

// Only the proxy is ever reached from other machines: on a desktop opened from
// other devices the infrastructure web UIs (RabbitMQ, Mailpit, PgAdmin) stay on
// the loopback default like everything else — their links name localhost.
func TestOnlyTheProxyGoesPublicOnANonLocalHost(t *testing.T) {
	wasDesktop := config.IsDesktopMode()
	t.Cleanup(func() { config.SetDesktopMode(wasDesktop) })
	config.SetDesktopMode(true)

	cfg := basicCfg()
	cfg.Proxy.Host = "192.168.1.20"
	resp, err := Generate(cfg, &bundle.Def{}, &bundle.WorkspaceConfig{}, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	checked := 0
	for _, name := range []string{appdef.AppRabbitmq, appdef.AppMailpit, appdef.AppPgadmin, appdef.AppPostgres} {
		app := findGeneratedApp(resp, name)
		require.NotNil(t, app, name)
		require.NotEmpty(t, app.Ports, name)
		for _, p := range app.Ports {
			assert.NotContains(t, p, "*", "%s stays on loopback", name)
			checked++
		}
	}
	assert.Positive(t, checked)
}

// What the editor shows is what runs: the proxy's public address comes from
// the GENERATOR ("*:…", visible in `citeck edit proxy`), and a patch is
// taken exactly as written — an address-less port an operator saves is
// loopback like any other, with no rewriting behind the editor's back.
func TestAProxyPortsPatchIsTakenAsWritten(t *testing.T) {
	wasDesktop := config.IsDesktopMode()
	t.Cleanup(func() { config.SetDesktopMode(wasDesktop) })
	config.SetDesktopMode(false)

	gen := func(patch string) []string {
		bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppGateway: {Image: "nexus.citeck.ru/ecos-gateway:1"}}}
		opts := GenerateOpts{}
		if patch != "" {
			opts.EditedAppPatches = map[string]json.RawMessage{appdef.AppProxy: json.RawMessage(patch)}
		}
		resp, err := Generate(basicCfg(), bun, &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppGateway}}},
			SystemSecrets{JWT: "j", OIDC: "o"}, opts)
		require.NoError(t, err)
		proxy := findGeneratedApp(resp, appdef.AppProxy)
		require.NotNil(t, proxy)
		return proxy.Ports
	}

	unpatched := gen("")
	require.Len(t, unpatched, 1)
	assert.Regexp(t, `^\*:`, unpatched[0], "the generator names the public address, so the editor shows it")
	written := gen(`{"ports":["8443:443"]}`)
	assert.Equal(t, []string{"8443:443"}, written, "a saved port is taken as written")
}
