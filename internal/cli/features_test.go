package cli

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/output"
)

// fakeFeaturesDaemon is the daemon surface runFeaturesToggle needs, with the
// PUT recorded so a test can read what the command sent.
type fakeFeaturesDaemon struct {
	ns       api.NamespaceDto
	features api.ExperimentalFeaturesDto
	puts     []api.NamespaceEditDto
	putNsIDs []string
	putErr   error
}

func (f *fakeFeaturesDaemon) GetNamespace() (*api.NamespaceDto, error) { return &f.ns, nil }
func (f *fakeFeaturesDaemon) GetExperimentalFeatures() (*api.ExperimentalFeaturesDto, error) {
	return &f.features, nil
}

func (f *fakeFeaturesDaemon) PutNamespaceEdit(nsID string, edit api.NamespaceEditDto) (*api.ActionResultDto, error) {
	f.puts = append(f.puts, edit)
	f.putNsIDs = append(f.putNsIDs, nsID)
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &api.ActionResultDto{Success: true}, nil
}

// threeFeatures is a bundle offering a (on), b (off) and c (on).
func threeFeatures() api.ExperimentalFeaturesDto {
	return api.ExperimentalFeaturesDto{Features: []api.ExperimentalFeatureDto{
		{ID: "a", Title: "Alpha", Apps: []string{"alpha"}, Enabled: true},
		{ID: "b", Title: "Beta module", Description: "Still cooking.", Apps: []string{"beta", "beta-db"}},
		{ID: "c", Title: "c", Apps: []string{"gamma"}, Enabled: true},
	}}
}

// The PUT carries the resulting state of EVERY offered feature — the ids on
// before plus (or minus) the one toggled, in the daemon's order — and nothing
// else: a feature the daemon lists is the only kind the daemon accepts back.
func TestFeaturesToggle_SendsTheResultingStateOfEveryOfferedFeature(t *testing.T) {
	depsTestSetup(t)

	t.Run("enable adds the id to the ids already on", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1", Status: api.NsStatusRunning}, features: threeFeatures()}
		out := captureStdout(t, func() { require.NoError(t, runFeaturesToggle(d, "b", true)) })
		require.Len(t, d.puts, 1)
		assert.Equal(t, []string{"ns1"}, d.putNsIDs)
		require.NotNil(t, d.puts[0].EnabledExperimentalFeatures)
		assert.Equal(t, []string{"a", "b", "c"}, *d.puts[0].EnabledExperimentalFeatures)
		assert.Empty(t, d.puts[0].Name, "an edit that changes one thing sends that one thing")
		assert.Nil(t, d.puts[0].MongoEnabled)
		assert.Contains(t, out, tHelper("features.enabled.running", "id", "b", "apps", "beta, beta-db"))
	})

	t.Run("disable drops the id and keeps the others", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1", Status: api.NsStatusStopped}, features: threeFeatures()}
		out := captureStdout(t, func() { require.NoError(t, runFeaturesToggle(d, "a", false)) })
		require.Len(t, d.puts, 1)
		assert.Equal(t, []string{"c"}, *d.puts[0].EnabledExperimentalFeatures)
		assert.Contains(t, out, tHelper("features.disabled.stopped", "id", "a", "apps", "alpha"))
		assert.Contains(t, out, "citeck start", "a stopped namespace is told when the change takes effect")
	})

	t.Run("disabling the last one sends an empty list, not an absent field", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1"}, features: api.ExperimentalFeaturesDto{
			Features: []api.ExperimentalFeatureDto{{ID: "a", Apps: []string{"alpha"}, Enabled: true}}}}
		captureStdout(t, func() { require.NoError(t, runFeaturesToggle(d, "a", false)) })
		require.Len(t, d.puts, 1)
		require.NotNil(t, d.puts[0].EnabledExperimentalFeatures, "nil would mean 'leave unchanged' to the daemon")
		assert.Empty(t, *d.puts[0].EnabledExperimentalFeatures)
	})
}

