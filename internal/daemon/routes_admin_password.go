package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// handleSetAdminPassword resets the admin password in both keycloak realms
// (master + ecos-app) by driving kcadm.sh inside the running keycloak
// container, then persists the new value to the `_admin_password` system
// secret so in-memory state and future daemon restarts stay consistent.
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
	currentPassword := d.systemSecrets.AdminPassword
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

	// kcadm.sh with login + set-password needs a few seconds at most, but
	// keycloak startup can be slow and the admin realm might not be ready
	// on the first few seconds after container boot. 60s is generous.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := d.resetKeycloakAdminPassword(ctx, kcApp.ContainerID, currentPassword, req.Password); err != nil {
		writeInternalError(w, err)
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

	// Keycloak, RabbitMQ, and PgAdmin were updated at runtime above —
	// their containers don't need a restart. However the webapps connect
	// to RabbitMQ using the ECOS_WEBAPP_RABBITMQ_PASSWORD env var which
	// is baked into their container spec at creation time. A reload
	// regenerates the containers with the new env value so the webapps
	// can reconnect to RabbitMQ with the updated password.
	if d.reloadMu.TryLock() {
		if reloadErr := d.doReload(); reloadErr != nil {
			slog.Warn("Reload after admin password change failed", "err", reloadErr)
		}
		d.reloadMu.Unlock()
	} else {
		slog.Warn("Reload already in progress, webapps will pick up new RabbitMQ password on next reload")
	}

	slog.Info("Admin password reset (keycloak master + ecos-app, rabbitmq, pgadmin, webapps reloaded)")
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Admin password reset"})
}

// resetKeycloakAdminPassword authenticates kcadm.sh as the keycloak master
// admin (using currentPassword — the one still recorded in the system
// secret store at the moment of the call) and resets the admin user's
// password in BOTH the master realm and the ecos-app realm. Install-time
// parity is maintained on every change — one password covers keycloak and
// the platform.
//
// The ecos-app password is changed first; the master password last. This
// keeps kcadm authenticated for the whole operation (the current session
// token is obtained against the master realm with the OLD password).
func (d *Daemon) resetKeycloakAdminPassword(ctx context.Context, containerID, currentPassword, newPassword string) error {
	if currentPassword == "" {
		currentPassword = "admin"
	}

	// Step 1: authenticate kcadm.sh using the current master realm admin
	// credentials. Note: passing the password on the CLI is visible to
	// other users of the container via /proc/<pid>/cmdline. For our
	// server-mode, single-tenant deployment that's acceptable —
	// kcadm.sh doesn't support reading the password from stdin in
	// config credentials mode.
	loginCmd := []string{
		"/opt/keycloak/bin/kcadm.sh", "config", "credentials",
		"--server", "http://localhost:8080",
		"--realm", "master",
		"--user", "admin",
		"--password", currentPassword,
	}
	out, exitCode, err := d.dockerClient.ExecInContainer(ctx, containerID, loginCmd)
	if err != nil {
		return fmt.Errorf("kcadm login: %w (output: %s)", err, out)
	}
	if exitCode != 0 {
		return fmt.Errorf("kcadm login exited %d: %s", exitCode, out)
	}

	// Step 2: reset the ecos-app realm admin user's password first.
	if err := d.kcadmSetPassword(ctx, containerID, "ecos-app", newPassword); err != nil {
		return err
	}

	// Step 3: reset the master realm admin user's password last. We
	// intentionally do this after ecos-app because a failure here still
	// leaves the platform-facing credential updated; keycloak's master
	// admin is only used internally by kcadm and by clients substituting
	// ${KK_ADMIN_PASSWORD}.
	if err := d.kcadmSetPassword(ctx, containerID, "master", newPassword); err != nil {
		return err
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

