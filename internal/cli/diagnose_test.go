package cli

import (
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	dockercontainer "github.com/docker/docker/api/types/container"
)

func TestIsFailedAppStatus(t *testing.T) {
	failing := []string{"FAILED", "START_FAILED", "PULL_FAILED", "STOPPING_FAILED"}
	for _, s := range failing {
		if !isFailedAppStatus(s) {
			t.Errorf("expected %q to be classified as failed", s)
		}
	}
	healthy := []string{"RUNNING", "STARTING", "STOPPED", "READY_TO_PULL", ""}
	for _, s := range healthy {
		if isFailedAppStatus(s) {
			t.Errorf("did not expect %q to be classified as failed", s)
		}
	}
}

func TestHasProxyFailure(t *testing.T) {
	tests := []struct {
		name string
		apps []api.AppDto
		want bool
	}{
		{"empty", nil, false},
		{"proxy running", []api.AppDto{{Name: "proxy", Status: "RUNNING"}}, false},
		{"proxy failed", []api.AppDto{{Name: "proxy", Status: "START_FAILED"}}, true},
		{"other failed", []api.AppDto{{Name: "gateway", Status: "START_FAILED"}}, false},
		{"proxy pull failed", []api.AppDto{{Name: "proxy", Status: "PULL_FAILED"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasProxyFailure(tt.apps); got != tt.want {
				t.Errorf("hasProxyFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}

// B7-01: failed app must produce an ERROR-severity diagnose entry that
// references troubleshooting.rst.
func TestFailedAppCheck_SeverityAndHint(t *testing.T) {
	c := failedAppCheck(api.AppDto{Name: "proxy", Status: "START_FAILED"})
	if c.Status != "error" {
		t.Errorf("want status=error, got %q", c.Status)
	}
	if c.FixHint == "" {
		t.Error("expected FixHint to reference troubleshooting.rst")
	}
	if !strings.Contains(c.FixHint, "troubleshooting.rst") {
		t.Errorf("FixHint = %q, want it to mention troubleshooting.rst", c.FixHint)
	}
	if !strings.Contains(c.Message, "proxy") || !strings.Contains(c.Message, "START_FAILED") {
		t.Errorf("message should name app and status, got %q", c.Message)
	}
}

// B7-01/B7-02: port-443 conflict severity depends on proxy state; hint is
// always attached so --fix (and the plain report) can point at the docs.
func TestPortConflictCheck(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		proxyFailed bool
		wantStatus  string
		wantHint    bool
	}{
		{"port 443 + proxy failed", 443, true, "error", true},
		{"port 443 alone", 443, false, "warning", true},
		{"port 80", 80, false, "warning", true},
		{"other port", 8080, false, "warning", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := portConflictCheck(tt.port, "HTTPS", tt.proxyFailed, "")
			if c.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", c.Status, tt.wantStatus)
			}
			hasHintVal := c.FixHint != ""
			if hasHintVal != tt.wantHint {
				t.Errorf("hint presence = %v, want %v (hint=%q)", hasHintVal, tt.wantHint, c.FixHint)
			}
			if hasHintVal && !strings.Contains(c.FixHint, "troubleshooting.rst") {
				t.Errorf("hint should reference troubleshooting.rst, got %q", c.FixHint)
			}
		})
	}
}

func TestHasHint(t *testing.T) {
	if hasHint(nil) {
		t.Error("nil slice should not have hints")
	}
	if hasHint([]diagnoseCheck{{Status: "error"}}) {
		t.Error("empty hints should return false")
	}
	if !hasHint([]diagnoseCheck{{Status: "error"}, {Status: "warning", FixHint: "x"}}) {
		t.Error("expected true when at least one hint present")
	}
	if hasHint([]diagnoseCheck{{FixHint: "   "}}) {
		t.Error("whitespace-only hint should not count")
	}
}

// B7-02: troubleshootingRef is what diagnose prints for manual remediation.
// The constant is the contract with the docs — assert its shape so a silent
// rename doesn't break the user-facing pointer.
func TestTroubleshootingRefContainsDocsPath(t *testing.T) {
	if !strings.Contains(troubleshootingRef, "troubleshooting.rst") {
		t.Errorf("troubleshootingRef must reference troubleshooting.rst, got %q", troubleshootingRef)
	}
}

// #16: when the port is held by our own proxy container, diagnose must
// report OK (not WARN). ERROR severity (proxyFailed) is suppressed too —
// if we're the owner the port is exactly where it should be.
func TestPortConflictCheck_OwnedByLauncher(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		proxyFailed bool
		owner       string
	}{
		{"443 held by proxy", 443, false, "citeck_proxy_default"},
		{"443 held by proxy, proxy also 'failed'", 443, true, "citeck_proxy_default"},
		{"80 held by proxy", 80, false, "citeck_proxy_default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := portConflictCheck(tt.port, "HTTPS", tt.proxyFailed, tt.owner)
			if c.Status != "ok" {
				t.Errorf("status = %q, want ok (port held by our own container)", c.Status)
			}
			if c.FixHint != "" {
				t.Errorf("held-by-ours should have no FixHint, got %q", c.FixHint)
			}
			if !strings.Contains(c.Message, tt.owner) {
				t.Errorf("message should name the owning container, got %q", c.Message)
			}
		})
	}
}

// #16: matchPortOwners must pick the citeck container publishing each
// requested port and ignore containers that publish other ports only.
func TestMatchPortOwners(t *testing.T) {
	containers := []dockercontainer.Summary{
		{
			Names: []string{"/citeck_proxy_default"},
			Ports: []dockercontainer.Port{
				{PublicPort: 443, PrivatePort: 443, Type: "tcp"},
				{PublicPort: 80, PrivatePort: 80, Type: "tcp"},
			},
		},
		{
			Names: []string{"/citeck_rabbitmq_default"},
			Ports: []dockercontainer.Port{
				{PublicPort: 15672, PrivatePort: 15672, Type: "tcp"},
			},
		},
		{
			// Container exposes port internally but does not publish to host.
			Names: []string{"/citeck_gateway_default"},
			Ports: []dockercontainer.Port{
				{PublicPort: 0, PrivatePort: 8094, Type: "tcp"},
			},
		},
	}

	owners := matchPortOwners(containers, []int{80, 443, 8080})
	if owners[443] != "citeck_proxy_default" {
		t.Errorf("port 443 owner = %q, want citeck_proxy_default", owners[443])
	}
	if owners[80] != "citeck_proxy_default" {
		t.Errorf("port 80 owner = %q, want citeck_proxy_default", owners[80])
	}
	if _, has := owners[8080]; has {
		t.Errorf("port 8080 should have no owner, got %q", owners[8080])
	}
}

// #16: empty container list or Docker failure must yield no owners so the
// caller falls back to the foreign-conflict branch (safe default).
func TestMatchPortOwners_Empty(t *testing.T) {
	owners := matchPortOwners(nil, []int{443})
	if len(owners) != 0 {
		t.Errorf("empty container list should yield no owners, got %v", owners)
	}
}

// #16: unpublished ports (PublicPort=0) must not be reported as owners —
// otherwise a webapp exposing an internal SERVER_PORT would shadow the
// actual host binding.
func TestMatchPortOwners_IgnoresUnpublished(t *testing.T) {
	containers := []dockercontainer.Summary{
		{
			Names: []string{"/citeck_eapps_default"},
			Ports: []dockercontainer.Port{
				{PublicPort: 0, PrivatePort: 443, Type: "tcp"},
			},
		},
	}
	owners := matchPortOwners(containers, []int{443})
	if _, has := owners[443]; has {
		t.Errorf("unpublished port must not register an owner, got %q", owners[443])
	}
}

// #16: containerDisplayName must trim the leading slash Docker adds to
// container names — otherwise our report reads "/citeck_proxy_default".
func TestContainerDisplayName(t *testing.T) {
	tests := []struct {
		in   dockercontainer.Summary
		want string
	}{
		{dockercontainer.Summary{Names: []string{"/citeck_proxy_default"}}, "citeck_proxy_default"},
		{dockercontainer.Summary{Names: []string{}, ID: "abcdef1234567890"}, "abcdef123456"},
		{dockercontainer.Summary{}, "citeck container"},
	}
	for _, tt := range tests {
		got := containerDisplayName(tt.in)
		if got != tt.want {
			t.Errorf("containerDisplayName(%+v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
