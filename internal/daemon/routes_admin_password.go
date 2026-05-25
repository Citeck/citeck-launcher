package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// handleSetAdminPassword rotates the human-administrator password across
// every admin-facing UI on the platform: the Keycloak `master` realm admin
// (Keycloak admin console), the Keycloak `ecos-app` realm admin (platform
// login), the RabbitMQ management admin, and the PgAdmin admin. It drives
// kcadm.sh / rabbitmqctl / setup.py inside the running containers and then
// persists the new value to the `_admin_password` system secret so
// in-memory state and future daemon restarts stay consistent.
//
// The `citeck` service account password is deliberately NOT rotated here —
// it is generated once and kept stable so the launcher retains access to
// Keycloak (master realm admin role) and RabbitMQ (monitoring) regardless
// of what the user does with the human admin password.
//
// Socket-only is NOT required: the handler goes through the normal mux,
// so CSRF (localhost TCP) and mTLS (remote) protections apply.
func (d *Daemon) handleSetAdminPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "password too short (min 6 characters)")
		return
	}

	d.configMu.RLock()
	runtime := d.runtime
	d.configMu.RUnlock()
	if runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}

	kcApp := runtime.FindApp(appdef.AppKeycloak)
	if kcApp == nil || kcApp.ContainerID == "" {
		writeError(w, http.StatusBadRequest, "keycloak container is not running")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Phase 1: ecos-app realm — the user-facing platform login. Fatal on
	// failure: without it the user would be locked out of the platform.
	if err := d.resetKeycloakAdminPassword(ctx, kcApp.ContainerID, req.Password); err != nil {
		writeInternalError(w, err)
		return
	}

	// Phase 2: master realm admin — Keycloak admin console login. Also
	// fatal: leaving the old password (especially the snapshot default
	// "admin") accessible is a live security hole on any publicly-reachable
	// install, and we already succeeded on ecos-app so there's no partial
	// rollback to worry about — user just needs to retry after addressing
	// whatever tripped kcadm here. The launcher's own master-realm ops go
	// through the `citeck` SA, so launcher functionality is NOT what we're
	// protecting with this fatal-ness; it's the admin-console endpoint.
	//
	// Use writeError (not writeInternalError) so the CLI user actually sees
	// the retry guidance — writeInternalError would flatten everything to
	// "internal error" and hide both the partial-rotation state and the
	// `citeck setup admin-password` retry hint.
	if err := d.kcadmSetPassword(ctx, kcApp.ContainerID, "master", req.Password); err != nil {
		slog.Error("master realm admin password rotation failed", "err", err)
		writeError(w, http.StatusInternalServerError,
			"master realm admin password rotation failed (ecos-app already rotated — "+
				"master console may still accept the old password; "+
				"retry `citeck setup admin-password`): "+err.Error())
		return
	}

	// Change RabbitMQ password at runtime via rabbitmqctl. The env var
	// (RABBITMQ_DEFAULT_PASS) only applies on first start with an empty
	// volume — for an existing RabbitMQ instance the password lives in
	// Mnesia and must be changed via rabbitmqctl.
	rmqApp := runtime.FindApp(appdef.AppRabbitmq)
	if rmqApp != nil && rmqApp.ContainerID != "" {
		rmqCmd := []string{"rabbitmqctl", "change_password", "admin", req.Password}
		if out, exitCode, execErr := d.dockerClient.ExecInContainer(ctx, rmqApp.ContainerID, rmqCmd); execErr != nil || exitCode != 0 {
			slog.Warn("RabbitMQ password change failed (will be updated on next container recreate)",
				"err", execErr, "exitCode", exitCode, "output", out)
		}
	}

	// Change PgAdmin password at runtime via setup.py update-user. The env
	// var (PGADMIN_DEFAULT_PASSWORD) only applies on first start — for an
	// existing instance the credential is in PgAdmin's internal SQLite DB.
	pgApp := runtime.FindApp(appdef.AppPgadmin)
	if pgApp != nil && pgApp.ContainerID != "" {
		pgCmd := []string{"/venv/bin/python3", "/pgadmin4/setup.py", "update-user",
			"admin@admin.com", "--password", req.Password, "--no-console"}
		if out, exitCode, execErr := d.dockerClient.ExecInContainer(ctx, pgApp.ContainerID, pgCmd); execErr != nil || exitCode != 0 {
			slog.Warn("PgAdmin password change failed (will be updated on next container recreate)",
				"err", execErr, "exitCode", exitCode, "output", out)
		}
	}

	// Persist the new password to the system secret store so on daemon
	// restart resolveOneSystemSecret reads the updated value, and update
	// the in-memory copy so subsequent kcadm.sh invocations in this same
	// daemon lifetime (e.g. a second `citeck setup admin-password`) can
	// authenticate with the new password.
	if err := d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "_admin_password", Name: "_admin_password", Type: storage.SecretSystem},
		Value:      req.Password,
	}); err != nil {
		slog.Warn("Admin password reset OK in keycloak, but failed to persist to secret store", "err", err)
	}
	d.configMu.Lock()
	d.systemSecrets.AdminPassword = req.Password
	d.configMu.Unlock()

	slog.Info("Admin password reset (keycloak master + ecos-app, rabbitmq, pgadmin)")
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Admin password reset"})

	// Keycloak, RabbitMQ, and PgAdmin were updated at runtime above — their
	// containers don't need a restart. Webapps connect to RabbitMQ as the
	// stable "citeck" SA (not "admin"), so the admin-password change does
	// not touch ECOS_WEBAPP_RABBITMQ_PASSWORD and webapps do NOT need a
	// reload here. See addWebappInfraEnv in internal/namespace/generator.go.
}

