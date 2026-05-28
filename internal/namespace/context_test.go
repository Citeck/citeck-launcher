package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

func makeCtx(port int, host string, tlsEnabled bool) *NsGenContext {
	cfg := &Config{
		Proxy: ProxyProps{
			Port: port,
			Host: host,
			TLS:  TlsConfig{Enabled: tlsEnabled},
		},
	}
	return NewNsGenContext(cfg, &bundle.EmptyDef)
}

func TestProxyBaseURL_HTTP_80(t *testing.T) {
	ctx := makeCtx(80, "localhost", false)
	if url := ctx.ProxyBaseURL(); url != "http://localhost" {
		t.Errorf("expected http://localhost, got %s", url)
	}
}

func TestProxyBaseURL_HTTPS_443(t *testing.T) {
	ctx := makeCtx(443, "prod.example.com", true)
	if url := ctx.ProxyBaseURL(); url != "https://prod.example.com" {
		t.Errorf("expected https://prod.example.com, got %s", url)
	}
}

func TestProxyBaseURL_HTTP_8080(t *testing.T) {
	ctx := makeCtx(8080, "localhost", false)
	if url := ctx.ProxyBaseURL(); url != "http://localhost:8080" {
		t.Errorf("expected http://localhost:8080, got %s", url)
	}
}

func TestProxyBaseURL_HTTPS_8443(t *testing.T) {
	ctx := makeCtx(8443, "custom.launcher.ru", true)
	if url := ctx.ProxyBaseURL(); url != "https://custom.launcher.ru:8443" {
		t.Errorf("expected https://custom.launcher.ru:8443, got %s", url)
	}
}

func TestProxyBaseURL_HTTP_443(t *testing.T) {
	// Non-standard: HTTP on 443
	ctx := makeCtx(443, "localhost", false)
	if url := ctx.ProxyBaseURL(); url != "http://localhost:443" {
		t.Errorf("expected http://localhost:443, got %s", url)
	}
}

func TestProxyBaseURL_HTTPS_80(t *testing.T) {
	// Non-standard: HTTPS on 80
	ctx := makeCtx(80, "localhost", true)
	if url := ctx.ProxyBaseURL(); url != "https://localhost:80" {
		t.Errorf("expected https://localhost:80, got %s", url)
	}
}

func TestProxyBaseURL_BlankHost(t *testing.T) {
	ctx := makeCtx(80, "", false)
	if url := ctx.ProxyBaseURL(); url != "http://localhost" {
		t.Errorf("expected http://localhost, got %s", url)
	}
}

// IP hosts are NOT assumed to sit behind a reverse-proxy that terminates TLS
// (no Cloudflare / nginx CDN serves a raw IP). When the wizard saves
// http://<ip>:80, the printed and webapp-injected URL must reflect that
// directly — a stale https:// scheme would break browser access and webapp
// callbacks. Pinned because the previous behavior swapped to https for any
// non-localhost host.
func TestProxyBaseURL_RawIP_HTTP(t *testing.T) {
	ctx := makeCtx(80, "45.15.158.227", false)
	if url := ctx.ProxyBaseURL(); url != "http://45.15.158.227" {
		t.Errorf("expected http://45.15.158.227, got %s", url)
	}
}

func TestProxyBaseURL_RawIP_HTTP_CustomPort(t *testing.T) {
	ctx := makeCtx(8080, "45.15.158.227", false)
	if url := ctx.ProxyBaseURL(); url != "http://45.15.158.227:8080" {
		t.Errorf("expected http://45.15.158.227:8080, got %s", url)
	}
}

func TestProxyBaseURL_RawIP_HTTPS(t *testing.T) {
	ctx := makeCtx(443, "45.15.158.227", true)
	if url := ctx.ProxyBaseURL(); url != "https://45.15.158.227" {
		t.Errorf("expected https://45.15.158.227, got %s", url)
	}
}

// Non-local *domain* hosts keep the reverse-proxy assumption: deploy without
// local TLS but show https:// because a CDN/nginx terminates TLS upstream.
func TestProxyBaseURL_Domain_HTTPLocal_RendersHTTPS(t *testing.T) {
	ctx := makeCtx(80, "demo.example.com", false)
	if url := ctx.ProxyBaseURL(); url != "https://demo.example.com" {
		t.Errorf("expected https://demo.example.com, got %s", url)
	}
}

func TestProxyBaseURL_IPv6Loopback(t *testing.T) {
	ctx := makeCtx(80, "::1", false)
	if url := ctx.ProxyBaseURL(); url != "http://::1" {
		t.Errorf("expected http://::1, got %s", url)
	}
}

// ProxyScheme drives the lua_oidc redirect_uri_scheme. A raw IP HTTP-only
// install must emit "http" so Keycloak (whose redirectUris are registered as
// http://<ip>*) accepts the redirect_uri; otherwise the OIDC dance dies at
// "Invalid parameter: redirect_uri".
func TestProxyScheme_RawIP_HTTP(t *testing.T) {
	ctx := makeCtx(80, "45.15.158.227", false)
	if s := ctx.ProxyScheme(); s != "http" {
		t.Errorf("expected http, got %s", s)
	}
}

func TestProxyScheme_Domain_HTTPLocal_RendersHTTPS(t *testing.T) {
	ctx := makeCtx(80, "demo.example.com", false)
	if s := ctx.ProxyScheme(); s != "https" {
		t.Errorf("expected https (reverse-proxy assumption), got %s", s)
	}
}

func TestProxyScheme_TLSAlwaysHTTPS(t *testing.T) {
	ctx := makeCtx(443, "45.15.158.227", true)
	if s := ctx.ProxyScheme(); s != "https" {
		t.Errorf("expected https, got %s", s)
	}
}

func TestPresumedHTTPS(t *testing.T) {
	cases := []struct {
		host       string
		tlsEnabled bool
		want       bool
	}{
		{"localhost", false, false},
		{"localhost", true, true},
		{"45.15.158.227", false, false},
		{"45.15.158.227", true, true},
		{"::1", false, false},
		{"demo.example.com", false, true},
		{"demo.example.com", true, true},
		{"", false, false},
	}
	for _, c := range cases {
		ctx := makeCtx(80, c.host, c.tlsEnabled)
		if got := ctx.PresumedHTTPS(); got != c.want {
			t.Errorf("PresumedHTTPS(host=%q, tls=%v) = %v, want %v", c.host, c.tlsEnabled, got, c.want)
		}
	}
}

func TestNextPort(t *testing.T) {
	ctx := makeCtx(80, "", false)
	p1 := ctx.NextPort()
	p2 := ctx.NextPort()
	if p2 <= p1 {
		t.Error("expected incrementing ports")
	}
}
