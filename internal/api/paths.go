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

	Namespace               = APIV1 + "/namespace"
	NamespaceStart          = APIV1 + "/namespace/start"
	NamespaceStop           = APIV1 + "/namespace/stop"
	NamespaceReload         = APIV1 + "/namespace/reload"
	NamespaceUpgrade        = APIV1 + "/namespace/upgrade"
	NamespaceEdit           = APIV1 + "/namespace/edit"
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
	Templates   = APIV1 + "/templates"
	QuickStarts = APIV1 + "/quick-starts"

	Bundles = APIV1 + "/bundles"

	Secrets              = APIV1 + "/secrets"
	SecretsStatus        = Secrets + "/status"
	SecretsUnlock        = Secrets + "/unlock"
	SecretsSetupPassword = Secrets + "/setup-password"
	SecretsReset         = Secrets + "/reset"

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

	// GitSkipPull records a user "Skip" decision from GitPullErrorDialog so
	// subsequent pulls against the same host no-op for the suppression window
	// (Kotlin parity — 1 hour default).
	GitSkipPull = APIV1 + "/git/skip-pull"
)

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
