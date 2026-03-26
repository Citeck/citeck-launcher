package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

func makeCtx(port int, host string, tlsEnabled bool) *NsGenContext {
	cfg := &NamespaceConfig{
		Proxy: ProxyProps{
			Port: port,
			Host: host,
			TLS:  TlsConfig{Enabled: tlsEnabled},
		},
	}
	return NewNsGenContext(cfg, &bundle.EmptyBundleDef)
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

func TestNextPort(t *testing.T) {
	ctx := makeCtx(80, "", false)
	p1 := ctx.NextPort()
	p2 := ctx.NextPort()
	if p2 <= p1 {
		t.Error("expected incrementing ports")
	}
}