// resetKeycloakAdminPassword authenticates kcadm.sh using the "citeck"
// service account in the master realm and resets the admin user's password
// in the ecos-app realm (the user-facing platform login).
//
// The master realm admin password is rotated by the caller in a separate
// phase — see handleSetAdminPassword. Both phases are now fatal on
// failure: partial rotation (ecos-app changed but master still on old /
// default "admin") is a live security hole, not a non-event. Splitting
// the two lets the caller craft a specific error message pointing the
// user at `citeck setup admin-password` for retry.
func (d *Daemon) resetKeycloakAdminPassword(ctx context.Context, containerID, newPassword string) error {
	d.configMu.RLock()
	saPassword := d.systemSecrets.CiteckSA
	d.configMu.RUnlock()

	if saPassword == "" {
		return fmt.Errorf("citeck SA not configured — restart namespace to run keycloak init")
	}
	if err := d.ensureCiteckSaUsable(ctx, containerID, saPassword); err != nil {
		return fmt.Errorf("ensure citeck SA usable: %w", err)
	}

	// Reset the ecos-app realm admin password only (user-facing)
	if err := d.kcadmSetPassword(ctx, containerID, "ecos-app", newPassword); err != nil {
		return err
	}
	return nil
}

// ensureCiteckSaUsable logs kcadm in as the "citeck" service account. If
// the SA password stored in the launcher is out of sync with what Keycloak
// has (snapshot import with mismatched SA, manual DB edits, lost
// passwords), the standard login fails — in that case we fall back to
// `kc.sh bootstrap-admin user` which creates a temporary master-realm
// admin by writing directly to the DB, requiring no existing credentials.
// We then use that temporary admin to reset the SA password to the stored
// value and re-login as SA. The recovery user is deleted before return.
//
// On exit, kcadm is configured with citeck SA credentials regardless of
// which path was taken — callers can issue further kcadm commands as-is.
func (d *Daemon) ensureCiteckSaUsable(ctx context.Context, containerID, saPassword string) error {
	// Steady-state path: SA login succeeds, nothing else to do.
	if err := d.kcadmLogin(ctx, containerID, namespace.CiteckSAUser, saPassword); err == nil {
		return nil
	}
	slog.Warn("citeck SA login failed, attempting bootstrap-admin recovery", "user", namespace.CiteckSAUser)

	recoveryUser, recoveryPass, err := d.bootstrapKeycloakRecovery(ctx, containerID)
	if err != nil {
		return fmt.Errorf("bootstrap-admin recovery: %w (SA may be out of sync; "+
			"if this persists, restore from snapshot)", err)
	}
	defer func() {
		// Best-effort cleanup — recovery user auto-expires after ~120 min
		// anyway, so a delete failure here is non-fatal.
		if err := d.kcadmDeleteUser(ctx, containerID, "master", recoveryUser); err != nil {
			slog.Warn("Failed to delete bootstrap recovery user (will auto-expire)",
				"user", recoveryUser, "err", err)
		}
	}()

	if err := d.kcadmLogin(ctx, containerID, recoveryUser, recoveryPass); err != nil {
		return fmt.Errorf("kcadm login as recovery user %s: %w", recoveryUser, err)
	}

	// Re-sync SA: ensure the master-realm "citeck" user exists, has admin
	// role, and uses the launcher's stored password.
	if err := d.kcadmEnsureUser(ctx, containerID, "master", namespace.CiteckSAUser); err != nil {
		return fmt.Errorf("ensure SA user exists: %w", err)
	}
	d.kcadmAddRole(ctx, containerID, "master", namespace.CiteckSAUser, "admin")
	// kcadmSetPassword (in this file) is hard-coded to user "admin" — we
	// need a parameterised variant for the SA username.
	if err := d.kcadmSetUserPassword(ctx, containerID, "master", namespace.CiteckSAUser, saPassword); err != nil {
		return fmt.Errorf("set SA password: %w", err)
	}

	// Re-login as SA so callers continue with the standard identity.
	if err := d.kcadmLogin(ctx, containerID, namespace.CiteckSAUser, saPassword); err != nil {
		return fmt.Errorf("re-login as citeck SA after recovery: %w", err)
	}
	slog.Info("Bootstrap-admin recovery succeeded — citeck SA restored",
		"user", namespace.CiteckSAUser)
	return nil
}

