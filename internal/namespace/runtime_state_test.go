package namespace

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakePersister is a concurrency-safe NsStatePersister test double. It records
// the last status/json plus a full call count and history, so tests can assert
// either the latest save (TestPersistStateUsesPersister) or observe save
// cadence / payloads over time (the migrated phase7c tests). persistState may
// be invoked from the runtimeLoop goroutine, so all access is mutex-guarded.
type fakePersister struct {
	mu      sync.Mutex
	status  string
	json    string
	calls   int
	history []string // every stateJSON saved, in order
}

func (f *fakePersister) SaveNamespaceState(status, stateJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
	f.json = stateJSON
	f.calls++
	f.history = append(f.history, stateJSON)
	return nil
}

// callCount returns the number of SaveNamespaceState invocations so far.
func (f *fakePersister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// lastJSON returns the most recently saved stateJSON, or "" if none.
func (f *fakePersister) lastJSON() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.json
}

func TestPersistStateUsesPersister(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	r.mu.Lock()
	r.status = NsStatusRunning
	r.manualStoppedApps["edi"] = true
	r.mu.Unlock()

	r.persistState()

	require.Equal(t, 1, fp.calls)
	require.Equal(t, string(NsStatusRunning), fp.status)
	require.Contains(t, fp.json, `"status":"RUNNING"`)
	require.Contains(t, fp.json, "edi")
}

func TestPersistStateNoPersisterIsNoop(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	require.NotPanics(t, func() { r.persistState() }) // nil persister -> no-op
}