// Nothing is sent when there is nothing to change or nothing to check it
// against: an unknown id is refused here naming what the bundle does offer,
// an unresolved bundle names the way out, and a no-op is reported as one.
func TestFeaturesToggle_RefusesLocallyAndNeverSendsANoOp(t *testing.T) {
	depsTestSetup(t)

	t.Run("an id the bundle does not offer", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1"}, features: threeFeatures()}
		err := runFeaturesToggle(d, "nope", true)
		require.ErrorIs(t, err, errFeatureRefused)
		assert.Contains(t, err.Error(), "a, b, c", "the refusal names the offered ids")
		assert.Empty(t, d.puts)
	})

	t.Run("a bundle that offers nothing", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1"}}
		err := runFeaturesToggle(d, "nope", true)
		require.ErrorIs(t, err, errFeatureRefused)
		assert.Contains(t, err.Error(), tHelper("features.noneOffered", "id", "nope"))
		assert.Empty(t, d.puts)
	})

	t.Run("a bundle that is not on disk", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1"},
			features: api.ExperimentalFeaturesDto{Reason: api.ExperimentalFeaturesBundleUnresolved}}
		err := runFeaturesToggle(d, "a", true)
		require.ErrorIs(t, err, errFeatureRefused)
		assert.Contains(t, err.Error(), "citeck update")
		assert.Empty(t, d.puts)
	})

	t.Run("no namespace loaded", func(t *testing.T) {
		d := &fakeFeaturesDaemon{features: threeFeatures()}
		require.ErrorIs(t, runFeaturesToggle(d, "a", true), errFeatureRefused)
		assert.Empty(t, d.puts)
	})

	t.Run("already in the requested state", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1"}, features: threeFeatures()}
		out := captureStdout(t, func() { require.NoError(t, runFeaturesToggle(d, "a", true)) })
		assert.Empty(t, d.puts, "no PUT for a no-op")
		assert.Contains(t, out, tHelper("features.alreadyOn", "id", "a"))
	})

	t.Run("the daemon's refusal is the error", func(t *testing.T) {
		d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1"}, features: threeFeatures(),
			putErr: errors.New("bundle community:2026.1 does not offer experimental feature(s) b")}
		err := runFeaturesToggle(d, "b", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not offer")
	})
}

// --format json prints one verdict object for a toggle: a script reads the
// state after the command and whether anything was sent.
func TestFeaturesToggle_JSONVerdict(t *testing.T) {
	depsTestSetup(t)
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	d := &fakeFeaturesDaemon{ns: api.NamespaceDto{ID: "ns1", Status: api.NsStatusRunning}, features: threeFeatures()}
	out := captureStdout(t, func() { require.NoError(t, runFeaturesToggle(d, "b", true)) })
	var report featuresToggleReport
	require.NoError(t, json.Unmarshal([]byte(out), &report), out)
	assert.Equal(t, featuresToggleReport{ID: "b", Enabled: true, Changed: true,
		NamespaceStatus: api.NsStatusRunning, Apps: []string{"beta", "beta-db"}}, report)

	out = captureStdout(t, func() { require.NoError(t, runFeaturesToggle(d, "a", true)) })
	require.NoError(t, json.Unmarshal([]byte(out), &report), out)
	assert.False(t, report.Changed, "a no-op says so instead of printing prose")
	assert.True(t, report.Enabled)
}

func TestFeaturesToggleMessageKey(t *testing.T) {
	assert.Equal(t, "features.enabled.running", featuresToggleMessageKey(true, api.NsStatusRunning))
	assert.Equal(t, "features.enabled.running", featuresToggleMessageKey(true, api.NsStatusStarting))
	assert.Equal(t, "features.enabled.stopped", featuresToggleMessageKey(true, api.NsStatusStopped))
	assert.Equal(t, "features.disabled.running", featuresToggleMessageKey(false, api.NsStatusStalled))
	assert.Equal(t, "features.disabled.stopped", featuresToggleMessageKey(false, api.NsStatusStopped))
}

