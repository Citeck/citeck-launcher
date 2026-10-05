package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// The S3 provider's resolved secret in these tests. Long and distinctive on
// purpose: the redaction test greps for it.
const csSecretValue = "s3cret-value-7f1c"

// fakeECOS plays the three ECOS apps behind the gateway: the default storage
// config, and the records the connection creates.
type fakeECOS struct {
	mu sync.Mutex

	defaultRef string // the ref inside the config's value
	valueNull  bool   // the config answers a null value
	// notDeployedQueries answers this many CONFIG queries with
	// `_notExists: true` — the artifact not deployed yet.
	notDeployedQueries int
	exists             map[string]bool // full ref -> exists

	// failQueries answers this many queries with failWith first.
	failQueries int
	failWith    error
	// queryErr answers every query (after failQueries) with this error.
	queryErr error
	// blockQuery, when set, holds every query until it is closed or the
	// caller's context ends — or, with blockIgnoresCtx, until it is closed:
	// an answer that was already on its way when the run was canceled.
	blockQuery      chan struct{}
	blockIgnoresCtx bool

	mutateErr     map[string]error // keyed by the id mutated
	ignoreDefault bool             // accept the default mutation, change nothing

	queries   int
	mutations []fakeMutation
}

type fakeMutation struct {
	id   string
	atts map[string]any
}

func newFakeECOS() *fakeECOS {
	return &fakeECOS{defaultRef: csLocalStorageRef, exists: map[string]bool{}, mutateErr: map[string]error{}}
}

func (f *fakeECOS) Query(ctx context.Context, refs []string, _ map[string]string) ([]map[string]json.RawMessage, error) {
	f.mu.Lock()
	f.queries++
	block, ignoreCtx := f.blockQuery, f.blockIgnoresCtx
	f.mu.Unlock()
	switch {
	case block != nil && ignoreCtx:
		<-block
	case block != nil:
		select {
		case <-block:
		case <-ctx.Done():
			return nil, &ecosRecordsError{Err: ctx.Err()}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failQueries > 0 {
		f.failQueries--
		return nil, f.failWith
	}
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	out := make([]map[string]json.RawMessage, len(refs))
	for i, ref := range refs {
		m := map[string]json.RawMessage{}
		if ref == csConfigRef {
			notDeployed := f.notDeployedQueries > 0
			if notDeployed {
				f.notDeployedQueries--
			}
			m["notExists"], _ = json.Marshal(notDeployed)
			if f.valueNull {
				m["value"] = json.RawMessage("null")
			} else {
				m["value"], _ = json.Marshal(map[string]any{"ref": f.defaultRef, "config": map[string]any{}})
			}
		} else {
			m["notExists"], _ = json.Marshal(!f.exists[ref])
		}
		out[i] = m
	}
	return out, nil
}

func (f *fakeECOS) Mutate(ctx context.Context, id string, atts map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		// What the real client does with a canceled request: nothing reaches ECOS.
		return "", &ecosRecordsError{Err: ctx.Err()}
	}
	f.mutations = append(f.mutations, fakeMutation{id: id, atts: atts})
	if err := f.mutateErr[id]; err != nil {
		return "", err
	}
	if id == csConfigRef {
		if !f.ignoreDefault {
			value, _ := atts["_value?json"].(map[string]any)
			f.defaultRef, _ = value["ref"].(string)
		}
		return id, nil
	}
	localID, _ := atts["id"].(string)
	f.exists[id+localID] = true
	return id + localID, nil
}

func (f *fakeECOS) snapshot() (queries int, mutations []fakeMutation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries, append([]fakeMutation(nil), f.mutations...)
}

func (f *fakeECOS) mutatedIDs() []string {
	_, muts := f.snapshot()
	ids := make([]string, len(muts))
	for i, m := range muts {
		ids[i] = m.id
	}
	return ids
}

// csHarness is an S3 namespace whose platform is up, a provisioner wired to a
// fake ECOS, and the secret the workspace's `${secret:seaweedfs-s3}` resolves to.
type csHarness struct {
	d    *Daemon
	p    *contentStorageProvisioner
	rt   *namespace.Runtime
	ecos *fakeECOS

	secretValues map[string]string
	secretErr    error
}

func newCSHarness(t *testing.T) *csHarness {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	h := &csHarness{
		rt:           rt,
		ecos:         newFakeECOS(),
		secretValues: map[string]string{"seaweedfs-s3": csSecretValue},
	}
	h.setApps(nil)
	h.d = &Daemon{activeNs: &activeNamespace{
		workspaceID:     "ws1",
		nsConfig:        csS3Config("ns1"),
		workspaceConfig: parseCSWorkspace(t),
		runtime:         rt,
	}}
	h.p = h.d.contentStorageProv()
	h.p.records = func(string) ecosRecords { return h.ecos }
	h.p.gatewayAddr = func(context.Context, *namespace.Runtime) (string, error) { return "gateway:8094", nil }
	h.p.secrets = func(string, string, *bundle.WorkspaceConfig) (map[string]string, error) {
		return h.secretValues, h.secretErr
	}
	h.p.pollInterval = time.Millisecond
	h.p.readyTimeout = 10 * time.Second
	return h
}

func csS3Config(id string) *namespace.Config {
	return &namespace.Config{ID: id, ContentStorage: &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}}
}

// setApps puts the platform apps and the provider in the runtime, RUNNING
// unless overridden.
func (h *csHarness) setApps(override map[string]namespace.AppRuntimeStatus) {
	apps := make([]*namespace.AppRuntime, 0, 5)
	for _, name := range []string{appdef.AppGateway, appdef.AppEmodel, appdef.AppEapps, appdef.AppContent, csProvider} {
		st := namespace.AppStatusRunning
		if s, ok := override[name]; ok {
			st = s
		}
		apps = append(apps, &namespace.AppRuntime{Name: name, Status: st, ContainerID: "c-" + name})
	}
	h.rt.InjectAppsForTest(apps...)
}