// bootstrapKeycloakRecovery invokes `kc.sh bootstrap-admin user` inside the
// keycloak container to create a temporary master-realm admin via direct DB
// write. Returns the generated username and password — they are NEVER
// persisted; the caller is responsible for deleting the user when done.
//
// The recovery user auto-expires after ~120 min (Keycloak's built-in TTL)
// so even if the delete is skipped or fails, the credential is short-lived.
//
// Why `--http-management-port=0`: the running keycloak server already binds
// the management port (default 9000); bootstrap-admin starts its own
// Quarkus process that would otherwise fail with "Address already in use".
//
// Why `--password:env`: prevents the random recovery password from leaking
// into `ps`/`docker top` argv lists.
func (d *Daemon) bootstrapKeycloakRecovery(ctx context.Context, containerID string) (recoveryUser, recoveryPass string, err error) {
	recoveryUser = fmt.Sprintf("_citeck_recover_%d", time.Now().UnixNano())
	recoveryPass, err = randomRecoveryPassword()
	if err != nil {
		return "", "", fmt.Errorf("generate recovery password: %w", err)
	}

	dbURL := namespace.KeycloakDBJDBCURL()
	dbUser := namespace.KeycloakDBName
	dbPass := namespace.KeycloakDBName

	cmd := []string{
		"sh", "-c",
		"RECOVERY_PASS_ENV=\"$1\" exec /opt/keycloak/bin/kc.sh bootstrap-admin user " +
			"--optimized " +
			"--db-url=\"$2\" --db-username=\"$3\" --db-password=\"$4\" " +
			"--http-management-port=0 " +
			"--username \"$5\" --password:env RECOVERY_PASS_ENV --no-prompt",
		"sh", recoveryPass, dbURL, dbUser, dbPass, recoveryUser,
	}
	out, exitCode, execErr := d.dockerClient.ExecInContainer(ctx, containerID, cmd)
	if execErr != nil {
		return "", "", fmt.Errorf("exec kc.sh bootstrap-admin: %w (output: %s)", execErr, out)
	}
	if exitCode != 0 {
		return "", "", fmt.Errorf("kc.sh bootstrap-admin exited %d: %s", exitCode, out)
	}
	return recoveryUser, recoveryPass, nil
}

// randomRecoveryPassword returns a 32-character URL-safe base64 string
// (~192 bits of entropy). The password is never persisted — only used to
// authenticate the one-shot recovery kcadm login.
func randomRecoveryPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// kcadmEnsureUser creates the given user in the realm if absent. Idempotent.
func (d *Daemon) kcadmEnsureUser(ctx context.Context, containerID, realm, username string) error {
	// Exact-match check via grep -Fxq on the username column (same approach
	// as init.sh — `-q username=` is a substring query in kcadm).
	checkCmd := []string{
		"sh", "-c",
		"/opt/keycloak/bin/kcadm.sh get users -r \"$1\" -q username=\"$2\" " +
			"--fields id,username --format csv --noquotes 2>/dev/null | " +
			"cut -d',' -f2 | grep -Fxq \"$2\"",
		"sh", realm, username,
	}
	_, exitCode, _ := d.dockerClient.ExecInContainer(ctx, containerID, checkCmd)
	if exitCode == 0 {
		return nil // user already exists
	}

	createCmd := []string{
		"/opt/keycloak/bin/kcadm.sh", "create", "users",
		"-r", realm,
		"-s", "username=" + username,
		"-s", "enabled=true",
	}
	out, exitCode, execErr := d.dockerClient.ExecInContainer(ctx, containerID, createCmd)
	if execErr != nil {
		return fmt.Errorf("create user %s: %w (output: %s)", username, execErr, out)
	}
	if exitCode != 0 {
		return fmt.Errorf("create user %s exited %d: %s", username, exitCode, out)
	}
	return nil
}

