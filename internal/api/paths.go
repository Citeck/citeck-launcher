package api

import "fmt"

const (
	APIV1 = "/api/v1"

	DaemonStatus   = APIV1 + "/daemon/status"
	DaemonShutdown = APIV1 + "/daemon/shutdown"

	Namespace      = APIV1 + "/namespace"
	NamespaceStart = APIV1 + "/namespace/start"
	NamespaceStop  = APIV1 + "/namespace/stop"
	NamespaceReload = APIV1 + "/namespace/reload"

	Events = APIV1 + "/events"
	Apps   = APIV1 + "/apps"
	Health = APIV1 + "/health"

	// Phase E1: Namespaces
	Namespaces  = APIV1 + "/namespaces"
	Templates   = APIV1 + "/templates"
	QuickStarts = APIV1 + "/quick-starts"

	// Phase E3: Bundles
	Bundles = APIV1 + "/bundles"

	// Phase F1: Secrets
	Secrets = APIV1 + "/secrets"

	// Phase F2: Diagnostics
	Diagnostics    = APIV1 + "/diagnostics"
	DiagnosticsFix = APIV1 + "/diagnostics/fix"

	// Phase 3E: Forms
	Forms = APIV1 + "/forms"

	// Phase F3: Snapshots
	Snapshots       = APIV1 + "/snapshots"
	SnapshotsExport = APIV1 + "/snapshots/export"
	SnapshotsImport = APIV1 + "/snapshots/import"
)

func AppLogs(name string) string {
	return fmt.Sprintf("%s/%s/logs", Apps, name)
}

func AppRestart(name string) string {
	return fmt.Sprintf("%s/%s/restart", Apps, name)
}

func AppInspect(name string) string {
	return fmt.Sprintf("%s/%s/inspect", Apps, name)
}

func AppExec(name string) string {
	return fmt.Sprintf("%s/%s/exec", Apps, name)
}

func AppStop(name string) string {
	return fmt.Sprintf("%s/%s/stop", Apps, name)
}

func AppStart(name string) string {
	return fmt.Sprintf("%s/%s/start", Apps, name)
}