// wait blocks until the run in flight, if any, has finished.
func (h *csHarness) wait(t *testing.T) {
	t.Helper()
	h.p.mu.Lock()
	j := h.p.job
	h.p.mu.Unlock()
	if j == nil {
		return
	}
	select {
	case <-j.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the content storage run did not finish")
	}
}

// retry is the Retry route, run to completion.
func (h *csHarness) retry(t *testing.T) ContentStorageStatus {
	t.Helper()
	h.p.Retry("ns1")
	h.wait(t)
	return h.p.Status("ns1")
}

// event is one qualifying runtime event, evaluated synchronously.
func (h *csHarness) event(t *testing.T) ContentStorageStatus {
	t.Helper()
	h.p.evaluate(false, "")
	h.wait(t)
	return h.p.Status("ns1")
}

func (h *csHarness) subscribe(t *testing.T) chan api.EventDto {
	t.Helper()
	ch, _, ok := h.d.addSubscriber()
	require.True(t, ok)
	return ch
}

// drainStates is every content-storage state broadcast so far.
func drainStates(ch chan api.EventDto) []string {
	var out []string
	for {
		select {
		case evt := <-ch:
			if evt.Type == contentStorageEventType {
				out = append(out, evt.After)
			}
		default:
			return out
		}
	}
}

// --- the decision ---

// The ordinary first run: ECOS on its built-in LOCAL storage, none of our
// records there. All three are created, in dependency order, with exactly the
// attributes ECOS's own forms write, the default switched, and read back.
func TestContentStorageProvision_CreatesTheRecordsAndSwitchesTheDefault(t *testing.T) {
	h := newCSHarness(t)
	events := h.subscribe(t)

	st := h.retry(t)
	assert.Equal(t, api.ContentStorageStateConnected, st.State)
	assert.True(t, st.ErrorMsg.Empty())
	assert.False(t, st.At.IsZero())

	_, muts := h.ecos.snapshot()
	require.Len(t, muts, 4)
	assert.Equal(t, fakeMutation{id: "emodel/secret@", atts: map[string]any{
		"id":        "seaweedfs-s3-credentials",
		"name":      "seaweedfs S3 credentials",
		"type":      "BASIC",
		"data?json": map[string]any{"username": "citeck", "password": csSecretValue},
	}}, muts[0])
	assert.Equal(t, fakeMutation{id: "emodel/endpoint@", atts: map[string]any{
		"id":          "seaweedfs-s3",
		"name":        "seaweedfs S3",
		"url":         "http://seaweedfs:8333",
		"credentials": "emodel/secret@seaweedfs-s3-credentials",
	}}, muts[1])
	assert.Equal(t, fakeMutation{id: "content/storage@", atts: map[string]any{
		"id":   "seaweedfs-s3",
		"name": "seaweedfs S3",
		"type": "S3",
		"config?json": map[string]any{
			"endpoint":   "emodel/endpoint@seaweedfs-s3",
			"bucketName": "ecos-content",
		},
	}}, muts[2])
	assert.Equal(t, fakeMutation{id: csConfigRef, atts: map[string]any{
		"_value?json": map[string]any{"ref": "content/storage@seaweedfs-s3", "config": map[string]any{}},
	}}, muts[3])

	assert.Equal(t, []string{
		api.ContentStorageStatePending + ">" + api.ContentStorageStateWaiting,
		api.ContentStorageStateWaiting + ">" + api.ContentStorageStateProvisioning,
		api.ContentStorageStateProvisioning + ">" + api.ContentStorageStateConnected,
	}, drainTransitions(events, "ns1"), "every state change reaches the SSE stream")
}

// drainTransitions is every content-storage event so far as "before>after",
// checking each names the namespace it is about.
func drainTransitions(ch chan api.EventDto, nsID string) []string {
	var out []string
	for {
		select {
		case evt := <-ch:
			if evt.Type == contentStorageEventType && evt.NamespaceID == nsID && evt.Timestamp > 0 {
				out = append(out, evt.Before+">"+evt.After)
			}
		default:
			return out
		}
	}
}

// A config with no ref at all is the same as LOCAL: nobody chose anything.
func TestContentStorageProvision_AnEmptyDefaultIsNotAChoice(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.defaultRef = ""
	assert.Equal(t, api.ContentStorageStateConnected, h.retry(t).State)
	assert.Len(t, h.ecos.mutatedIDs(), 4)
}

// Already connected (a second start, a daemon restart): the answer is read,
// nothing is written.
func TestContentStorageProvision_OurDefaultIsConnectedWithNoWrites(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.defaultRef = "content/storage@seaweedfs-s3"
	assert.Equal(t, api.ContentStorageStateConnected, h.retry(t).State)
	assert.Empty(t, h.ecos.mutatedIDs())
}

// An administrator chose another storage — including ecos-content's own
// example S3 storage. Never overwritten.
func TestContentStorageProvision_AnotherDefaultIsAdminManagedWithNoWrites(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.defaultRef = "content/storage@content-storage-s3"
	assert.Equal(t, api.ContentStorageStateAdminManaged, h.retry(t).State)
	assert.Empty(t, h.ecos.mutatedIDs())
}

// Our storage exists but the default is back on LOCAL: somebody switched it
// back on purpose. Not undone.
func TestContentStorageProvision_OurStorageBackOnLocalIsAdminManagedWithNoWrites(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.exists["content/storage@seaweedfs-s3"] = true
	assert.Equal(t, api.ContentStorageStateAdminManaged, h.retry(t).State)
	assert.Empty(t, h.ecos.mutatedIDs())
}

