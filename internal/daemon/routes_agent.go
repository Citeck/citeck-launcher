package daemon

import (
	"net/http"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

func rejectAgentDiscoveryRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "agent discovery is read-only")
		return
	}
	writeError(w, http.StatusNotFound, "unknown agent discovery path")
}

func (d *Daemon) handleAgentCapabilities(w http.ResponseWriter, _ *http.Request, routes []api.AgentRouteDto) {
	writeJSON(w, d.agentCapabilities(routes))
}

func (d *Daemon) handleAgentOperation(w http.ResponseWriter, r *http.Request, routes []api.AgentRouteDto) {
	id := r.PathValue("id")
	for _, operation := range d.agentCapabilities(routes).Operations {
		if operation.ID == id {
			writeJSON(w, operation)
			return
		}
	}
	writeError(w, http.StatusNotFound, "unknown agent operation")
}

func (d *Daemon) agentCapabilities(registered []api.AgentRouteDto) api.AgentCapabilitiesDto {
	mode := "server"
	if config.IsDesktopMode() {
		mode = "desktop"
	}
	routes := slices.Clone(registered)
	operations := make([]api.AgentOperationDto, 0)
	for _, operation := range agentOperationContracts() {
		for i := range routes {
			if routes[i].Method != operation.Method || routes[i].Path != operation.Path {
				continue
			}
			operation.Supported = slices.Contains(operation.Modes, mode)
			routes[i].OperationID = operation.ID
			operations = append(operations, operation)
			break
		}
	}
	return api.AgentCapabilitiesDto{
		InterfaceVersion: 1,
		LauncherVersion:  d.version,
		Mode:             mode,
		SocketPath:       config.SocketPath(),
		Instructions: []string{
			"Manage Launcher-owned containers through this API or the citeck CLI. Do not use docker stop, start, restart, rm or compose for their lifecycle: reconciliation may reverse direct Docker changes and persisted intent will disagree with the containers.",
			"Read namespace.status first (GET /api/v1/namespace or citeck status --format json). Use the short application names in apps[].name, not image names or Docker container names.",
			"Namespace and application lifecycle routes operate on the daemon's ACTIVE namespace. Verify its id before a mutation. Server mode has one namespace; desktop namespace activation selects the active namespace in its workspace.",
			"Application stop persists detach intent and may regenerate dependents. Application start clears detach intent. Stopping a namespace preserves app detach choices and data; stopping one dependency can leave the namespace STALLED.",
			"Lifecycle success means the request was accepted, not that the target state has been reached. Poll namespace.status, namespace.updating, namespace.updateError and apps[].status/detached, or follow GET /api/v1/events. Check the namespace id while waiting.",
			"Do not bypass HTTP refusals with Docker. A 409 may indicate a snapshot, dependency migration, pending snapshot import, stopped namespace or already-running app; read the JSON error code/message and current state before deciding what to do.",
			"supported describes mode compatibility, not current readiness; handlers still enforce namespace state, locks, data-recovery markers and authorization.",
			"This catalog describes capabilities, not user authorization. Apply only changes within the user's requested scope; destructive operations and secret-bearing responses require corresponding care.",
			"Use the existing local Unix socket or configured authenticated TCP transport. citeck agent uses the same CITECK_DESKTOP/CITECK_RUN/CITECK_HOST/TLS settings as other CLI commands; it never starts a daemon. Do not start a second daemon on the same Docker engine to obtain an API: changing home/run/id does not isolate its Docker resources.",
			"routes is the complete inventory of registered /api/v1 endpoints in this process; some handlers still enforce runtime/mode preconditions. operations supplies explicit contracts for documented automation workflows. Do not guess request bodies for inventory-only routes. GET /api/v1/agent/operations/{id} or citeck agent <id> retrieves a documented contract.",
		},
		Operations: operations,
		Routes:     routes,
	}
}

func agentOperation(id, method, path, description, scope, response string, cli ...string) api.AgentOperationDto {
	return api.AgentOperationDto{
		ID: id, Method: method, Path: path, Description: description, Scope: scope,
		ReadOnly: method == http.MethodGet, Modes: []string{"server", "desktop"},
		Response: response, CLI: cli,
	}
}

func agentParameter(name, location, typ, description string, required bool) api.AgentParameterDto {
	return api.AgentParameterDto{Name: name, In: location, Required: required, Description: description, Schema: map[string]any{"type": typ}}
}

