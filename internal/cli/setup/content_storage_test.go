package setup

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

const csTestWorkspace = `contentStorage:
  s3:
    app: seaweedfs
    url: http://seaweedfs:8333
    bucket: ecos-content
    accessKey: citeck
    secretKey: ${secret:seaweedfs-s3}
additionalApps:
  - name: seaweedfs
    environments:
      AWS_SECRET_ACCESS_KEY: ${secret:seaweedfs-s3}
`

// csSetupContext is what runSetup resolves for an enterprise namespace whose
// workspace declares the provider; withProviderImage false is a release that
// stopped naming the provider's image.
func csSetupContext(t *testing.T, withProviderImage bool) *setupContext {
	t.Helper()
	var ws bundle.WorkspaceConfig
	require.NoError(t, yaml.Unmarshal([]byte(csTestWorkspace), &ws))
	require.NotNil(t, ws.ContentStorageS3())
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppContent: {Image: "enterprise/ecos-content:3.1.0"},
	}}
	if withProviderImage {
		bun.Dependencies = map[string]bundle.AppDef{"seaweedfs": {Image: "chrislusf/seaweedfs:4.48"}}
	}
	return &setupContext{
		PendingSecrets: map[string]string{},
		CurrentApps:    []string{appdef.AppContent, appdef.AppEmodel},
		Bundle:         bun,
		Workspace:      &ws,
	}
}

func csNamespace(typ namespace.ContentStorageType) *namespace.Config {
	cfg := namespace.DefaultNamespaceConfig()
	cfg.BundleRef = bundle.Ref{Repo: "enterprise", Key: "2026.3"}
	if typ != "" {
		cfg.ContentStorage = &namespace.ContentStorageProps{Type: typ}
	}
	return &cfg
}

// captureStdout returns what fn printed on stdout — where a setting prints the
// explanation of a refusal.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	os.Stdout = orig
	require.NoError(t, w.Close())
	out := <-done
	require.NoError(t, r.Close())
	return out
}

// --- the setting ---

func TestContentStorageSetting_Metadata(t *testing.T) {
	s := &contentStorageSetting{}
	assert.Equal(t, "content-storage", s.ID())
	assert.Equal(t, NamespaceFile, s.TargetFile())
	var setting Setting = s
	_, daemonApplied := setting.(daemonAppliedSetting)
	assert.True(t, daemonApplied, "the switch is written by the daemon, never by setup itself")
	_, isAction := setting.(actionSetting)
	assert.False(t, isAction, "it keeps the diff, confirmation and history of a file setting")
	assert.NotNil(t, findSettingByID("content-storage"), "registered in the menu")
}

// A bundle without ecos-content (Community) has no content storage to choose.
func TestContentStorageSetting_AvailableOnlyWithEcosContent(t *testing.T) {
	s := &contentStorageSetting{}
	assert.True(t, s.Available(nil, []string{appdef.AppEmodel, appdef.AppContent}))
	assert.False(t, s.Available(nil, []string{appdef.AppEmodel, appdef.AppGateway}))
	assert.False(t, s.Available(nil, nil))
}

func TestContentStorageSetting_CurrentValue(t *testing.T) {
	s := &contentStorageSetting{}
	assert.Equal(t, "database", s.CurrentValue(csNamespace(""), nil), "absent means DB")
	assert.Equal(t, "S3", s.CurrentValue(csNamespace(namespace.ContentStorageS3), nil))
}