// A run interrupted half-way (or an administrator who made the secret by
// hand) leaves some records: only the missing ones are created, an existing
// one is never updated.
func TestContentStorageProvision_CreatesOnlyWhatIsMissing(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.exists["emodel/secret@seaweedfs-s3-credentials"] = true
	assert.Equal(t, api.ContentStorageStateConnected, h.retry(t).State)
	assert.Equal(t, []string{"emodel/endpoint@", "content/storage@", csConfigRef}, h.ecos.mutatedIDs())

	h2 := newCSHarness(t)
	h2.ecos.exists["emodel/secret@seaweedfs-s3-credentials"] = true
	h2.ecos.exists["emodel/endpoint@seaweedfs-s3"] = true
	assert.Equal(t, api.ContentStorageStateConnected, h2.retry(t).State)
	assert.Equal(t, []string{"content/storage@", csConfigRef}, h2.ecos.mutatedIDs())
}

// The gateway answers a failed write with messages[] at level ERROR (and HTTP
// 500). That is the error state, the steps after it do not run, and the key
// never reaches the operator even if ECOS echoes it.
func TestContentStorageProvision_AnERRORAnswerIsTheErrorState(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.mutateErr["content/storage@"] = &ecosRecordsError{
		Status:   http.StatusInternalServerError,
		Messages: []string{"IllegalStateException: cannot reach bucket with key " + csSecretValue},
	}
	events := h.subscribe(t)
	st := h.retry(t)
	assert.Equal(t, api.ContentStorageStateError, st.State)
	assert.Equal(t, "contentStorage.msg.provision.createFailed", st.ErrorMsg.Key)
	rendered := englishForLogs.Render(st.ErrorMsg)
	assert.Contains(t, strings.Join(st.ErrorMsg.Args, " "), "content/storage@seaweedfs-s3")
	assert.NotContains(t, strings.Join(st.ErrorMsg.Args, " "), csSecretValue, "the secret key is redacted")
	assert.NotContains(t, rendered, csSecretValue)
	assert.Equal(t, []string{"emodel/secret@", "emodel/endpoint@", "content/storage@"}, h.ecos.mutatedIDs(),
		"the default is not switched to a storage that was not created")
	assert.Equal(t, api.ContentStorageStateError, drainStates(events)[2])

	// The default mutation failing is its own sentence.
	h2 := newCSHarness(t)
	h2.ecos.mutateErr[csConfigRef] = &ecosRecordsError{Status: 500, Messages: []string{"boom"}}
	assert.Equal(t, "contentStorage.msg.provision.setDefaultFailed", h2.retry(t).ErrorMsg.Key)
}

// The default was written but reads back as something else: not connected.
func TestContentStorageProvision_VerifiesTheDefaultItSet(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.ignoreDefault = true
	st := h.retry(t)
	assert.Equal(t, api.ContentStorageStateError, st.State)
	assert.Equal(t, "contentStorage.msg.provision.verifyFailed", st.ErrorMsg.Key)
	assert.Contains(t, st.ErrorMsg.Args, csLocalStorageRef)
}

// --- readiness ---

// A gateway that is up is not a platform that is ready: the admin user is
// created by an emodel patch, the config artifact deploys asynchronously, the
// connection can be refused outright. All of it is waited out; nothing is
// written before the answers are all there.
func TestContentStorageProvision_WaitsUntilECOSIsReady(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.failQueries = 2
	h.ecos.failWith = &ecosRecordsError{Status: http.StatusInternalServerError,
		Err: errors.New("User 'admin' is not created yet. Please try again later")}
	h.ecos.notDeployedQueries = 2
	events := h.subscribe(t)

	assert.Equal(t, api.ContentStorageStateConnected, h.retry(t).State)
	queries, muts := h.ecos.snapshot()
	assert.GreaterOrEqual(t, queries, 2+2+2, "two refusals, two undeployed configs, then the real read")
	assert.Len(t, muts, 4)
	assert.Equal(t, []string{
		api.ContentStorageStateWaiting, api.ContentStorageStateProvisioning, api.ContentStorageStateConnected,
	}, drainStates(events))

	// A refused connection (no answer at all) is waited out the same way.
	h2 := newCSHarness(t)
	h2.ecos.failQueries = 3
	h2.ecos.failWith = &ecosRecordsError{Err: errors.New("connect: connection refused")}
	assert.Equal(t, api.ContentStorageStateConnected, h2.retry(t).State)
}

