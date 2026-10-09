package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The connection uses ecos-content's native S3 storage. Endpoint and secrets
// are supplied through the content container's environment, never Records API.
type csRecordIDs struct{ storage string }

func csNativeRecordIDs() csRecordIDs {
	return csRecordIDs{storage: "content/storage@content-storage-s3"}
}

// csCredentials redacts secrets from platform errors; credentials are never sent via Records API.
type csCredentials struct {
	secretKey string
}

// redact hides the secret key in a text that is about to be logged or shown.
// An answer from ECOS has no business echoing it, but nothing guarantees it.
func (c csCredentials) redact(s string) string {
	if c.secretKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.secretKey, "***")
}

// csConnectionState supplies only the persisted decision inputs.
type csConnectionState interface {
	ContentStorageSwitch() string
	ContentStorageDefaultSet() bool
}

// csConnectionRunner is one attempt. It has no daemon, scheduler or job pointer.
// Its immutable inputs and callbacks are captured before starting; the scheduler
// owns cancellation, current-job checks, status and all persistence.
type csConnectionRunner struct {
	nsID                       string
	creds                      csCredentials
	state                      csConnectionState
	probe                      func(context.Context, csRecordIDs) (ecosRecords, csSnapshot, error)
	now                        func() time.Time
	pollInterval, readyTimeout time.Duration
	mayWrite                   func(context.Context) bool
	recordSwitch               func(context.Context, string) bool
	recordVerdict              func(context.Context, bool) bool
	provisioning               func()
	settle                     func(string, string)
	fail                       func(msg.Message)
	acquireMutation            func() (func(), bool)
}

func (r *csConnectionRunner) run(ctx context.Context) {
	ids := csNativeRecordIDs()
	deadline := r.now().Add(r.readyTimeout)
	for {
		client, snap, ok := r.awaitReady(ctx, ids, r.creds, deadline)
		if !ok {
			return
		}
		step, done := r.pass(ctx, client, snap, ids, r.creds)
		if done || ctx.Err() != nil || step == csPostponedStep {
			return
		}
		if !step.retryable || !r.now().Before(deadline) {
			r.fail(step.msg)
			return
		}
		slog.Info("An S3 content storage step did not go through yet; asking ECOS again",
			"ns", r.nsID, "reason", englishForLogs.Render(step.msg))
		if !r.pause(ctx) {
			return
		}
	}
}

// pass decides on one reading of ECOS and acts on it. done is true when the
// run is over — a verdict recorded, or the run canceled or postponed, each
// already said — and step is what failed otherwise, for the caller to retry
// or report.
func (r *csConnectionRunner) pass(ctx context.Context, client ecosRecords, snap csSnapshot, ids csRecordIDs, creds csCredentials) (step *csStepError, done bool) {
	pending := r.state.ContentStorageSwitch() == ids.storage
	if state, settled := csDecide(snap, ids, pending, r.state.ContentStorageDefaultSet()); settled {
		// A verdict from ECOS ends whatever switch was pending: ours is the
		// default now, or an administrator chose another storage. A run that
		// is no longer current says and writes nothing more.
		if !r.recordVerdict(ctx, state == api.ContentStorageStateConnected) {
			return nil, true
		}
		r.settle(state, snap.defaultRef)
		return nil, true
	}
	if !r.mayWrite(ctx) {
		return nil, true
	}
	// Recorded BEFORE the first write, so a failure or a daemon death anywhere
	// between writing the default and confirming it is finished by
	// the next run instead of read as an administrator's choice. A run that is
	// no longer current writes nothing at all.
	if !r.recordSwitch(ctx, ids.storage) {
		return nil, true
	}
	r.provisioning()
	actual, failed := r.provision(ctx, client, ids, creds)
	if failed != nil {
		return failed, false
	}
	connected := actual == ids.storage
	if !r.recordVerdict(ctx, connected) {
		return nil, true
	}
	state := api.ContentStorageStateAdminManaged
	if connected {
		state = api.ContentStorageStateConnected
	}
	r.settle(state, actual)
	return nil, true
}

