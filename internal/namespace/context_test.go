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

// Pins the "reverse proxy in front" assumption for non-local hosts: domain or
// IP, when local TLS is off, both render https:// because the most common
// production deployment terminates TLS in front of the launcher (CF tunnel,
// nginx, Caddy). This is wrong for raw-IP HTTP-only installs without a
// terminator — OIDC redirect_uri / KC hostname mismatch breaks login — but we
// keep the broader case working until a `proxy.publicScheme` override lands.
// See ProxyScheme() doc and the wizard TODO. If the assumption is ever
// re-narrowed, swap this test's expectation along with the production logic.
func TestProxyBaseURL_NonLocalHost_AssumesReverseProxyHTTPS(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"demo.example.com", "https://demo.example.com"},
		{"45.15.158.227", "https://45.15.158.227"},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			ctx := makeCtx(80, c.host, false)
			if url := ctx.ProxyBaseURL(); url != c.want {
				t.Errorf("expected %s, got %s", c.want, url)
			}
		})
	}
}

func TestProxyBaseURL_RawIP_HTTPS(t *testing.T) {
	// TLS-on (e.g. Let's Encrypt shortlived profile for IPs) keeps https.
	ctx := makeCtx(443, "45.15.158.227", true)
	if url := ctx.ProxyBaseURL(); url != "https://45.15.158.227" {
		t.Errorf("expected https://45.15.158.227, got %s", url)
	}
}

func TestProxyBaseURL_IPv6Loopback(t *testing.T) {
	ctx := makeCtx(80, "::1", false)
	if url := ctx.ProxyBaseURL(); url != "http://::1" {
		t.Errorf("expected http://::1, got %s", url)
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
