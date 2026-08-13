package namespace

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// nginx resolves its upstreams once, at startup. Recreate the gateway and the
// proxy keeps sending traffic to a container IP that no longer exists — every
// gateway-backed page 502s until something else recreates the proxy.
//
// doStart has always known this. doRegenerate did not, and doRegenerate is the
// path taken by `citeck reload`, by the gear-icon config editor and by
// `citeck edit <app>` — i.e. by every routine config change. Measured on the
// local stack (2026-08-13): a `citeck edit gateway` reload recreated the gateway
// at 03:01:18Z and again at 03:04:43Z while the proxy sat untouched from
// 00:02:55Z, so the stack ran with a stale upstream IP for ~33 minutes and the
// only reason nobody noticed is that nobody requested a gateway-backed page.
//
// The failure is invisible from inside the launcher: both containers are
// RUNNING, both probes pass, the namespace is green. It surfaces as a mass
// "product regression" in whatever walks the UI.
func TestDoRegenerate_ProxyFollowsGatewayRecreate(t *testing.T) {
	md := newMockDocker()
	tmpDir := t.TempDir()

	gateway := simpleApp(appdef.AppGateway, "gateway:1")
	proxy := simpleApp(appdef.AppProxy, "proxy:1")
	apps := []appdef.ApplicationDef{gateway, proxy}

	r := NewRuntime(testConfig(), md, tmpDir)
	defer r.Shutdown()
	r.Start(apps, false)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING, got %v", r.Status())
	}

	md.mu.Lock()
	gatewayBefore := md.containers[appdef.AppGateway].id
	proxyBefore := md.containers[appdef.AppProxy].id
	md.mu.Unlock()
	if gatewayBefore == "" || proxyBefore == "" {
		t.Fatalf("expected both containers present before regenerate")
	}

	// Edit ONLY the gateway — exactly what `citeck edit gateway` produces. The
	// proxy's own def is byte-identical, so by hash alone it would be reused.
	gatewayEdited := gateway
	gatewayEdited.Environments = appdef.OrderedMap{{Key: "JAVA_OPTS", Value: "-Xmx256m"}}
	r.Regenerate([]appdef.ApplicationDef{gatewayEdited, proxy}, nil, nil, false)

	waitForStatus(r, NsStatusStarting, 5*time.Second)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING after regenerate, got %v", r.Status())
	}
	if !waitForAppStatus(r, appdef.AppGateway, AppStatusRunning, 15*time.Second) {
		t.Fatalf("gateway did not return to RUNNING after regenerate")
	}
	if !waitForAppStatus(r, appdef.AppProxy, AppStatusRunning, 15*time.Second) {
		t.Fatalf("proxy did not return to RUNNING after regenerate")
	}

	md.mu.Lock()
	gatewayAfter := md.containers[appdef.AppGateway].id
	proxyAfter := md.containers[appdef.AppProxy].id
	md.mu.Unlock()

	if gatewayAfter == gatewayBefore {
		t.Fatalf("gateway (edited def) should have been recreated, container id unchanged: %s", gatewayAfter)
	}
	if proxyAfter == proxyBefore {
		t.Fatalf("proxy must follow the gateway recreate (nginx caches the upstream IP at startup), "+
			"but its container id is unchanged: %s", proxyAfter)
	}
}

// The rule is narrow on purpose: a reload that leaves the gateway alone must
// stay surgical. Recreating the proxy on every reload would drop the basic-auth
// htpasswd and every writable-layer change for nothing.
func TestDoRegenerate_ProxyIsLeftAloneWhenTheGatewayIsUnchanged(t *testing.T) {
	md := newMockDocker()
	tmpDir := t.TempDir()

	gateway := simpleApp(appdef.AppGateway, "gateway:1")
	proxy := simpleApp(appdef.AppProxy, "proxy:1")
	other := simpleApp("app-a", "image-a:1")

	r := NewRuntime(testConfig(), md, tmpDir)
	defer r.Shutdown()
	r.Start([]appdef.ApplicationDef{gateway, proxy, other}, false)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING, got %v", r.Status())
	}

	md.mu.Lock()
	proxyBefore := md.containers[appdef.AppProxy].id
	gatewayBefore := md.containers[appdef.AppGateway].id
	md.mu.Unlock()

	otherEdited := other
	otherEdited.Environments = appdef.OrderedMap{{Key: "FOO", Value: "bar"}}
	r.Regenerate([]appdef.ApplicationDef{gateway, proxy, otherEdited}, nil, nil, false)

	waitForStatus(r, NsStatusStarting, 5*time.Second)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING after regenerate, got %v", r.Status())
	}
	if !waitForAppStatus(r, "app-a", AppStatusRunning, 15*time.Second) {
		t.Fatalf("app-a did not return to RUNNING after regenerate")
	}

	md.mu.Lock()
	proxyAfter := md.containers[appdef.AppProxy].id
	gatewayAfter := md.containers[appdef.AppGateway].id
	md.mu.Unlock()

	if gatewayAfter != gatewayBefore {
		t.Fatalf("gateway was not edited and must be reused, id changed %s -> %s", gatewayBefore, gatewayAfter)
	}
	if proxyAfter != proxyBefore {
		t.Fatalf("proxy must not be recreated when the gateway is untouched, id changed %s -> %s", proxyBefore, proxyAfter)
	}
}