// The list: one row per feature with its state, each description under the
// table, the beta legend — and for the two states with no rows, one sentence
// each (an unresolved bundle names `citeck update`).
func TestFeaturesListLines(t *testing.T) {
	depsTestSetup(t)

	dto := threeFeatures()
	lines := featuresListLines(&dto)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"a", "Alpha", "alpha", "b", "Beta module", "beta, beta-db",
		tHelper("features.state.on"), tHelper("features.state.off"),
		tHelper("features.descriptionLine", "id", "b", "description", "Still cooking."),
		tHelper("features.legend"),
		tHelper("features.col.id"), tHelper("features.col.state")} {
		assert.Contains(t, text, want)
	}
	assert.Equal(t, 1, strings.Count(text, "Still cooking."), "a description is a line of its own, not a cell")
	assert.NotContains(t, text, "features.", "no bare key leaked")

	empty := featuresListLines(&api.ExperimentalFeaturesDto{})
	assert.Equal(t, []string{tHelper("features.empty")}, empty)

	unresolved := featuresListLines(&api.ExperimentalFeaturesDto{Reason: api.ExperimentalFeaturesBundleUnresolved})
	require.Len(t, unresolved, 1)
	assert.Contains(t, unresolved[0], "citeck update")
}

// The command tree over a real socket: `citeck features` prints the daemon's
// DTO verbatim in JSON mode, and `citeck features enable` reaches the edit
// endpoint of the namespace `GET /namespace` named. Run with TMPDIR=/tmp — a
// unix socket path has a length limit the default macOS temp dir exceeds.
func TestFeaturesCommands_OverTheSocket(t *testing.T) {
	depsTestSetup(t)
	dir := t.TempDir()
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")
	listener, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
	require.NoError(t, err)

	// The handler runs on the server's goroutine; the mutex is what lets the
	// test read what it recorded without a data race.
	var mu sync.Mutex
	var puts atomic.Int32
	var lastPut api.NamespaceEditDto
	var lastPutErr error
	recorded := func() (api.NamespaceEditDto, error) {
		mu.Lock()
		defer mu.Unlock()
		return lastPut, lastPutErr
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.Namespace, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.NamespaceDto{ID: "default", Status: api.NsStatusStopped})
	})
	mux.HandleFunc("GET "+api.ExperimentalFeatures, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(threeFeatures())
	})
	mux.HandleFunc("PUT "+api.NamespaceEditPath("default"), func(w http.ResponseWriter, r *http.Request) {
		puts.Add(1)
		mu.Lock()
		defer mu.Unlock()
		lastPutErr = json.NewDecoder(r.Body).Decode(&lastPut)
		_ = json.NewEncoder(w).Encode(api.ActionResultDto{Success: true, Message: "namespace updated"})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	t.Run("list prints the DTO as-is in JSON mode", func(t *testing.T) {
		prev := output.GetFormat()
		output.SetFormat(output.FormatJSON)
		t.Cleanup(func() { output.SetFormat(prev) })
		list := newFeaturesListCmd()
		out := captureStdout(t, func() { require.NoError(t, list.RunE(list, nil)) })
		var got api.ExperimentalFeaturesDto
		require.NoError(t, json.Unmarshal([]byte(out), &got), out)
		assert.Equal(t, threeFeatures(), got)
	})

	t.Run("the bare command lists too", func(t *testing.T) {
		root := newFeaturesCmd()
		out := captureStdout(t, func() { require.NoError(t, root.RunE(root, nil)) })
		assert.Contains(t, out, "Beta module")
		assert.Contains(t, out, tHelper("features.legend"))
	})

	t.Run("enable PUTs the edit of the active namespace", func(t *testing.T) {
		enable := newFeaturesToggleCmd("enable", true)
		out := captureStdout(t, func() { require.NoError(t, enable.RunE(enable, []string{"b"})) })
		assert.Equal(t, int32(1), puts.Load())
		put, putErr := recorded()
		require.NoError(t, putErr)
		require.NotNil(t, put.EnabledExperimentalFeatures)
		assert.Equal(t, []string{"a", "b", "c"}, *put.EnabledExperimentalFeatures)
		assert.Contains(t, out, tHelper("features.enabled.stopped", "id", "b", "apps", "beta, beta-db"))
	})

	t.Run("disable of an unknown id is refused before any request", func(t *testing.T) {
		disable := newFeaturesToggleCmd("disable", false)
		require.Error(t, disable.RunE(disable, []string{"nope"}))
		assert.Equal(t, int32(1), puts.Load(), "no second PUT")
	})
}