func agentBody(properties map[string]any, required ...string) map[string]any {
	body := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		body["required"] = required
	}
	return body
}

func agentOperationContracts() []api.AgentOperationDto {
	action := "application/json ActionResultDto; observe current namespace/application state for convergence"
	operations := []api.AgentOperationDto{
		agentOperation("daemon.status", "GET", api.DaemonStatus, "Read daemon version, mode and active workspace.", "daemon", "application/json DaemonStatusDto"),
		agentOperation("namespace.status", "GET", api.Namespace, "Read active namespace id, status, apps, update progress and blockers.", "active namespace", "application/json NamespaceDto", "citeck status --format json"),
		agentOperation("namespace.list", "GET", api.Namespaces, "List namespaces in the active workspace; server mode returns its single configured namespace.", "active workspace", "application/json NamespaceSummaryDto[]"),
		agentOperation("git.repository-secret", "PUT", api.Workspaces+"/{id}/git-secret", "Bind an existing Git token secret to a configured workspace or bundle repository without exposing its value.", "workspace id", action),
		agentOperation("namespace.start", "POST", api.NamespaceStart, "Update configuration and request startup of the active namespace.", "active namespace", action, "citeck start --detach"),
		agentOperation("namespace.stop", "POST", api.NamespaceStop, "Request stopping the active namespace, retaining volumes and app detach choices. Keep the daemon running.", "active namespace", action, "citeck stop --detach"),
		agentOperation("namespace.reload-plan", "GET", api.NamespaceReloadPlan, "Preview configuration changes before applying a reload.", "active namespace", "application/json reload plan", "citeck diff --format json"),
		agentOperation("namespace.reload", "POST", api.NamespaceReload, "Regenerate/apply configuration; changed containers may be recreated.", "active namespace", action, "citeck reload"),
		agentOperation("namespace.activate", "POST", api.Namespaces+"/{id}/activate", "Select a desktop namespace in the active workspace. Refused while another active namespace is not STOPPED.", "namespace id in active workspace", action),
		agentOperation("namespace.deactivate", "POST", api.Namespaces+"/deactivate", "Unload the stopped desktop namespace and return to workspace selection.", "active namespace", action),
		agentOperation("namespace.delete", "DELETE", api.Namespaces+"/{id}", "Delete a desktop namespace and its associated resources. An active namespace must be stopped first.", "namespace id in active workspace", action),
		agentOperation("application.start", "POST", api.Apps+"/{name}/start", "Attach/start a configured application through the runtime; clear persisted manual-stop intent.", "application in active namespace", action, "citeck start <name> --detach"),
		agentOperation("application.stop", "POST", api.Apps+"/{name}/stop", "Stop/detach an application and persist the choice so reconciliation respects it.", "application in active namespace", action, "citeck stop <name> --detach"),
		agentOperation("application.restart", "POST", api.Apps+"/{name}/restart", "Request restart through the namespace runtime.", "application in active namespace", action, "citeck restart <name> --detach"),
		agentOperation("application.logs", "GET", api.Apps+"/{name}/logs", "Read application logs or follow their stream.", "application in active namespace", "text/plain; streaming when follow=true", "citeck logs <name> --tail 100", "citeck logs <name> --follow"),
		agentOperation("application.inspect", "GET", api.Apps+"/{name}/inspect", "Inspect a configured application/container. The response can include environment values; redact before sharing.", "application in active namespace", "application/json AppInspectDto", "citeck describe <name> --format json"),
		agentOperation("application.config", "GET", api.Apps+"/{name}/config", "Read effective application YAML and baseline. Configuration may contain secret values.", "application in active namespace", "application/json AppConfigDto"),
		agentOperation("application.exec", "POST", api.Apps+"/{name}/exec", "Execute an explicit command inside a running application via Launcher.", "application in active namespace", "application/json ExecResultDto", "citeck exec <name> -- <command> [args...]"),
		agentOperation("application.jvm-command", "POST", api.Apps+"/{name}/jcmd", "Run a JVM attach command; non-JVM or non-running containers are refused.", "application in active namespace", "application/json JVMCommandResponseDto", "citeck jcmd <name> <command> [args...]"),
		agentOperation("applications.retry-pulls", "POST", api.AppsRetryPullFailed, "Retry failed image pulls after resolving registry authentication.", "active namespace", action),
		agentOperation("health", "GET", api.Health, "Read the system health verdict.", "daemon", "application/json HealthDto", "citeck health --format json"),
		agentOperation("events", "GET", api.Events, "Observe namespace/app lifecycle and progress events without polling.", "daemon event stream", "text/event-stream; SSE ids allow resuming with Last-Event-ID"),
		agentOperation("dependencies.list", "GET", api.Dependencies, "Inspect infrastructure versions, compatibility gates and migration/rollback offers.", "active namespace", "application/json DependenciesDto", "citeck deps --format json"),
		agentOperation("dependencies.preflight", "GET", api.Dependencies+"/{id}/preflight", "Inspect upgrade problems/warnings before touching dependency data.", "dependency in active namespace", "application/json dependency preflight"),
		agentOperation("dependencies.migrate", "POST", api.Dependencies+"/{id}/migrate", "Run a supported dependency data migration after inspecting preflight.", "dependency in active namespace", "application/json migration acknowledgement; observe dependency/namespace progress", "citeck deps upgrade <id> --detach"),
		agentOperation("dependencies.rollback-preflight", "GET", api.Dependencies+"/{id}/rollback/preflight", "Inspect whether retained dependency data supports rollback.", "dependency in active namespace", "application/json dependency rollback preflight"),
		agentOperation("dependencies.rollback", "POST", api.Dependencies+"/{id}/rollback", "Switch back to retained dependency data; writes made after upgrade become unreachable.", "dependency in active namespace", "application/json rollback acknowledgement; observe dependency/namespace progress", "citeck deps rollback <id> --detach"),
		agentOperation("content-storage.options", "GET", api.ContentStorageOptions, "Inspect S3 availability for a selected bundle without pulling git repositories.", "selected bundle", "application/json ContentStorageOptionsDto"),
		agentOperation("content-storage.provision", "POST", api.NamespaceContentStorageProvision, "Retry connecting the active namespace's managed S3 content storage.", "active namespace", "HTTP 202 application/json ActionResultDto; observe namespace.contentStorage"),
		agentOperation("snapshots.list", "GET", api.Snapshots, "List available namespace snapshots.", "active namespace", "application/json snapshot list", "citeck snapshot list --format json"),
		agentOperation("snapshots.export", "POST", api.SnapshotsExport, "Export namespace data; secrets are excluded unless explicitly requested.", "active namespace", "HTTP 200 application/json export acknowledgement; await snapshot_complete/snapshot_error SSE", "citeck snapshot export"),
		agentOperation("snapshots.import", "POST", api.SnapshotsImport, "Restore a named snapshot, replacing selected volume contents and snapshot metadata.", "active namespace", "HTTP 202 application/json import acknowledgement; await snapshot_complete/snapshot_error SSE", "citeck snapshot import <snapshot-name>"),
		agentOperation("volumes.list", "GET", api.APIV1+"/volumes", "List volumes managed by this namespace.", "active namespace", "application/json volume list"),
		agentOperation("bundles.list", "GET", api.Bundles, "List synced bundle repositories and versions.", "active workspace", "application/json BundleInfoDto[]"),
		agentOperation("daemon.logs", "GET", api.DaemonLogs, "Read/follow Launcher logs when lifecycle operations fail.", "daemon", "text/plain; streaming when follow=true", "citeck logs --tail 100"),
	}
	for i := range operations {
		op := &operations[i]
		addAgentOperationDetails(op)
		if op.RequestBody != nil {
			op.ContentType = "application/json"
		}
	}
	return operations
}