// A record source that cannot say whether a record exists — the content app's
// DAO not registered yet — is NOT "the record does not exist". Reading it that
// way would create a duplicate of something that is there.
func TestContentStorageSnapshot_AnUnansweredExistenceIsNotReady(t *testing.T) {
	client := queryFunc(func(refs []string) ([]map[string]json.RawMessage, error) {
		if refs[0] == csConfigRef {
			return []map[string]json.RawMessage{{"notExists": json.RawMessage("false"), "value": json.RawMessage(`{"ref":""}`)}}, nil
		}
		return []map[string]json.RawMessage{
			{"notExists": json.RawMessage("false")},
			{"notExists": json.RawMessage("true")},
			{"notExists": json.RawMessage("null")},
		}, nil
	})
	_, err := csReadSnapshot(context.Background(), client, csRecordIDsFor(csProvider))
	var notReady *csNotReadyError
	require.ErrorAs(t, err, &notReady, "null is not false")
	assert.Contains(t, err.Error(), "content/storage@seaweedfs-s3")
	assert.True(t, csRetryable(err))

	absent := queryFunc(func(refs []string) ([]map[string]json.RawMessage, error) {
		if refs[0] == csConfigRef {
			return []map[string]json.RawMessage{{"notExists": json.RawMessage("false"), "value": json.RawMessage(`{"ref":""}`)}}, nil
		}
		return []map[string]json.RawMessage{{}, {"notExists": json.RawMessage("true")}, {"notExists": json.RawMessage("true")}}, nil
	})
	_, err = csReadSnapshot(context.Background(), absent, csRecordIDsFor(csProvider))
	require.ErrorAs(t, err, &notReady, "an absent attribute is not false either")

	// A config that has no value yet is not ready either; one whose value the
	// launcher cannot read is not something it may replace — a hard error.
	nullValue := queryFunc(func([]string) ([]map[string]json.RawMessage, error) {
		return []map[string]json.RawMessage{{"notExists": json.RawMessage("false"), "value": json.RawMessage("null")}}, nil
	})
	_, err = csReadSnapshot(context.Background(), nullValue, csRecordIDsFor(csProvider))
	require.ErrorAs(t, err, &notReady)
	strange := queryFunc(func([]string) ([]map[string]json.RawMessage, error) {
		return []map[string]json.RawMessage{{"notExists": json.RawMessage("false"), "value": json.RawMessage(`["x"]`)}}, nil
	})
	_, err = csReadSnapshot(context.Background(), strange, csRecordIDsFor(csProvider))
	require.Error(t, err)
	assert.False(t, csRetryable(err))
}

// queryFunc is an ecosRecords that only answers queries.
type queryFunc func(refs []string) ([]map[string]json.RawMessage, error)

func (f queryFunc) Query(_ context.Context, refs []string, _ map[string]string) ([]map[string]json.RawMessage, error) {
	return f(refs)
}

func (queryFunc) Mutate(context.Context, string, map[string]any) (string, error) {
	return "", errors.New("queryFunc does not mutate")
}

// Fifteen minutes of a platform that never answers is the error state, with
// nothing written.
func TestContentStorageProvision_GivesUpAfterTheReadinessBudget(t *testing.T) {
	h := newCSHarness(t)
	h.p.readyTimeout = 30 * time.Millisecond
	h.ecos.queryErr = &ecosRecordsError{Status: http.StatusServiceUnavailable, Err: errors.New("starting")}
	st := h.retry(t)
	assert.Equal(t, api.ContentStorageStateError, st.State)
	assert.Equal(t, "contentStorage.msg.provision.notReady", st.ErrorMsg.Key)
	assert.Empty(t, h.ecos.mutatedIDs())
}

// A refusal waiting will not change (the gateway does not accept the identity)
// is said at once rather than after fifteen minutes.
func TestContentStorageProvision_APermanentRefusalIsNotWaitedOut(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.queryErr = &ecosRecordsError{Status: http.StatusForbidden, Err: errors.New("forbidden")}
	st := h.retry(t)
	assert.Equal(t, api.ContentStorageStateError, st.State)
	assert.Equal(t, "contentStorage.msg.provision.readFailed", st.ErrorMsg.Key)
	queries, muts := h.ecos.snapshot()
	assert.Equal(t, 1, queries)
	assert.Empty(t, muts)
}

// The platform going away during the wait ends the run without a verdict:
// back to pending, and the next start runs it again.
func TestContentStorageProvision_StopsWaitingWhenThePlatformGoesAway(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.queryErr = &ecosRecordsError{Status: http.StatusServiceUnavailable, Err: errors.New("starting")}
	h.p.Retry("ns1")
	require.Eventually(t, func() bool { q, _ := h.ecos.snapshot(); return q >= 2 }, 5*time.Second, time.Millisecond)
	h.setApps(map[string]namespace.AppRuntimeStatus{appdef.AppContent: namespace.AppStatusStarting})
	h.wait(t)
	assert.Equal(t, api.ContentStorageStatePending, h.p.Status("ns1").State)
	assert.Empty(t, h.ecos.mutatedIDs())

	h.ecos.mu.Lock()
	h.ecos.queryErr = nil
	h.ecos.mu.Unlock()
	h.setApps(nil)
	assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State, "the latch was re-armed")
}

// --- when it runs ---

// A DB namespace is never touched — not by an event, not by Retry.
func TestContentStorageProvision_NeverForADBNamespace(t *testing.T) {
	h := newCSHarness(t)
	h.d.activeNs.nsConfig = &namespace.Config{ID: "ns1"}
	h.event(t)
	h.retry(t)
	queries, muts := h.ecos.snapshot()
	assert.Zero(t, queries)
	assert.Empty(t, muts)
	assert.Equal(t, api.ContentStorageStatePending, h.p.Status("ns1").State)
}

// A generation that could not wire the provider (a locked vault, a provider
// whose own dependency is missing) is not connected to anything.
func TestContentStorageProvision_NotWhileTheGenerationReportsAProblem(t *testing.T) {
	h := newCSHarness(t)
	h.d.activeNs.contentStorageProblem = &namespace.ContentStorageProblem{
		Reason: namespace.ContentStorageProviderNotGenerated, ProviderApp: csProvider,
	}
	h.retry(t)
	queries, _ := h.ecos.snapshot()
	assert.Zero(t, queries)
}

// The gate is the four ECOS apps AND the provider: switching the default
// storage to an S3 server that is down makes every upload fail at once.
func TestContentStorageProvision_WaitsForThePlatformAndTheProvider(t *testing.T) {
	for _, app := range []string{appdef.AppGateway, appdef.AppEmodel, appdef.AppEapps, appdef.AppContent, csProvider} {
		t.Run(app, func(t *testing.T) {
			h := newCSHarness(t)
			h.setApps(map[string]namespace.AppRuntimeStatus{app: namespace.AppStatusStarting})
			assert.Equal(t, api.ContentStorageStatePending, h.event(t).State)
			assert.Equal(t, api.ContentStorageStatePending, h.retry(t).State, "Retry does not bypass the gate")
			queries, _ := h.ecos.snapshot()
			assert.Zero(t, queries)

			h.setApps(nil)
			assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State)
		})
	}
}

