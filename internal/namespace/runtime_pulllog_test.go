package namespace

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// TestHandlePullResult_LogsCauseDeduped verifies the pull-failure logging gap is
// closed: the cause is WARN-logged (parity with start/probe/init), and the log
// is deduplicated per app+error so 24 apps × T24 backoff retries don't flood
// daemon.log — re-logging only when the error text changes, and again after a
// recovery.
func TestHandlePullResult_LogsCauseDeduped(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	r := NewRuntime(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()

	const app = "emodel"
	setPulling := func() {
		r.mu.Lock()
		r.apps[app] = &AppRuntime{
			Name:   app,
			Status: AppStatusPulling,
			Def:    appdef.ApplicationDef{Name: app, Image: "registry.example/emodel:1.0"},
		}
		r.mu.Unlock()
	}
	failWith := func(msg string) {
		setPulling()
		r.handlePullResult(workers.Result{TaskID: workers.TaskID{App: app, Op: workers.OpPull}, Err: errors.New(msg)})
	}
	count := func(sub string) int { return strings.Count(buf.String(), sub) }

	// First failure → logged once with the cause and the image.
	failWith("connection refused 127.0.0.1:10801")
	assert.Equal(t, 1, count("Image pull failed"), "first pull failure must be logged")
	assert.Contains(t, buf.String(), "registry.example/emodel:1.0")
	assert.Contains(t, buf.String(), "connection refused 127.0.0.1:10801")

	// Same error again (a T24 retry) → deduped, still one line.
	failWith("connection refused 127.0.0.1:10801")
	assert.Equal(t, 1, count("Image pull failed"), "identical pull error must be deduped")

	// Different error → re-logged.
	failWith("manifest unknown")
	assert.Equal(t, 2, count("Image pull failed"), "changed pull error must re-log")

	// Recovery clears the dedup so a later failure logs again.
	setPulling()
	r.handlePullResult(workers.Result{TaskID: workers.TaskID{App: app, Op: workers.OpPull}, Payload: workers.PullPayload{Digest: "sha256:abc"}})
	require.Equal(t, AppStatusReadyToStart, r.FindApp(app).Status)
	failWith("manifest unknown")
	assert.Equal(t, 3, count("Image pull failed"), "failure after recovery must log again")
}