// kcadmAddRole grants the given realm-management role to the user.
// Best-effort: kcadm returns non-zero when the role is already assigned, so
// the exit code is intentionally ignored — subsequent kcadm operations will
// fail clearly if the role isn't actually granted.
func (d *Daemon) kcadmAddRole(ctx context.Context, containerID, realm, username, role string) {
	cmd := []string{
		"/opt/keycloak/bin/kcadm.sh", "add-roles",
		"-r", realm,
		"--uusername", username,
		"--rolename", role,
	}
	out, exitCode, err := d.dockerClient.ExecInContainer(ctx, containerID, cmd)
	if err != nil {
		// Docker exec itself failed (vs kcadm returning non-zero) — log so a
		// downstream "role not granted" failure is traceable to its root cause.
		slog.Debug("kcadm add-roles exec failed", "realm", realm, "user", username, "role", role, "err", err, "output", out)
	} else if exitCode != 0 {
		slog.Debug("kcadm add-roles non-zero exit (usually 'already a member')", "realm", realm, "user", username, "role", role, "exitCode", exitCode, "output", out)
	}
}

// kcadmSetUserPassword resets a specific user's password in the given realm.
// kcadmSetPassword (in this file) is hard-coded to user "admin" — keep it
// for the standard admin-rotation path and use this variant for the SA.
func (d *Daemon) kcadmSetUserPassword(ctx context.Context, containerID, realm, username, newPassword string) error {
	cmd := []string{
		"/opt/keycloak/bin/kcadm.sh", "set-password",
		"-r", realm,
		"--username", username,
		"--new-password", newPassword,
	}
	out, exitCode, err := d.dockerClient.ExecInContainer(ctx, containerID, cmd)
	if err != nil {
		return fmt.Errorf("kcadm set-password %s/%s: %w (output: %s)", realm, username, err, out)
	}
	if exitCode != 0 {
		return fmt.Errorf("kcadm set-password %s/%s exited %d: %s", realm, username, exitCode, out)
	}
	return nil
}

// kcadmDeleteUser deletes the user from the realm by username. Idempotent
// when kcadm is authenticated (an absent user produces an empty `UID_` and
// the delete is skipped) — if `kcadm get` itself fails (lost session,
// transient docker exec error), the wrapper returns the underlying error.
func (d *Daemon) kcadmDeleteUser(ctx context.Context, containerID, realm, username string) error {
	cmd := []string{
		"sh", "-c",
		"UID_=$(/opt/keycloak/bin/kcadm.sh get users -r \"$1\" -q username=\"$2\" " +
			"--fields id,username --format csv --noquotes 2>/dev/null | " +
			"grep -F \",$2\" | cut -d',' -f1 | head -1); " +
			"if [ -n \"$UID_\" ]; then /opt/keycloak/bin/kcadm.sh delete \"users/$UID_\" -r \"$1\"; fi",
		"sh", realm, username,
	}
	out, exitCode, err := d.dockerClient.ExecInContainer(ctx, containerID, cmd)
	if err != nil {
		return fmt.Errorf("delete user %s: %w (output: %s)", username, err, out)
	}
	if exitCode != 0 {
		return fmt.Errorf("delete user %s exited %d: %s", username, exitCode, out)
	}
	return nil
}

// kcadmLogin authenticates kcadm.sh in the keycloak container as the given
// master-realm user. Subsequent kcadm calls reuse the stored credentials.
func (d *Daemon) kcadmLogin(ctx context.Context, containerID, user, password string) error {
	cmd := []string{
		"/opt/keycloak/bin/kcadm.sh", "config", "credentials",
		"--server", "http://localhost:8080",
		"--realm", "master",
		"--user", user,
		"--password", password,
	}
	out, exitCode, err := d.dockerClient.ExecInContainer(ctx, containerID, cmd)
	if err != nil {
		return fmt.Errorf("exec: %w (output: %s)", err, out)
	}
	if exitCode != 0 {
		return fmt.Errorf("exit %d: %s", exitCode, out)
	}
	return nil
}

// kcadmSetPassword runs a single kcadm.sh set-password invocation inside
// the keycloak container for the given realm.
func (d *Daemon) kcadmSetPassword(ctx context.Context, containerID, realm, newPassword string) error {
	cmd := []string{
		"/opt/keycloak/bin/kcadm.sh", "set-password",
		"-r", realm,
		"--username", "admin",
		"--new-password", newPassword,
	}
	out, exitCode, err := d.dockerClient.ExecInContainer(ctx, containerID, cmd)
	if err != nil {
		return fmt.Errorf("kcadm set-password %s: %w (output: %s)", realm, err, out)
	}
	if exitCode != 0 {
		return fmt.Errorf("kcadm set-password %s exited %d: %s", realm, exitCode, out)
	}
	return nil
}
