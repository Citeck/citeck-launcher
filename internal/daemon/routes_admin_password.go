package daemon

import (
	"context"
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
	if err := d.resetKeycloakAdminPassword(ctx, kcApp.ContainerID, "", req.Password); err != nil {
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
	if err := d.kcadmSetPassword(ctx, kcApp.ContainerID, "master", req.Password); err != nil {
		writeInternalError(w, fmt.Errorf("master realm admin password rotation failed "+
			"(ecos-app already rotated — master console may still accept the old password; "+
			"retry `citeck setup admin-password`): %w", err))
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
// best-effort phase — see handleSetAdminPassword. Splitting the two keeps
// ecos-app failures fatal (platform-critical) while letting master realm
// failures be reported without aborting the rotation.
func (d *Daemon) resetKeycloakAdminPassword(ctx context.Context, containerID, _, newPassword string) error {
	d.configMu.RLock()
	saPassword := d.systemSecrets.CiteckSA
	d.configMu.RUnlock()

	if saPassword == "" {
		return fmt.Errorf("citeck SA not configured — restart namespace to run keycloak init")
	}
	if err := d.kcadmLogin(ctx, containerID, namespace.CiteckSAUser, saPassword); err != nil {
		return fmt.Errorf("kcadm login as %s: %w "+
			"(SA may be out of sync after snapshot import — try `citeck reload` to re-run init)",
			namespace.CiteckSAUser, err)
	}

	// Reset the ecos-app realm admin password only (user-facing)
	if err := d.kcadmSetPassword(ctx, containerID, "ecos-app", newPassword); err != nil {
		return err
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