// A snapshot or a dependency migration/rollback owns the namespace's data:
// nothing is asked or written until it is over, and the state stays pending.
// An update pass or an ordinary request does not hold it back.
func TestContentStorageProvision_NotWhileALongOperationOwnsTheData(t *testing.T) {
	for _, kind := range []longOpKind{longOpSnapshot, longOpMigration, longOpDepsRollback} {
		t.Run(string(kind), func(t *testing.T) {
			h := newCSHarness(t)
			require.True(t, h.d.longOp.TryLock(kind))
			assert.Equal(t, api.ContentStorageStatePending, h.event(t).State)
			assert.Equal(t, api.ContentStorageStatePending, h.retry(t).State)
			queries, _ := h.ecos.snapshot()
			assert.Zero(t, queries)

			h.d.longOp.Unlock()
			assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State, "the next event runs it")
		})
	}
	for _, kind := range []longOpKind{longOpUpdatePass, longOpRequest} {
		t.Run(string(kind), func(t *testing.T) {
			h := newCSHarness(t)
			require.True(t, h.d.longOp.TryLock(kind))
			defer h.d.longOp.Unlock()
			assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State)
		})
	}
}

// A snapshot import stops the platform while it holds the lock and starts it
// again: the restored ECOS is a different ECOS, and its answer is re-derived
// once the lock is released — never while it is held.
func TestContentStorageProvision_ReDerivesAfterASnapshotRestore(t *testing.T) {
	h := newCSHarness(t)
	require.Equal(t, api.ContentStorageStateConnected, h.event(t).State)
	q1, _ := h.ecos.snapshot()

	require.True(t, h.d.longOp.TryLock(longOpSnapshot))
	h.setApps(map[string]namespace.AppRuntimeStatus{
		appdef.AppGateway: namespace.AppStatusStopped, appdef.AppContent: namespace.AppStatusStopped,
	})
	h.event(t)
	// The snapshot held a wiped content database: ECOS is back on LOCAL.
	h.ecos.mu.Lock()
	h.ecos.defaultRef = csLocalStorageRef
	h.ecos.exists = map[string]bool{}
	h.ecos.mu.Unlock()
	h.setApps(nil)
	h.event(t)
	q2, _ := h.ecos.snapshot()
	assert.Equal(t, q1, q2, "nothing is asked while the snapshot holds the lock")

	h.d.longOp.Unlock()
	assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State)
	assert.Len(t, h.ecos.mutatedIDs(), 8, "the restored platform was connected again")
}

// Once per time the platform comes up: events while it stays up do not run it
// again, a restart of the platform does — and a re-verification does not flash
// "waiting" over a namespace that is already connected.
func TestContentStorageProvision_RunsOncePerPlatformStart(t *testing.T) {
	h := newCSHarness(t)
	assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State)
	q1, _ := h.ecos.snapshot()

	h.event(t)
	h.event(t)
	q2, _ := h.ecos.snapshot()
	assert.Equal(t, q1, q2, "no run while the platform stays up")

	events := h.subscribe(t)
	h.setApps(map[string]namespace.AppRuntimeStatus{appdef.AppGateway: namespace.AppStatusStopped})
	h.event(t)
	h.setApps(nil)
	assert.Equal(t, api.ContentStorageStateConnected, h.event(t).State)
	q3, _ := h.ecos.snapshot()
	assert.Greater(t, q3, q2, "a platform restart re-derives the answer")
	assert.Empty(t, drainStates(events), "a verdict that did not change is not re-announced")
	assert.Len(t, h.ecos.mutatedIDs(), 4, "and re-deriving it wrote nothing")
}

// After an error a qualifying event retries, but at most every five minutes;
// the Retry route does not wait.
func TestContentStorageProvision_RetriesAnErrorAtMostEveryFiveMinutes(t *testing.T) {
	h := newCSHarness(t)
	var clockMu sync.Mutex
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	h.p.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	advance := func(d time.Duration) { clockMu.Lock(); now = now.Add(d); clockMu.Unlock() }
	h.ecos.queryErr = &ecosRecordsError{Status: http.StatusForbidden, Err: errors.New("forbidden")}

	assert.Equal(t, api.ContentStorageStateError, h.event(t).State)
	queries := func() int { q, _ := h.ecos.snapshot(); return q }
	assert.Equal(t, 1, queries())

	advance(4 * time.Minute)
	h.event(t)
	assert.Equal(t, 1, queries(), "not within five minutes")

	advance(time.Minute)
	h.event(t)
	assert.Equal(t, 2, queries(), "five minutes later the next event retries")

	h.retry(t)
	assert.Equal(t, 3, queries(), "Retry runs at once")

	h.ecos.mu.Lock()
	h.ecos.queryErr = nil
	h.ecos.mu.Unlock()
	assert.Equal(t, api.ContentStorageStateConnected, h.retry(t).State)
}

// A Retry while the platform is down cannot run, and does not leave the old
// error up either.
func TestContentStorageProvision_RetryWhileDownClearsTheError(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.queryErr = &ecosRecordsError{Status: http.StatusForbidden, Err: errors.New("forbidden")}
	require.Equal(t, api.ContentStorageStateError, h.retry(t).State)
	h.setApps(map[string]namespace.AppRuntimeStatus{appdef.AppEapps: namespace.AppStatusStopped})
	assert.Equal(t, api.ContentStorageStatePending, h.retry(t).State)
}