func TestContentStorageSwitchCheck(t *testing.T) {
	t.Run("available: the provider comes from the workspace", func(t *testing.T) {
		avail, refusal := contentStorageSwitchCheck(csSetupContext(t, true), csNamespace(""))
		assert.Empty(t, refusal)
		assert.Equal(t, "seaweedfs", avail.ProviderApp)
		assert.Equal(t, "S3 (seaweedfs)", avail.OptionLabel(), "no title in this workspace: the app id is the label")
	})
	t.Run("S3 never goes back to DB", func(t *testing.T) {
		_, refusal := contentStorageSwitchCheck(csSetupContext(t, true), csNamespace(namespace.ContentStorageS3))
		assert.Contains(t, refusal, "cannot be switched back to the database")
	})
	t.Run("an external S3 block is the other answer to the same question", func(t *testing.T) {
		cfg := csNamespace("")
		cfg.S3 = &namespace.S3Config{Endpoint: "https://s3.example.com", Bucket: "b", AccessKey: "a", SecretKey: "s"}
		_, refusal := contentStorageSwitchCheck(csSetupContext(t, true), cfg)
		assert.Contains(t, refusal, "citeck setup s3")
	})
	t.Run("an unreadable bundle is not offered S3", func(t *testing.T) {
		ctx := csSetupContext(t, true)
		ctx.Bundle = nil
		_, refusal := contentStorageSwitchCheck(ctx, csNamespace(""))
		assert.Contains(t, refusal, "enterprise:2026.3")
		assert.Contains(t, refusal, "citeck update")
	})
	t.Run("a release without the provider image is refused in the gate's words", func(t *testing.T) {
		_, refusal := contentStorageSwitchCheck(csSetupContext(t, false), csNamespace(""))
		assert.Contains(t, refusal, "image for the S3 provider seaweedfs")
	})
	t.Run("a workspace without the block", func(t *testing.T) {
		ctx := csSetupContext(t, true)
		ctx.Workspace = &bundle.WorkspaceConfig{}
		_, refusal := contentStorageSwitchCheck(ctx, csNamespace(""))
		assert.Contains(t, refusal, "contentStorage.s3")
	})
}

// A refusal is explained BEFORE any question, and leaves the config alone:
// ErrCanceled is how runSingleSetting restores and returns quietly.
func TestContentStorageSetting_RunRefusesWithoutAsking(t *testing.T) {
	t.Cleanup(func() { daemonReachable = daemonIsRunning })
	daemonReachable = func() bool { return true }

	cfg := csNamespace(namespace.ContentStorageS3)
	var err error
	out := captureStdout(t, func() { err = (&contentStorageSetting{}).Run(csSetupContext(t, true), cfg, nil) })
	require.ErrorIs(t, err, prompt.ErrCanceled)
	assert.Contains(t, out, "cannot be switched back")
	assert.Equal(t, namespace.ContentStorageS3, cfg.ContentStorageType())

	// The switch is the daemon's to apply: with none running it is refused up
	// front rather than written into a file the provider stays detached under.
	daemonReachable = func() bool { return false }
	cfg = csNamespace("")
	out = captureStdout(t, func() { err = (&contentStorageSetting{}).Run(csSetupContext(t, true), cfg, nil) })
	require.ErrorIs(t, err, prompt.ErrCanceled)
	assert.Contains(t, out, "The daemon is not running")
	assert.Contains(t, out, "seaweedfs")
	assert.Nil(t, cfg.ContentStorage, "nothing chosen, nothing changed")
}

// The external-S3 setting is hidden on a namespace that already stores its
// content in the S3 content storage: validation refuses the two together.
func TestS3Setting_HiddenOnAnS3ContentStorageNamespace(t *testing.T) {
	s := &s3Setting{}
	apps := []string{appdef.AppContent}
	assert.True(t, s.Available(csNamespace(""), apps))
	assert.False(t, s.Available(csNamespace(namespace.ContentStorageS3), apps))
}

// --- the write ---

type fakeConfigWriter struct {
	body []byte
	err  error
}

func (f *fakeConfigWriter) PutNamespaceConfig(body []byte) (*api.ActionResultDto, error) {
	f.body = body
	if f.err != nil {
		return nil, f.err
	}
	return &api.ActionResultDto{Success: true}, nil
}

