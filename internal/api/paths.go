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