// Switching namespace cancels the run in flight and forgets it: nothing it
// would have written lands, and no verdict survives for the new namespace.
func TestContentStorageProvision_ResetCancelsTheRun(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.blockQuery = make(chan struct{})
	h.p.Retry("ns1")
	require.Equal(t, api.ContentStorageStateWaiting, h.p.Status("ns1").State)
	j := h.currentJob(t)

	h.p.Reset()
	awaitJob(t, j)
	assert.Equal(t, api.ContentStorageStatePending, h.p.Status("ns1").State)
	assert.Empty(t, h.ecos.mutatedIDs())
	h.p.mu.Lock()
	assert.Nil(t, h.p.job)
	assert.Empty(t, h.p.status.State, "the canceled run wrote nothing after the reset")
	h.p.mu.Unlock()
}

// An answer already on its way when the run was canceled must not land
// either: a run writes state only while it is still THE run.
func TestContentStorageProvision_ACanceledRunWritesNoVerdict(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.defaultRef = "content/storage@seaweedfs-s3"
	h.ecos.blockQuery = make(chan struct{})
	h.ecos.blockIgnoresCtx = true
	events := h.subscribe(t)
	h.p.Retry("ns1")
	j := h.currentJob(t)
	require.Eventually(t, func() bool { q, _ := h.ecos.snapshot(); return q >= 1 }, 5*time.Second, time.Millisecond,
		"the run is waiting on ECOS")

	h.p.Reset()
	close(h.ecos.blockQuery)
	awaitJob(t, j)
	h.p.mu.Lock()
	assert.Empty(t, h.p.status.State, "the late verdict was dropped")
	h.p.mu.Unlock()
	assert.Equal(t, []string{api.ContentStorageStateWaiting}, drainStates(events))
}

func (h *csHarness) currentJob(t *testing.T) *csJob {
	t.Helper()
	h.p.mu.Lock()
	defer h.p.mu.Unlock()
	require.NotNil(t, h.p.job)
	return h.p.job
}

func awaitJob(t *testing.T, j *csJob) {
	t.Helper()
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled run did not end")
	}
}

// A run is single-flight: a second trigger while one is in flight starts
// nothing.
func TestContentStorageProvision_IsSingleFlight(t *testing.T) {
	h := newCSHarness(t)
	h.ecos.blockQuery = make(chan struct{})
	h.p.Retry("ns1")
	h.p.mu.Lock()
	first := h.p.job
	h.p.mu.Unlock()
	h.p.Retry("ns1")
	h.p.evaluate(false, "")
	h.p.mu.Lock()
	assert.Same(t, first, h.p.job)
	h.p.mu.Unlock()
	close(h.ecos.blockQuery)
	h.wait(t)
	assert.Equal(t, api.ContentStorageStateConnected, h.p.Status("ns1").State)
}

// A Retry aimed at a namespace that is no longer the active one does nothing.
func TestContentStorageProvision_RetryIsForTheActiveNamespaceOnly(t *testing.T) {
	h := newCSHarness(t)
	h.p.Retry("other")
	h.wait(t)
	queries, _ := h.ecos.snapshot()
	assert.Zero(t, queries)
}

// The secret is resolved through the namespace's own values before ECOS is
// asked anything: a locked vault or a reference with no value is the error
// state, and nothing is read or written.
func TestContentStorageProvision_AnUnresolvableSecretIsAnErrorWithNoWrites(t *testing.T) {
	h := newCSHarness(t)
	h.secretErr = storage.ErrSecretsLocked
	st := h.retry(t)
	assert.Equal(t, api.ContentStorageStateError, st.State)
	assert.Equal(t, "contentStorage.msg.provision.secretsLocked", st.ErrorMsg.Key)
	queries, muts := h.ecos.snapshot()
	assert.Zero(t, queries)
	assert.Empty(t, muts)

	h2 := newCSHarness(t)
	h2.secretValues = map[string]string{}
	st = h2.retry(t)
	assert.Equal(t, "contentStorage.msg.provision.secretMissing", st.ErrorMsg.Key)
	assert.Contains(t, st.ErrorMsg.Args, "seaweedfs-s3")
	assert.Empty(t, h2.ecos.mutatedIDs())

	h3 := newCSHarness(t)
	h3.secretErr = errors.New("disk on fire")
	assert.Equal(t, "contentStorage.msg.provision.secretsUnreadable", h3.retry(t).ErrorMsg.Key)
}

// The runtime hook ignores what cannot change the gate and never blocks.
func TestContentStorageProvision_OnRuntimeEvent(t *testing.T) {
	h := newCSHarness(t)
	h.p.OnRuntimeEvent(api.EventDto{Type: "app_stats", AppName: appdef.AppContent})
	h.p.OnRuntimeEvent(api.EventDto{Type: "pull_progress", AppName: appdef.AppContent})
	time.Sleep(20 * time.Millisecond)
	queries, _ := h.ecos.snapshot()
	assert.Zero(t, queries, "stats and pull progress are not status changes")

	h.p.OnRuntimeEvent(api.EventDto{Type: "app_status", AppName: appdef.AppContent, After: "RUNNING"})
	require.Eventually(t, func() bool {
		return h.p.Status("ns1").State == api.ContentStorageStateConnected
	}, 5*time.Second, time.Millisecond)
}

func TestLongOpOwnsNamespaceData(t *testing.T) {
	owns := map[longOpKind]bool{
		longOpNone: false, longOpSnapshot: true, longOpMigration: true, longOpDepsRollback: true,
		longOpUpdatePass: false, longOpRequest: false,
	}
	for kind, want := range owns {
		assert.Equal(t, want, longOpOwnsNamespaceData(kind), string(kind))
	}
}

// --- the wire ---

// ecosStub is an httptest gateway: it records each request and answers with
// the given status and body.
type ecosStub struct {
	t        *testing.T
	mu       sync.Mutex
	requests []ecosStubRequest
	answer   func(path string, body []byte) (int, string)
}