func TestWriteThroughDaemon(t *testing.T) {
	notWritten := func() bool { return false }

	w := &fakeConfigWriter{}
	outcome, _ := writeThroughDaemon(w, []byte("id: default\n"), notWritten)
	assert.Equal(t, daemonWriteApplied, outcome)
	assert.Equal(t, "id: default\n", string(w.body), "the bytes reach the daemon verbatim")

	// The daemon's refusal is its own sentence, already in the operator's
	// language — reported as it came, not as Go's wrapping of it.
	refusal := errors.Join(errors.New("put namespace config"),
		&client.APIError{Status: http.StatusConflict, Message: "bundle X has no ecos-content"})
	outcome, detail := writeThroughDaemon(&fakeConfigWriter{err: refusal}, []byte("x"), notWritten)
	assert.Equal(t, daemonWriteRefused, outcome)
	assert.Equal(t, "bundle X has no ecos-content", detail)

	outcome, detail = writeThroughDaemon(nil, []byte("x"), notWritten)
	assert.Equal(t, daemonWriteRefused, outcome)
	assert.Contains(t, detail, "daemon is not running")

	// A failed request after the daemon WROTE the config — its reload failed
	// (500), or the connection dropped — is not "nothing changed": the file is
	// the evidence, and it says S3.
	failedReload := &client.APIError{Status: http.StatusInternalServerError, Message: "internal error"}
	outcome, detail = writeThroughDaemon(&fakeConfigWriter{err: failedReload}, []byte("x"), func() bool { return true })
	assert.Equal(t, daemonWriteNotApplied, outcome)
	assert.Equal(t, "internal error", detail)
}

// fakeSetupDaemon answers the calls applyThroughDaemon makes: the raw config
// PUT (the daemon's single write path) and the namespace poll that waits for
// the reload the daemon started.
type fakeSetupDaemon struct {
	refuse string
	puts   [][]byte
	// nsStatus is the namespace status the daemon reports; "" is RUNNING.
	nsStatus string
}

func (f *fakeSetupDaemon) serve(t *testing.T) {
	f.serveWith(t, nil)
}

// serveWith lets a test act on the PUT before it is answered — writing the
// file the way the real daemon does before a reload that then fails.
func (f *fakeSetupDaemon) serveWith(t *testing.T, onPut func(body []byte) int) {
	t.Helper()
	// Short on purpose: a unix socket path is capped at ~104 bytes on macOS.
	dir, err := os.MkdirTemp("", "citeck-sock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	ln, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+api.Config, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.puts = append(f.puts, b)
		w.Header().Set("Content-Type", "application/json")
		if onPut != nil {
			if status := onPut(b); status != http.StatusOK {
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(api.ErrorDto{Error: "Internal", Code: api.ErrCodeInternalError, Message: "internal error"})
				return
			}
		}
		if f.refuse != "" {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.ErrorDto{Error: "Conflict", Code: api.ErrCodeContentStorageUnavailable, Message: f.refuse})
			return
		}
		_ = json.NewEncoder(w).Encode(api.ActionResultDto{Success: true, Message: "Configuration saved and applied"})
	})
	mux.HandleFunc(api.Namespace, func(w http.ResponseWriter, _ *http.Request) {
		status, appStatus := "RUNNING", api.AppStatusRunning
		if f.nsStatus != "" {
			status, appStatus = f.nsStatus, api.AppStatusStopped
		}
		_ = json.NewEncoder(w).Encode(api.NamespaceDto{ID: "default", Status: status,
			Apps: []api.AppDto{{Name: "seaweedfs", Status: appStatus}}})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// applyFixture writes a DB namespace.yml and returns the setting's before and
// after state as runSingleSetting holds them at the write step.
func applyFixture(t *testing.T) (before, after *namespace.Config, onDisk []byte) {
	t.Helper()
	t.Setenv("CITECK_HOME", t.TempDir())
	before = csNamespace("")
	data, err := namespace.MarshalNamespaceConfig(before)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(config.ConfDir(), 0o755))
	require.NoError(t, os.WriteFile(config.NamespaceConfigPath(), data, 0o600))
	after, err = deepCopy(before)
	require.NoError(t, err)
	after.ContentStorage = &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}
	return before, after, data
}

