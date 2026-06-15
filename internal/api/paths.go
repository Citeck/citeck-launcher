package api

import "fmt"

// APIV1 is the base path prefix for all v1 API routes.
const APIV1 = "/api/v1"

// API route constants for daemon, namespace, and resource endpoints.
const (
	DaemonStatus   = APIV1 + "/daemon/status"
	DaemonShutdown = APIV1 + "/daemon/shutdown"
	DaemonLogs     = APIV1 + "/daemon/logs"
	Config         = APIV1 + "/config"
	ConfigApplied  = APIV1 + "/config/applied"
	UIPrefs        = APIV1 + "/ui-prefs"

	Namespace               = APIV1 + "/namespace"
	NamespaceStart          = APIV1 + "/namespace/start"
	NamespaceStop           = APIV1 + "/namespace/stop"
	NamespaceReload         = APIV1 + "/namespace/reload"
	NamespaceReloadPlan     = APIV1 + "/namespace/reload-plan"
	NamespaceUpgrade        = APIV1 + "/namespace/upgrade"
	NamespaceCreateDefaults = APIV1 + "/namespace/create-defaults"
	NamespaceAdminPassword  = APIV1 + "/namespace/admin-password"
	RestartEvents           = APIV1 + "/namespace/restart-events"

	Events = APIV1 + "/events"
	Apps   = APIV1 + "/apps"
	Health = APIV1 + "/health"

	// AppsRetryPullFailed re-queues every PULL_FAILED app for a fresh pull
	// attempt without waiting for the auto-retry backoff. The Web UI calls
	// this after the user saves registry credentials so the affected apps
	// pick up the new secret immediately.
	AppsRetryPullFailed = APIV1 + "/apps/retry-pull-failed"

	Namespaces  = APIV1 + "/namespaces"
	QuickStarts = APIV1 + "/quick-starts"

	// NamespaceEdit is the id-scoped namespace-edit endpoint, expressed as a
	// Go 1.22 mux routing pattern. GET returns the authoritative editable
	// values stored in namespace {id}'s namespace.yml (bundle key RAW — a
	// stored "LATEST" stays "LATEST"); PUT patches and persists them, and
	// triggers a live reload only when {id} is the active namespace. Clients
	// build concrete URLs via NamespaceEditPath.
	NamespaceEdit = Namespaces + "/{id}/edit"

	Bundles = APIV1 + "/bundles"

	Secrets              = APIV1 + "/secrets"
	SecretsUnlock        = Secrets + "/unlock"
	SecretsSetupPassword = Secrets + "/setup-password"
	SecretsReset         = Secrets + "/reset"

	// Licenses is the enterprise-license CRUD collection; LicensesStatus is
	// the read-only effective-license summary consumed by `citeck status`
	// and the dashboard indicator (absent on pre-2.6 daemons — clients must
	// treat a 404 as "no license info").
	Licenses       = APIV1 + "/licenses"
	LicensesStatus = Licenses + "/status"

	MigrationStatus         = APIV1 + "/migration/status"
	MigrationMasterPassword = APIV1 + "/migration/master-password"

	Diagnostics    = APIV1 + "/diagnostics"
	DiagnosticsFix = APIV1 + "/diagnostics/fix"

	Forms = APIV1 + "/forms"

	Snapshots          = APIV1 + "/snapshots"
	SnapshotsExport    = APIV1 + "/snapshots/export"
	SnapshotsImport    = APIV1 + "/snapshots/import"
	SnapshotsDownload  = APIV1 + "/snapshots/download"
	WorkspaceSnapshots = APIV1 + "/workspace/snapshots"
	WorkspaceUpdate    = APIV1 + "/workspace/update"

	// Multi-workspace (desktop-only). All return 404 in server mode.
	Workspaces = APIV1 + "/workspaces"

	SystemOpenDir = APIV1 + "/system/open-dir"

	DesktopTrayMenu = APIV1 + "/desktop/tray-menu"

	// Desktop auto-update — desktop-only; not registered in server mode.
	DesktopUpdateStatus    = APIV1 + "/desktop/update/status"
	DesktopUpdateCheck     = APIV1 + "/desktop/update/check"
	DesktopUpdateChangelog = APIV1 + "/desktop/update/changelog"
	DesktopUpdateApply     = APIV1 + "/desktop/update/apply"

	// GitSkipPull records a user "Skip" decision from GitPullErrorDialog so
	// subsequent pulls against the same host no-op for the suppression window
	// (Kotlin parity — 1 hour default).
	GitSkipPull = APIV1 + "/git/skip-pull"

	// AuthSession is the browser token→cookie handshake for the opt-in API
	// token auth (daemon.yml api_auth). Deliberately OUTSIDE /api/v1: the
	// auth middleware protects /api/* and this route must stay reachable to
	// establish a session. GET /auth/session?token=<token> sets an HttpOnly
	// session cookie and redirects to /.
	AuthSession = "/auth/session"
)

// NamespaceEditPath returns the concrete edit endpoint for a namespace id
// (the client-side counterpart of the NamespaceEdit routing pattern).
func NamespaceEditPath(id string) string {
	return fmt.Sprintf("%s/%s/edit", Namespaces, id)
}

// AppLogs returns the API path for streaming an app's container logs.
func AppLogs(name string) string {
	return fmt.Sprintf("%s/%s/logs", Apps, name)
}

// AppRestart returns the API path for restarting an app.
func AppRestart(name string) string {
	return fmt.Sprintf("%s/%s/restart", Apps, name)
}

// AppInspect returns the API path for inspecting an app's container.
func AppInspect(name string) string {
	return fmt.Sprintf("%s/%s/inspect", Apps, name)
}

// AppExec returns the API path for executing a command in an app's container.
func AppExec(name string) string {
	return fmt.Sprintf("%s/%s/exec", Apps, name)
}

// AppStop returns the API path for stopping an app.
func AppStop(name string) string {
	return fmt.Sprintf("%s/%s/stop", Apps, name)
}

// AppStart returns the API path for starting an app.
func AppStart(name string) string {
	return fmt.Sprintf("%s/%s/start", Apps, name)
}