type ecosStubRequest struct {
	path, method, user, contentType string
	body                            string
}

func newECOSStub(t *testing.T, answer func(path string, body []byte) (int, string)) (stub *ecosStub, addr string) {
	t.Helper()
	stub = &ecosStub{t: t, answer: answer}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.requests = append(stub.requests, ecosStubRequest{
			path: r.URL.Path, method: r.Method, user: r.Header.Get("X-ECOS-User"),
			contentType: r.Header.Get("Content-Type"), body: string(body),
		})
		stub.mu.Unlock()
		status, resp := stub.answer(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return stub, strings.TrimPrefix(srv.URL, "http://")
}

func (s *ecosStub) all() []ecosStubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ecosStubRequest(nil), s.requests...)
}

func TestHTTPEcosRecords_Query(t *testing.T) {
	stub, addr := newECOSStub(t, func(string, []byte) (int, string) {
		return http.StatusOK, `{"records":[` +
			`{"id":"eapps/config@app/emodel$default-content-storage","attributes":{"value":{"ref":"emodel/content-storage@LOCAL","config":{}},"notExists":false}},` +
			`{"id":"content/storage@seaweedfs-s3","attributes":{"notExists":true}}` +
			`],"messages":[],"hasMore":false,"totalCount":0,"version":1}`
	})
	got, err := newHTTPEcosRecords(addr).Query(context.Background(),
		[]string{csConfigRef, "content/storage@seaweedfs-s3"},
		map[string]string{"value": "_value?json", "notExists": "_notExists?bool"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.JSONEq(t, `{"ref":"emodel/content-storage@LOCAL","config":{}}`, string(got[0]["value"]))
	assert.Equal(t, "true", string(got[1]["notExists"]))

	reqs := stub.all()
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodPost, reqs[0].method)
	assert.Equal(t, "/api/records/query", reqs[0].path, "the gateway itself, not nginx's /gateway prefix")
	assert.Equal(t, "admin", reqs[0].user)
	assert.Equal(t, "application/json", reqs[0].contentType)
	// No "version": the gateway reads an attributes query as v2 by default.
	assert.JSONEq(t, `{
		"records": ["eapps/config@app/emodel$default-content-storage", "content/storage@seaweedfs-s3"],
		"attributes": {"value": "_value?json", "notExists": "_notExists?bool"}
	}`, reqs[0].body)
}

func TestHTTPEcosRecords_Mutate(t *testing.T) {
	stub, addr := newECOSStub(t, func(string, []byte) (int, string) {
		return http.StatusOK, `{"records":[{"id":"emodel/secret@seaweedfs-s3-credentials","attributes":{}}],"messages":[],"version":1}`
	})
	id, err := newHTTPEcosRecords(addr).Mutate(context.Background(), "emodel/secret@", map[string]any{
		"id":        "seaweedfs-s3-credentials",
		"type":      "BASIC",
		"data?json": map[string]any{"username": "citeck", "password": "p"},
	})
	require.NoError(t, err)
	assert.Equal(t, "emodel/secret@seaweedfs-s3-credentials", id)
	reqs := stub.all()
	require.Len(t, reqs, 1)
	assert.Equal(t, "/api/records/mutate", reqs[0].path)
	assert.Equal(t, "admin", reqs[0].user)
	assert.JSONEq(t, `{"records":[{"id":"emodel/secret@","attributes":{
		"id":"seaweedfs-s3-credentials","type":"BASIC","data?json":{"username":"citeck","password":"p"}}}]}`, reqs[0].body)
}

// The gateway answers an ERROR message with HTTP 500 and the records error in
// msg; a failure before the records layer (the auth filter) is a Spring error
// body; a refused identity is a 4xx. Each is read, and classified.
func TestHTTPEcosRecords_Errors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantText  string
		retryable bool
	}{
		{"records ERROR", http.StatusInternalServerError,
			`{"records":[],"messages":[{"level":"ERROR","time":"2026-10-05T00:00:00Z","type":"records-error",` +
				`"msg":{"type":"IllegalStateException","msg":"Config not found by id: x","stackTrace":[]},"requestId":"","requestTrace":[]}]}`,
			"IllegalStateException: Config not found by id: x", true},
		{"text ERROR on a 200", http.StatusOK,
			`{"records":[{"id":"a@b","attributes":{}}],"messages":[{"level":"ERROR","type":"text","msg":"Records source is not found: storage"}]}`,
			"Records source is not found: storage", true},
		{"spring error", http.StatusInternalServerError,
			`{"timestamp":"x","path":"/api/records/query","status":500,"error":"Internal Server Error","message":"User 'admin' is not created yet. Please try again later"}`,
			"User 'admin' is not created yet", true},
		{"not json", http.StatusBadGateway, `upstream not ready`, "upstream not ready", true},
		{"forbidden", http.StatusForbidden, `{"error":"Forbidden"}`, "Forbidden", false},
		{"bad request", http.StatusBadRequest, `{}`, "HTTP 400", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, addr := newECOSStub(t, func(string, []byte) (int, string) { return tc.status, tc.body })
			_, err := newHTTPEcosRecords(addr).Query(context.Background(), []string{"a@b"}, map[string]string{"x": "y"})
			var rec *ecosRecordsError
			require.ErrorAs(t, err, &rec)
			assert.Equal(t, tc.status, rec.Status)
			assert.Contains(t, err.Error(), tc.wantText)
			assert.Equal(t, tc.retryable, rec.retryable())
			assert.Equal(t, tc.retryable, csRetryable(err))
		})
	}

	// WARN and INFO messages are not failures.
	_, addr := newECOSStub(t, func(string, []byte) (int, string) {
		return http.StatusOK, `{"records":[{"id":"a@b","attributes":{}}],"messages":[{"level":"WARN","type":"text","msg":"both query and records"}]}`
	})
	_, err := newHTTPEcosRecords(addr).Query(context.Background(), []string{"a@b"}, map[string]string{"x": "y"})
	require.NoError(t, err)

	// No answer at all is a transport failure, and retryable.
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	_, err = newHTTPEcosRecords(dead).Query(context.Background(), []string{"a@b"}, map[string]string{"x": "y"})
	var rec *ecosRecordsError
	require.ErrorAs(t, err, &rec)
	assert.Zero(t, rec.Status)
	assert.True(t, rec.retryable())
}

