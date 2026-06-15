package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
)

// --- Fakes ---

// execCall records one ExecInContainer invocation.
type execCall struct {
	containerID string
	cmd         []string
}

// fakeExecer records every ExecInContainer call and answers via the optional
// respond hook (default: success with empty output).
type fakeExecer struct {
	calls   []execCall
	respond func(containerID string, cmd []string) (string, int, error)
}

func (f *fakeExecer) ExecInContainer(_ context.Context, containerID string, cmd []string) (output string, exitCode int, err error) {
	f.calls = append(f.calls, execCall{containerID: containerID, cmd: cmd})
	if f.respond != nil {
		return f.respond(containerID, cmd)
	}
	return "", 0, nil
}

// classifyRotationCall maps a recorded exec command onto the documented
// rotation steps so order assertions read as the spec, not as argv plumbing.
func classifyRotationCall(c execCall) string {
	joined := strings.Join(c.cmd, " ")
	switch {
	case strings.Contains(joined, "kcadm.sh config credentials"):
		return "sa-login"
	case strings.Contains(joined, "kcadm.sh set-password") && strings.Contains(joined, "-r ecos-app"):
		return "set-password-ecos-app"
	case strings.Contains(joined, "kcadm.sh set-password") && strings.Contains(joined, "-r master"):
		return "set-password-master"
	case c.cmd[0] == "rabbitmqctl":
		return "rabbitmq-change-password"
	case strings.Contains(joined, "setup.py update-user"):
		return "pgadmin-update-user"
	default:
		return "other:" + joined
	}
}

func classifyAll(calls []execCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, classifyRotationCall(c))
	}
	return out
}

// stubAppFinder satisfies appFinder with a fixed app map.
type stubAppFinder map[string]*namespace.AppRuntime

func (s stubAppFinder) FindApp(name string) *namespace.AppRuntime { return s[name] }

// newRotationTestDaemon wires a Daemon with a real SQLite-backed SecretService
// (unlocked with the default password so the `_admin_password` persistence
// step is exercisable), the fake execer, and a stub runtime exposing the three
// rotation target containers.
func newRotationTestDaemon(t *testing.T, exec *fakeExecer) *Daemon {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	return &Daemon{
		store:         store,
		secretService: svc,
		execClient:    exec,
		rotationApps: stubAppFinder{
			appdef.AppKeycloak: {Name: appdef.AppKeycloak, ContainerID: "kc-1"},
			appdef.AppRabbitmq: {Name: appdef.AppRabbitmq, ContainerID: "rmq-1"},
			appdef.AppPgadmin:  {Name: appdef.AppPgadmin, ContainerID: "pg-1"},
		},
		activeNs: &activeNamespace{systemSecrets: namespace.SystemSecrets{CiteckSA: "sa-pass"}},
	}
}

func postAdminPassword(d *Daemon, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/namespace/admin-password", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSetAdminPassword(rec, req)
	return rec
}

// --- Tests ---

// TestHandleSetAdminPassword_HappyPathCallOrder asserts the documented
// rotation sequence: citeck-SA usability check (kcadm login) → kcadm
// set-password for ecos-app → master → rabbitmqctl → pgadmin, and that the
// new password is persisted to the `_admin_password` system secret plus the
// in-memory systemSecrets copy.
func TestHandleSetAdminPassword_HappyPathCallOrder(t *testing.T) {
	exec := &fakeExecer{}
	d := newRotationTestDaemon(t, exec)

	rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	require.Equal(t, []string{
		"sa-login",
		"set-password-ecos-app",
		"set-password-master",
		"rabbitmq-change-password",
		"pgadmin-update-user",
	}, classifyAll(exec.calls))

	// Each step must hit its own container.
	assert.Equal(t, "kc-1", exec.calls[0].containerID)
	assert.Equal(t, "kc-1", exec.calls[1].containerID)
	assert.Equal(t, "kc-1", exec.calls[2].containerID)
	assert.Equal(t, "rmq-1", exec.calls[3].containerID)
	assert.Equal(t, "pg-1", exec.calls[4].containerID)

	// SA usability check authenticates as the citeck SA, not the human admin.
	assert.Contains(t, exec.calls[0].cmd, namespace.CiteckSAUser)
	assert.Contains(t, exec.calls[0].cmd, "sa-pass")

	// Persistence: the plain launcher_state key (the priority-1 source
	// systemsecrets.Get reads on restart) and the in-memory copy carry
	// the rotation. A SecretService row must NOT be (re)created — the stale
	// state value used to win over it on restart, silently reverting the
	// rotation via the Keycloak init script.
	v, err := d.store.GetStateValue(systemsecrets.Key(systemsecrets.IDAdminPassword))
	require.NoError(t, err)
	assert.Equal(t, "new-secret-1", v)
	_, err = d.secretService.GetSecret("_admin_password")
	require.Error(t, err, "rotation must not persist via the SecretService anymore")
	assert.Equal(t, "new-secret-1", d.active().systemSecrets.AdminPassword)
}

