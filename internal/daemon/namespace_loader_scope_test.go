package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/docker"
)

// TestDockerClientScoped pins the invariant enforcer used by loadNamespace: a
// client is reused only when it is already scoped to exactly (workspace, ns);
// any mismatch (or nil) forces a rebuild, which is what makes the
// wrong-namespace client bug impossible regardless of the caller.
func TestDockerClientScoped(t *testing.T) {
	c, err := docker.NewClient("default", "ns1")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	if c.Namespace() != "ns1" || c.Workspace() != "default" {
		t.Fatalf("getters wrong: ws=%q ns=%q", c.Workspace(), c.Namespace())
	}

	cases := []struct {
		name      string
		dc        *docker.Client
		ws, ns    string
		wantMatch bool
	}{
		{"exact match → reuse", c, "default", "ns1", true},
		{"different namespace → rebuild", c, "default", "ns2", false},
		{"different workspace → rebuild", c, "other", "ns1", false},
		{"nil client → rebuild", nil, "default", "ns1", false},
	}
	for _, tc := range cases {
		if got := dockerClientScoped(tc.dc, tc.ws, tc.ns); got != tc.wantMatch {
			t.Errorf("%s: dockerClientScoped = %v, want %v", tc.name, got, tc.wantMatch)
		}
	}
}