// The switch goes to the daemon, which writes, re-attaches the provider and
// reloads; setup records the history entry and writes NO file of its own —
// the daemon's write is the only one, behind the daemon's gate.
func TestApplyThroughDaemon_TheDaemonWritesAndSetupRecords(t *testing.T) {
	f := &fakeSetupDaemon{}
	f.serve(t)
	before, after, onDisk := applyFixture(t)
	forward, reverse, err := computeSettingDiff(NamespaceFile, before, after, nil, nil)
	require.NoError(t, err)
	restored := false

	out := captureStdout(t, func() {
		require.NoError(t, applyThroughDaemon("content-storage", csSetupContext(t, true), after, nil,
			before, nil, forward, reverse, func() { restored = true }))
	})

	require.Len(t, f.puts, 1)
	sent, err := namespace.ParseNamespaceConfig(f.puts[0])
	require.NoError(t, err)
	assert.Equal(t, namespace.ContentStorageS3, sent.ContentStorageType())
	assert.False(t, restored)
	current, err := os.ReadFile(config.NamespaceConfigPath())
	require.NoError(t, err)
	assert.Equal(t, string(onDisk), string(current), "setup itself must not write namespace.yml")

	entries, err := listPatchEntries(historyDir(config.NamespaceConfigPath()))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "content-storage", entries[0].Record.Setting)
	assert.Equal(t, "S3", entries[0].Record.Input["type"])
	assert.Contains(t, out, "Content storage is now S3: seaweedfs is starting")
}

// On a STOPPED namespace the daemon only clears the provider's detach and
// regenerates: nothing starts until the namespace does. Saying "is starting"
// and then waiting — which reports every app as updated while all of them are
// stopped — would be untrue; the operator is told to start the namespace.
func TestApplyThroughDaemon_AStoppedNamespaceIsToldToStart(t *testing.T) {
	f := &fakeSetupDaemon{nsStatus: "STOPPED"}
	f.serve(t)
	before, after, _ := applyFixture(t)
	forward, reverse, err := computeSettingDiff(NamespaceFile, before, after, nil, nil)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, applyThroughDaemon("content-storage", csSetupContext(t, true), after, nil,
			before, nil, forward, reverse, func() {}))
	})
	require.Len(t, f.puts, 1)
	assert.Contains(t, out, "The namespace is stopped: seaweedfs starts with it")
	assert.Contains(t, out, "citeck start")
	assert.NotContains(t, out, "is starting")
	assert.NotContains(t, out, "updated successfully", "nothing was waited for")
}

func TestContentStorageSwitchedNote(t *testing.T) {
	note, wait := contentStorageSwitchedNote("STOPPED", "seaweedfs")
	assert.False(t, wait)
	assert.Contains(t, note, "citeck start")
	for _, status := range []string{"RUNNING", "STARTING", "STALLED", ""} {
		note, wait = contentStorageSwitchedNote(status, "seaweedfs")
		assert.True(t, wait, status)
		assert.Contains(t, note, "seaweedfs is starting", status)
	}
}

// A refusal by the daemon's gate leaves everything as it was: the in-memory
// config is restored and no history entry claims a change that did not happen.
func TestApplyThroughDaemon_ARefusalChangesNothing(t *testing.T) {
	f := &fakeSetupDaemon{refuse: "S3 content storage is not available: no provider"}
	f.serve(t)
	before, after, onDisk := applyFixture(t)
	forward, reverse, err := computeSettingDiff(NamespaceFile, before, after, nil, nil)
	require.NoError(t, err)
	restored := false

	out := captureStdout(t, func() {
		require.NoError(t, applyThroughDaemon("content-storage", csSetupContext(t, true), after, nil,
			before, nil, forward, reverse, func() { restored = true }))
	})

	assert.True(t, restored)
	assert.Contains(t, out, "S3 content storage is not available: no provider")
	entries, err := listPatchEntries(historyDir(config.NamespaceConfigPath()))
	require.NoError(t, err)
	assert.Empty(t, entries)
	current, err := os.ReadFile(config.NamespaceConfigPath())
	require.NoError(t, err)
	assert.Equal(t, string(onDisk), string(current))
}