// TestHandleSetAdminPassword_SurvivesResolveSystemSecrets is the restart
// regression for the rotation-persistence bug: after a rotation,
// systemsecrets.Get (what resolveSystemSecrets runs at daemon startup)
// must return the ROTATED password — not a stale value. Historically the
// handler saved the new password into the SecretService while the
// priority-1 launcher_state plain key kept the OLD one, so every restart
// silently reverted the rotation.
func TestHandleSetAdminPassword_SurvivesResolveSystemSecrets(t *testing.T) {
	exec := &fakeExecer{}
	d := newRotationTestDaemon(t, exec)

	// Simulate a pre-existing install: the old password already lives in the
	// plain launcher_state slot (what resolveSystemSecrets wrote at startup),
	// and a legacy SecretService row lingers from an older launcher version.
	require.NoError(t, d.store.SetStateValue(systemsecrets.Key(systemsecrets.IDAdminPassword), "old-password"))
	require.NoError(t, d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "_admin_password", Name: "_admin_password", Type: storage.SecretSystem},
		Value:      "old-password",
	}))

	rec := postAdminPassword(d, `{"password":"rotated-pw-9"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	// What the next daemon start resolves must be the rotated value.
	got, err := systemsecrets.Get(d.store, d.secretService, systemsecrets.IDAdminPassword, func() string {
		t.Fatal("generate must not run — the rotated value must already be persisted")
		return ""
	})
	require.NoError(t, err)
	assert.Equal(t, "rotated-pw-9", got,
		"daemon restart must resolve the ROTATED admin password, not the stale pre-rotation value")

	// The legacy SecretService row is deleted so the priority-2 fallback can
	// never resurrect the old password.
	_, err = d.secretService.GetSecret("_admin_password")
	assert.Error(t, err, "legacy SecretService _admin_password row must be deleted on rotation")
}

// TestHandleSetAdminPassword_EcosAppFailureIsFatal: a failed ecos-app
// set-password aborts the rotation with a 500 BEFORE master / rabbitmq /
// pgadmin are touched — without ecos-app the user would be locked out of the
// platform, so nothing downstream may proceed.
func TestHandleSetAdminPassword_EcosAppFailureIsFatal(t *testing.T) {
	exec := &fakeExecer{
		respond: func(_ string, cmd []string) (string, int, error) {
			if classifyRotationCall(execCall{cmd: cmd}) == "set-password-ecos-app" {
				return "boom", 1, nil
			}
			return "", 0, nil
		},
	}
	d := newRotationTestDaemon(t, exec)

	rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	require.Equal(t, []string{"sa-login", "set-password-ecos-app"}, classifyAll(exec.calls),
		"rotation must stop at the ecos-app failure — no master/rabbitmq/pgadmin calls")

	// The persisted state must NOT record the failed rotation.
	v, err := d.store.GetStateValue(systemsecrets.Key(systemsecrets.IDAdminPassword))
	if err == nil {
		assert.Empty(t, v, "no _sys_admin_password state should be persisted on a fatal ecos-app failure")
	}
}

// TestHandleSetAdminPassword_MasterFailureAbortsWithRetryHint: since the
// 2.1.0 SA split the master realm rotation is ALSO fatal (a stale master
// console password is a live security hole), but it fails with a dedicated
// message carrying the partial-rotation state and the
// `citeck setup admin-password` retry hint — unlike ecos-app's generic 500.
// RabbitMQ / PgAdmin must not be touched after the abort.
func TestHandleSetAdminPassword_MasterFailureAbortsWithRetryHint(t *testing.T) {
	exec := &fakeExecer{
		respond: func(_ string, cmd []string) (string, int, error) {
			if classifyRotationCall(execCall{cmd: cmd}) == "set-password-master" {
				return "kc down", 1, nil
			}
			return "", 0, nil
		},
	}
	d := newRotationTestDaemon(t, exec)

	rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "master realm admin password rotation failed",
		"master failure must surface its dedicated message, not the generic internal error")
	assert.Contains(t, rec.Body.String(), "citeck setup admin-password",
		"master failure must carry the retry guidance")

	require.Equal(t, []string{"sa-login", "set-password-ecos-app", "set-password-master"},
		classifyAll(exec.calls), "rabbitmq/pgadmin must not run after the master abort")
}

// TestHandleSetAdminPassword_RabbitFailureIsBestEffort: rabbitmqctl failures
// are logged and skipped — PgAdmin still rotates and the request succeeds
// (the password applies to RabbitMQ on the next container recreate via env).
func TestHandleSetAdminPassword_RabbitFailureIsBestEffort(t *testing.T) {
	exec := &fakeExecer{
		respond: func(_ string, cmd []string) (string, int, error) {
			if classifyRotationCall(execCall{cmd: cmd}) == "rabbitmq-change-password" {
				return "", 0, context.DeadlineExceeded
			}
			return "", 0, nil
		},
	}
	d := newRotationTestDaemon(t, exec)

	rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, []string{
		"sa-login",
		"set-password-ecos-app",
		"set-password-master",
		"rabbitmq-change-password",
		"pgadmin-update-user",
	}, classifyAll(exec.calls), "pgadmin must still rotate after a rabbitmq failure")
}

// TestHandleSetAdminPassword_SALoginFallsBackToBootstrapRecovery: when the SA
// usability check fails, the handler must attempt `kc.sh bootstrap-admin user`
// recovery before giving up (lost-password / snapshot-mismatch path). Here the
// bootstrap also fails, so the request ends 500 — the assertion is on the
// attempted sequence.
func TestHandleSetAdminPassword_SALoginFallsBackToBootstrapRecovery(t *testing.T) {
	exec := &fakeExecer{
		respond: func(_ string, cmd []string) (string, int, error) {
			joined := strings.Join(cmd, " ")
			if strings.Contains(joined, "kcadm.sh config credentials") {
				return "invalid credentials", 1, nil
			}
			if strings.Contains(joined, "bootstrap-admin user") {
				return "db locked", 1, nil
			}
			return "", 0, nil
		},
	}
	d := newRotationTestDaemon(t, exec)

	rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	require.Len(t, exec.calls, 2)
	assert.Equal(t, "sa-login", classifyRotationCall(exec.calls[0]))
	assert.Contains(t, strings.Join(exec.calls[1].cmd, " "), "bootstrap-admin user",
		"failed SA login must trigger the bootstrap-admin recovery attempt")
}

// TestHandleSetAdminPassword_Validation covers the request-shape gates: short
// password, no runtime, and a missing keycloak container.
func TestHandleSetAdminPassword_Validation(t *testing.T) {
	t.Run("short password", func(t *testing.T) {
		exec := &fakeExecer{}
		d := newRotationTestDaemon(t, exec)
		rec := postAdminPassword(d, `{"password":"abc"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Empty(t, exec.calls)
	})
	t.Run("no namespace runtime", func(t *testing.T) {
		exec := &fakeExecer{}
		d := newRotationTestDaemon(t, exec)
		d.rotationApps = nil // and d.runtime is nil → NOT_CONFIGURED
		rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "no namespace configured")
	})
	t.Run("keycloak not running", func(t *testing.T) {
		exec := &fakeExecer{}
		d := newRotationTestDaemon(t, exec)
		d.rotationApps = stubAppFinder{} // no keycloak entry
		rec := postAdminPassword(d, `{"password":"new-secret-1"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "keycloak container is not running")
		assert.Empty(t, exec.calls)
	})
}
