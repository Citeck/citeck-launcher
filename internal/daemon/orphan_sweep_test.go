package daemon

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// fakeKeepLister is the two-method slice of storage.Store the keep set is
// built from. Errors are per call so a test can fail exactly one listing.
type fakeKeepLister struct {
	wss    []storage.WorkspaceDto
	nss    map[string][]storage.NamespaceRow
	wsErr  error
	nsErr  error
	nsCall int
}

func (f *fakeKeepLister) ListWorkspaces() ([]storage.WorkspaceDto, error) {
	return f.wss, f.wsErr
}

func (f *fakeKeepLister) ListNamespaces(wsID string) ([]storage.NamespaceRow, error) {
	f.nsCall++
	if f.nsErr != nil {
		return nil, f.nsErr
	}
	return f.nss[wsID], nil
}

// A profile that has never created anything must not sweep. This is the whole
// point of the guard: the keep set would be empty, and SweepOrphans deletes
// every citeck.launcher=true container, NAMED VOLUME and network that is not in
// it — i.e. another profile's entire stand, data included. A fresh CITECK_HOME,
// a reinstall whose launcher.db was not kept and a second profile opened for
// testing all look exactly like this.
func TestASweepIsRefusedWhenTheProfileHasNoWorkspacesAtAll(t *testing.T) {
	keep, ok := orphanKeepSet(&fakeKeepLister{}, "default", "")
	assert.False(t, ok, "a store that has never held a workspace knows nothing about the host")
	assert.Nil(t, keep)
}

// The narrow shape of the guard: it is about a store that has never been
// written, NOT about an empty keep set. A workspace whose only namespace was
// deleted while its containers kept running is precisely what the sweep is for,
// so that case must still sweep — with an empty keep set.
func TestAWorkspaceWithNoNamespacesStillSweeps(t *testing.T) {
	f := &fakeKeepLister{wss: []storage.WorkspaceDto{{ID: "ws1"}}}
	keep, ok := orphanKeepSet(f, "ws1", "")
	require.True(t, ok, "the deleted-namespace case is the reason the sweep exists")
	assert.Empty(t, keep)
}

// An active namespace is evidence of its own: the daemon is about to run it, so
// there is something to protect even before the store is read.
func TestAnActiveNamespaceIsKeptAndAllowsTheSweep(t *testing.T) {
	keep, ok := orphanKeepSet(&fakeKeepLister{}, "qung26i", "iwdsjaa")
	require.True(t, ok)
	assert.True(t, keep[docker.OrphanKey("iwdsjaa", "qung26i")])
}

func TestEveryStoredNamespaceIsKept(t *testing.T) {
	f := &fakeKeepLister{
		wss: []storage.WorkspaceDto{{ID: "ws1"}, {ID: "ws2"}},
		nss: map[string][]storage.NamespaceRow{
			"ws1": {{ID: "ns1"}, {ID: "ns2"}},
			"ws2": {{ID: "ns3"}},
		},
	}
	keep, ok := orphanKeepSet(f, "ws1", "ns1")
	require.True(t, ok)
	for _, p := range [][2]string{{"ns1", "ws1"}, {"ns2", "ws1"}, {"ns3", "ws2"}} {
		assert.True(t, keep[docker.OrphanKey(p[0], p[1])], "%s/%s must be kept", p[1], p[0])
	}
}

// A listing that FAILED leaves the keep set incomplete, which is a different
// thing from a store that is empty — and both must refuse, for opposite
// reasons. Never purge on doubt.
func TestAFailedListingRefusesTheSweep(t *testing.T) {
	t.Run("workspaces", func(t *testing.T) {
		_, ok := orphanKeepSet(&fakeKeepLister{wsErr: errors.New("boom")}, "ws1", "ns1")
		assert.False(t, ok)
	})
	t.Run("namespaces", func(t *testing.T) {
		f := &fakeKeepLister{wss: []storage.WorkspaceDto{{ID: "ws1"}}, nsErr: errors.New("boom")}
		_, ok := orphanKeepSet(f, "ws1", "ns1")
		assert.False(t, ok)
	})
}