// The daemon wrote the config and then failed to reload: the switch HAS
// happened, so it is recorded, and the operator is told how to finish it —
// not that nothing changed.
func TestApplyThroughDaemon_AWriteTheDaemonCouldNotApplyIsRecorded(t *testing.T) {
	f := &fakeSetupDaemon{}
	before, after, _ := applyFixture(t)
	cfgPath := config.NamespaceConfigPath()
	f.serveWith(t, func(body []byte) int {
		// The real daemon persists before it reloads (handlePutConfig).
		assert.NoError(t, os.WriteFile(cfgPath, body, 0o600))
		return http.StatusInternalServerError
	})
	forward, reverse, err := computeSettingDiff(NamespaceFile, before, after, nil, nil)
	require.NoError(t, err)
	restored := false

	out := captureStdout(t, func() {
		require.NoError(t, applyThroughDaemon("content-storage", csSetupContext(t, true), after, nil,
			before, nil, forward, reverse, func() { restored = true }))
	})

	assert.False(t, restored, "the config on disk says S3 — the in-memory one must agree")
	assert.Contains(t, out, "did not finish applying it")
	assert.Contains(t, out, "citeck reload")
	assert.Contains(t, out, "citeck start seaweedfs")
	entries, err := listPatchEntries(historyDir(config.NamespaceConfigPath()))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// --- undo ---

// `setup history --undo` writes namespace.yml itself, past the daemon's gate,
// so it must not be a way around the one-way rule — nor a way into S3 that
// skips the daemon's re-attach and reload.
func TestPerformUndo_NeverChangesTheContentStorage(t *testing.T) {
	cases := map[string]struct {
		current string
		rec     *PatchRecord
	}{
		"S3 back to DB": {
			current: "id: default\ncontentStorage:\n  type: S3\n",
			rec: &PatchRecord{
				Setting: "content-storage",
				Forward: []PatchOp{{Op: "add", Path: "/contentStorage", Value: map[string]any{"type": "S3"}}},
				Reverse: []PatchOp{{Op: "remove", Path: "/contentStorage"}},
			},
		},
		"DB into S3": {
			current: "id: default\n",
			rec: &PatchRecord{
				Setting: "bridge",
				Forward: []PatchOp{{Op: "remove", Path: "/contentStorage"}},
				Reverse: []PatchOp{{Op: "add", Path: "/contentStorage", Value: map[string]any{"type": "S3"}}},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "namespace.yml")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tc.current), 0o600))

			_, err := performUndo(NamespaceFile, cfgPath, historyDir(cfgPath), "entry-1", tc.rec)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "changes the content storage")
			after, readErr := os.ReadFile(cfgPath)
			require.NoError(t, readErr)
			assert.Equal(t, tc.current, string(after), "nothing written")
		})
	}
}

// Any other undo on an S3 namespace still works, and keeps it on S3.
func TestPerformUndo_AnUnrelatedEntryOnAnS3NamespaceStillWorks(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "namespace.yml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("id: default\nproxy:\n  host: new.com\n  port: 80\ncontentStorage:\n  type: S3\n"), 0o600))

	_, err := performUndo(NamespaceFile, cfgPath, historyDir(cfgPath), "entry-1", hostnamePatchRecord("old.com", "new.com"))

	require.NoError(t, err)
	cfg, err := namespace.LoadNamespaceConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "old.com", cfg.Proxy.Host)
	assert.Equal(t, namespace.ContentStorageS3, cfg.ContentStorageType())
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "contentStorage:")
}