func addAgentOperationDetails(op *api.AgentOperationDto) {
	if strings.Contains(op.Path, "{name}") {
		op.Parameters = append(op.Parameters, agentParameter("name", "path", "string", "Short application id from namespace.apps[].name, e.g. onlyoffice or emodel.", true))
	}
	if strings.Contains(op.Path, "{id}") {
		op.Parameters = append(op.Parameters, agentParameter("id", "path", "string", "Namespace id from namespace.list or dependency id from dependencies.list, according to scope.", true))
	}
	switch op.ID {
	case "namespace.start":
		op.Async = true
		p := agentParameter("force", "query", "boolean", "Bypass git pull throttling only; does not force release-image pulls.", false)
		p.Schema["default"] = false
		op.Parameters = append(op.Parameters, p)
		cached := agentParameter("cachedRepo", "query", "array", "Repeat once per explicitly chosen repository URL from Git failure metadata. Uses existing copies for this retry chain only; each choice is revalidated.", false)
		cached.Schema["items"] = map[string]any{"type": "string"}
		op.Parameters = append(op.Parameters, cached)
		op.Notes = append(op.Notes, "A failed Git refresh is visible in namespace.updateError/updateGitError even when cached configuration exists. Do not silently opt into cached data.")
	case "git.repository-secret":
		op.Modes = []string{"desktop"}
		op.Parameters[0].Description = "Workspace id from GET /api/v1/workspaces."
		op.RequestBody = agentBody(map[string]any{"repoUrl": map[string]any{"type": "string", "minLength": 1}, "secretId": map[string]any{"type": "string", "minLength": 1}}, "repoUrl", "secretId")
		op.Notes = append(op.Notes, "The repository must be configured in that workspace. The secret must be an existing GIT_TOKEN; the request changes only the credential reference, not its value. Bundle references are stored in the local workspace overlay and do not change the workspace repository's own credential.")
	case "namespace.stop", "namespace.reload", "application.start", "application.stop", "application.restart", "applications.retry-pulls", "content-storage.provision":
		op.Async = true
	case "namespace.activate", "namespace.deactivate", "namespace.delete":
		op.Modes = []string{"desktop"}
		op.Destructive = op.ID == "namespace.delete"
	case "application.exec":
		op.RequestBody = agentBody(map[string]any{"command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}}, "command")
	case "application.jvm-command":
		op.RequestBody = agentBody(map[string]any{"command": map[string]any{"type": "string", "minLength": 1}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "command")
	case "application.logs", "daemon.logs":
		p := agentParameter("tail", "query", "integer", "Number of recent log lines.", false)
		p.Schema["default"] = 100
		if op.ID == "daemon.logs" {
			p.Schema["default"] = 200
		}
		p.Schema["minimum"] = 0
		p.Schema["maximum"] = 10000
		op.Parameters = append(op.Parameters, p, agentParameter("follow", "query", "boolean", "Stream future log lines.", false))
	case "events":
		op.Parameters = append(op.Parameters, agentParameter("lastSeq", "query", "integer", "Resume after this event sequence; Last-Event-ID header is also supported.", false))
	case "dependencies.migrate":
		op.Async, op.Destructive = true, true
		op.RequestBody = agentBody(map[string]any{"replaceExistingVolume": map[string]any{"type": "boolean", "default": false, "description": "Explicitly authorize deleting a leftover TARGET volume. Inspect preflight first."}})
	case "dependencies.rollback":
		op.Async, op.Destructive = true, true
	case "content-storage.options":
		for _, name := range []string{"bundleRepo", "bundleKey", "workspaceId"} {
			op.Parameters = append(op.Parameters, agentParameter(name, "query", "string", "Selected bundle/workspace; empty bundle uses the active namespace.", false))
		}
	case "snapshots.export":
		op.Async = true
		op.Notes = []string{"Requires the namespace to be STOPPED before the API request. Subscribe to SSE before starting export; await snapshot_complete or snapshot_error for the matching namespace id."}
		op.RequestBody = agentBody(map[string]any{"volumes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Omit for all volumes; an empty array selects none."}, "includeSecrets": map[string]any{"type": "boolean", "default": false, "description": "Opt-in portable plaintext secrets in the archive; protect the output file."}})
		op.Parameters = append(op.Parameters, agentParameter("name", "query", "string", "Snapshot file name.", false), agentParameter("output", "query", "string", "Absolute daemon-side output directory.", false))
	case "snapshots.import":
		op.Async, op.Destructive = true, true
		op.Parameters = append(op.Parameters, agentParameter("name", "query", "string", "Existing daemon-side .zip snapshot name from snapshots.list.", true))
		op.Notes = []string{"Requires the namespace to be STOPPED before the API request. Subscribe to SSE before import and await snapshot_complete or snapshot_error; the API does not restart it. The CLI handles stop/restart around a successful import. Failed volume extraction leaves a durable pending-import marker blocking start until a successful retry; do not bypass it. Configuration and external S3 objects are outside the snapshot's volume/metadata transaction."}
	}
	if op.ID == "application.start" || op.ID == "application.restart" {
		op.Notes = []string{"Requires namespace RUNNING, STARTING or STALLED. Snapshot/migration guards remain authoritative."}
	}
	if op.ID == "application.start" {
		op.Notes = append(op.Notes, "A detached app can also be reattached while the namespace is STOPPED; it runs when the namespace starts.")
	}
}