// A query that answers a different number of records than it was asked for
// is not an answer.
func TestHTTPEcosRecords_QueryCountMismatch(t *testing.T) {
	_, addr := newECOSStub(t, func(string, []byte) (int, string) {
		return http.StatusOK, `{"records":[],"messages":[]}`
	})
	_, err := newHTTPEcosRecords(addr).Query(context.Background(), []string{"a@b"}, map[string]string{"x": "y"})
	require.Error(t, err)
	assert.True(t, csRetryable(err))
}

// End to end over the real client: the provisioner against a gateway stub
// playing a fresh ECOS. Pins the exact bytes of every request a first
// connection sends, and the identity header on each.
func TestContentStorageProvision_OverHTTP(t *testing.T) {
	var mu sync.Mutex
	defaultRef := csLocalStorageRef
	created := map[string]bool{}
	stub, addr := newECOSStub(t, func(path string, body []byte) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		var req struct {
			Records json.RawMessage `json:"records"`
		}
		_ = json.Unmarshal(body, &req)
		if path == ecosRecordsMutatePath {
			var recs []struct {
				ID         string         `json:"id"`
				Attributes map[string]any `json:"attributes"`
			}
			_ = json.Unmarshal(req.Records, &recs)
			if recs[0].ID == csConfigRef {
				defaultRef = recs[0].Attributes["_value?json"].(map[string]any)["ref"].(string)
				return http.StatusOK, `{"records":[{"id":"` + csConfigRef + `","attributes":{}}]}`
			}
			full := recs[0].ID + recs[0].Attributes["id"].(string)
			created[full] = true
			return http.StatusOK, `{"records":[{"id":"` + full + `","attributes":{}}]}`
		}
		var refs []string
		_ = json.Unmarshal(req.Records, &refs)
		var out []map[string]any
		for _, ref := range refs {
			if ref == csConfigRef {
				out = append(out, map[string]any{"id": ref, "attributes": map[string]any{
					"value": map[string]any{"ref": defaultRef, "config": map[string]any{}}, "notExists": false}})
				continue
			}
			out = append(out, map[string]any{"id": ref, "attributes": map[string]any{"notExists": !created[ref]}})
		}
		b, _ := json.Marshal(map[string]any{"records": out, "messages": []any{}})
		return http.StatusOK, string(b)
	})

	h := newCSHarness(t)
	h.p.records = newHTTPEcosRecords
	h.p.gatewayAddr = func(context.Context, *namespace.Runtime) (string, error) { return addr, nil }
	assert.Equal(t, api.ContentStorageStateConnected, h.retry(t).State)

	reqs := stub.all()
	var mutations []string
	for _, r := range reqs {
		assert.Equal(t, "admin", r.user, r.path)
		assert.Equal(t, "application/json", r.contentType)
		if r.path == ecosRecordsMutatePath {
			mutations = append(mutations, r.body)
		}
	}
	require.Len(t, mutations, 4)
	assert.JSONEq(t, `{"records":[{"id":"emodel/secret@","attributes":{
		"id":"seaweedfs-s3-credentials","name":"seaweedfs S3 credentials","type":"BASIC",
		"data?json":{"username":"citeck","password":"`+csSecretValue+`"}}}]}`, mutations[0])
	assert.JSONEq(t, `{"records":[{"id":"emodel/endpoint@","attributes":{
		"id":"seaweedfs-s3","name":"seaweedfs S3","url":"http://seaweedfs:8333",
		"credentials":"emodel/secret@seaweedfs-s3-credentials"}}]}`, mutations[1])
	assert.JSONEq(t, `{"records":[{"id":"content/storage@","attributes":{
		"id":"seaweedfs-s3","name":"seaweedfs S3","type":"S3",
		"config?json":{"endpoint":"emodel/endpoint@seaweedfs-s3","bucketName":"ecos-content"}}}]}`, mutations[2])
	assert.JSONEq(t, `{"records":[{"id":"eapps/config@app/emodel$default-content-storage","attributes":{
		"_value?json":{"ref":"content/storage@seaweedfs-s3","config":{}}}}]}`, mutations[3])

	mu.Lock()
	assert.Equal(t, "content/storage@seaweedfs-s3", defaultRef)
	assert.Equal(t, map[string]bool{
		"emodel/secret@seaweedfs-s3-credentials": true,
		"emodel/endpoint@seaweedfs-s3":           true,
		"content/storage@seaweedfs-s3":           true,
	}, maps.Clone(created))
	mu.Unlock()
}

// The state reaches the namespace DTO, rendered for the reader.
func TestContentStorageProvision_StateReachesTheNamespaceDto(t *testing.T) {
	h := newCSHarness(t)
	h.secretErr = storage.ErrSecretsLocked
	h.retry(t)
	dto := h.d.contentStorageDto(englishForLogs, h.d.active())
	require.NotNil(t, dto)
	assert.Equal(t, api.ContentStorageStateError, dto.State)
	assert.NotEmpty(t, dto.Error)
	assert.NotEmpty(t, dto.At)
}