// pause waits one poll interval; false when the run was canceled meanwhile.
func (r *csConnectionRunner) pause(ctx context.Context) bool {
	timer := time.NewTimer(r.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// csDecide is the verdict ECOS's own records already give, before anything is
// written. settled is false only when the launcher should switch the default. switchPending is whether this launcher's
// own switch to our storage was started and never confirmed.
func csDecide(snap csSnapshot, ids csRecordIDs, switchPending, defaultSet bool) (state string, settled bool) {
	if snap.defaultRef != "" && snap.defaultRef != csLocalStorageRef {
		if snap.defaultRef == ids.storage {
			return api.ContentStorageStateConnected, true
		}
		return api.ContentStorageStateAdminManaged, true
	}
	if defaultSet && !switchPending {
		// Native S3 was previously confirmed and the default is LOCAL; no pending
		// switch accounts for it: somebody switched back.
		return api.ContentStorageStateAdminManaged, true
	}
	return "", false
}

// awaitReady polls until ECOS can answer the decision, the platform goes away
// (abort), the answer is one that waiting will not change (error), or the
// readiness budget runs out (error). ok is true with a snapshot to decide on.
func (r *csConnectionRunner) awaitReady(ctx context.Context, ids csRecordIDs, creds csCredentials, deadline time.Time) (ecosRecords, csSnapshot, bool) {
	for {
		if ctx.Err() != nil {
			return nil, csSnapshot{}, false
		}
		if !r.mayWrite(ctx) {
			return nil, csSnapshot{}, false
		}
		client, snap, err := r.probe(ctx, ids)
		if err == nil {
			return client, snap, true
		}
		if ctx.Err() != nil {
			return nil, csSnapshot{}, false
		}
		if !csRetryable(err) {
			r.fail(msg.New("contentStorage.msg.provision.readFailed", "error", creds.redact(err.Error())))
			return nil, csSnapshot{}, false
		}
		if !r.now().Before(deadline) {
			r.fail(msg.New("contentStorage.msg.provision.notReady",
				"minutes", strconv.Itoa(int(r.readyTimeout/time.Minute)), "error", creds.redact(err.Error())))
			return nil, csSnapshot{}, false
		}
		slog.Debug("ECOS is not ready for the S3 content storage connection yet", "ns", r.nsID, "err", err)
		if !r.pause(ctx) {
			return nil, csSnapshot{}, false
		}
	}
}

// csStepError is why provision stopped: the operator's sentence, and whether
// the same run may go back to reading ECOS and try again.
type csStepError struct {
	msg       msg.Message
	retryable bool
}

// stepFailed builds a csStepError from a records call that failed.
func stepFailed(err error, m msg.Message) *csStepError {
	if errors.Is(err, errCSPostponed) {
		return csPostponedStep
	}
	return &csStepError{msg: m, retryable: csStepRetryable(err)}
}

// csStepRetryable distinguishes a temporary transport/startup failure from an
// ordinary refusal. Refusals become error and retry on the five-minute timer.
// Before every retry ECOS config is read again, including lost write replies.
func csStepRetryable(err error) bool {
	var rec *ecosRecordsError
	if !errors.As(err, &rec) {
		return false
	}
	switch rec.Status {
	case 0, http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return slices.ContainsFunc(rec.Messages, csSourceNotFound)
}

// csSourceNotFound reports whether a records message says the record source
// is not registered (records' RecordsSourceNotFoundException, "Source is not
// found with id …", and the older "Records source is not found: …").
func csSourceNotFound(message string) bool {
	return strings.Contains(strings.ToLower(message), "source is not found")
}

// provision re-reads config immediately before the only mutation and again
// after it. A newly selected administrator default is never overwritten.
func (r *csConnectionRunner) provision(ctx context.Context, client ecosRecords, ids csRecordIDs, creds csCredentials) (string, *csStepError) {
	release, ok := r.acquireMutation()
	if !ok {
		return "", csPostponedStep
	}
	defer release()
	if !r.mayWrite(ctx) {
		return "", csPostponedStep
	}
	before, err := csReadDefault(ctx, client)
	if err != nil {
		return "", csReadStep(err, creds)
	}
	if !r.mayWrite(ctx) {
		return "", csPostponedStep
	}
	pending := r.state.ContentStorageSwitch() == ids.storage
	if _, settled := csDecide(csSnapshot{defaultRef: before}, ids, pending, r.state.ContentStorageDefaultSet()); settled {
		return before, nil
	}
	_, err = client.Mutate(ctx, csConfigRef, map[string]any{
		"_value?json": map[string]any{"ref": ids.storage, "config": map[string]any{}},
	})
	if err != nil {
		return "", stepFailed(err, msg.New("contentStorage.msg.provision.setDefaultFailed", "storage", ids.storage, "error", creds.redact(err.Error())))
	}
	after, err := csReadDefault(ctx, client)
	if err != nil {
		return "", csReadStep(err, creds)
	}
	if after != ids.storage {
		actual := after
		if actual == "" {
			actual = "—"
		}
		return "", &csStepError{msg: msg.New("contentStorage.msg.provision.verifyFailed", "storage", ids.storage, "actual", actual)}
	}
	slog.Info("Switched the default content storage", "ns", r.nsID, "storage", ids.storage)
	return after, nil
}

func csReadStep(err error, creds csCredentials) *csStepError {
	if errors.Is(err, errCSPostponed) {
		return csPostponedStep
	}
	return &csStepError{msg: msg.New("contentStorage.msg.provision.readFailed", "error", creds.redact(err.Error())), retryable: csRetryable(err)}
}

// csSnapshot is what ECOS says about the connection.
type csSnapshot struct {
	// defaultRef is the default content storage, "" when it names none.
	defaultRef    string
	storageExists bool
}

// csNotReadyError is an answer that says ECOS is not done starting: a
// default-storage config with no value yet. Asking again later is the answer
// to it.
type csNotReadyError struct{ reason string }

func (e *csNotReadyError) Error() string { return e.reason }

// csRetryable reports whether the readiness poll should ask again.
func csRetryable(err error) bool {
	var notReady *csNotReadyError
	var rec *ecosRecordsError
	switch {
	case errors.As(err, &notReady):
		return true
	case errors.As(err, &rec):
		return rec.retryable()
	case errors.Is(err, namespace.ErrAppAddressUnknown), errors.Is(err, namespace.ErrAppNotRunning):
		// A gateway being recreated; platformStillUp ends the wait if it is
		// really gone.
		return true
	default:
		return false
	}
}

// csReadDefault requires a deployed config with a string ref. Unknown values
// are errors, never permission to overwrite a configuration.
func csReadDefault(ctx context.Context, client ecosRecords) (string, error) {
	cfg, err := client.Query(ctx, []string{csConfigRef}, map[string]string{"value": "_value?json"})
	if err != nil {
		return "", fmt.Errorf("read the default content storage: %w", err)
	}
	raw := cfg[0]["value"]
	if jsonNull(raw) {
		return "", &csNotReadyError{reason: csConfigRef + " has no value yet (not deployed yet?)"}
	}
	defaultRef, ok := csDefaultRef(raw)
	if !ok {
		// A value this launcher does not understand is not one it may replace.
		return "", fmt.Errorf("unexpected value of %s: %s", csConfigRef, truncateText(string(raw), 200))
	}

	return defaultRef, nil
}

func csReadSnapshot(ctx context.Context, client ecosRecords, ids csRecordIDs) (csSnapshot, error) {
	defaultRef, err := csReadDefault(ctx, client)
	if err != nil {
		return csSnapshot{}, err
	}
	recs, err := client.Query(ctx, []string{ids.storage}, map[string]string{"notExists": "_notExists?bool"})
	if err != nil {
		return csSnapshot{}, fmt.Errorf("read the native S3 storage: %w", err)
	}
	missing, answered := jsonBool(recs[0]["notExists"])
	if answered && missing {
		return csSnapshot{}, &csNotReadyError{reason: ids.storage + " is not deployed yet"}
	}
	return csSnapshot{defaultRef: defaultRef, storageExists: true}, nil
}

// csDefaultRef reads the storage ref out of the default-storage config value:
// a JSON object whose `ref` is a string ("" means nothing chosen). ok is false
// for any other shape — an array, a scalar, an object without `ref`, a `ref`
// that is null or not a string.
func csDefaultRef(raw json.RawMessage) (ref string, ok bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return "", false
	}
	refRaw, present := obj["ref"]
	if !present || !strings.HasPrefix(strings.TrimSpace(string(refRaw)), `"`) {
		return "", false
	}
	if json.Unmarshal(refRaw, &ref) != nil {
		return "", false
	}
	return strings.TrimSpace(ref), true
}

// jsonBool reads a JSON boolean; ok is false for anything else, null included.
func jsonBool(raw json.RawMessage) (value, ok bool) {
	var b *bool
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil || b == nil {
		return false, false
	}
	return *b, true
}

// jsonNull reports whether a raw JSON value is absent or null.
func jsonNull(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// A postponed mutation is neither a remote failure nor a settled verdict.
// abort re-arms the latch, so the next platform event resumes the job.
var errCSPostponed = errors.New("content-storage mutation postponed")
var csPostponedStep = &csStepError{}
