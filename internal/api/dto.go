package api

// ActionResultDto is the response for simple action endpoints.
type ActionResultDto struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// AppDto represents an application in the namespace.
type AppDto struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	StatusText string `json:"statusText,omitempty"`
	Image      string `json:"image"`
	CPU        string `json:"cpu"`
	Memory     string `json:"memory"`
	// MemoryPercent is the container's memory usage as a percentage of its
	// configured limit (0..100). Zero when no memory limit is set.
	MemoryPercent float64 `json:"memoryPercent,omitempty"`
	// MemoryWarning is true when memory usage is at or above 80% of the
	// configured limit. Surfaces a "high memory usage" tooltip in the UI.
	MemoryWarning bool `json:"memoryWarning,omitempty"`
	// MemoryCritical is true when memory usage is at or above 95% of the
	// configured limit. Surfaces a "near OOM limit" warning in the UI.
	MemoryCritical bool `json:"memoryCritical,omitempty"`
	// CPUThrottled is true when the container hit its CPU quota in the
	// most recent stats sample (Kotlin parity).
	CPUThrottled     bool     `json:"cpuThrottled,omitempty"`
	Kind             string   `json:"kind"`
	Ports            []string `json:"ports,omitempty"`
	Edited           bool     `json:"edited,omitempty"`
	Locked           bool     `json:"locked,omitempty"`
	RestartCount     int      `json:"restartCount,omitempty"`
	EditedFilesCount int      `json:"editedFilesCount,omitempty"`
}

// AppFileDto describes a single bind-mounted file exposed via the per-app
// file API. Edited=true means the user has modified the file via the Web UI
// — the launcher preserves those edits across reload/regenerate.
type AppFileDto struct {
	Path   string `json:"path"`
	Edited bool   `json:"edited,omitempty"`
}

// RestartEventDto represents a restart event for the API.
type RestartEventDto struct {
	Timestamp   string `json:"ts"`
	App         string `json:"app"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
	Diagnostics string `json:"diagnostics,omitempty"`
}

// DaemonStatusDto reports the daemon's runtime status.
type DaemonStatusDto struct {
	Running    bool   `json:"running"`
	PID        int64  `json:"pid"`
	Uptime     int64  `json:"uptime"`
	Version    string `json:"version"`
	Workspace  string `json:"workspace"`
	SocketPath string `json:"socketPath"`
	Desktop    bool   `json:"desktop"`
	Locale     string `json:"locale,omitempty"`
}

// NamespaceDto represents a namespace with its apps and links.
type NamespaceDto struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	BundleRef   string    `json:"bundleRef"`
	BundleError string    `json:"bundleError,omitempty"`
	Apps        []AppDto  `json:"apps"`
	Links       []LinkDto `json:"links,omitempty"`
	// HostCPUs is the number of CPU cores visible to the daemon process,
	// straight from runtime.NumCPU(). The UI uses it to cap the aggregate
	// CPU progress bar at (HostCPUs * 100)% — Docker per-container stats
	// already span all cores (a container fully using N cores reads as
	// N*100%), so the host total is the only meaningful aggregate ceiling.
	HostCPUs int `json:"hostCpus,omitempty"`
}

// LinkDto represents a named URL link associated with a namespace.
type LinkDto struct {
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	Icon           string  `json:"icon,omitempty"`
	Order          float64 `json:"order"`
	Category       string  `json:"category,omitempty"`    // grouping header in the sidebar
	Description    string  `json:"description,omitempty"` // tooltip
	AlwaysEnabled  bool    `json:"alwaysEnabled,omitempty"` // remains clickable when namespace is STOPPED (Kotlin parity)
}

// EventDto represents a server-sent event for state changes.
//
// Type-specific fields (omitempty so legacy events stay unchanged on the wire):
//   - "pull_progress": Percent (0..100), Phase (active layer id / status string).
//   - "pull_auth_required": After holds the registry host extracted from the image
//     reference so the UI can pre-fill the credentials dialog.
//   - "snapshot_progress": Current (1-based volume index), Total (volume count),
//     After (volume name). Emitted once per volume during export/import so the
//     UI can render a determinate progress bar inside the blocking overlay.
type EventDto struct {
	Type        string  `json:"type"`
	Seq         int64   `json:"seq"`
	Timestamp   int64   `json:"timestamp"`
	NamespaceID string  `json:"namespaceId"`
	AppName     string  `json:"appName"`
	Before      string  `json:"before"`
	After       string  `json:"after"`
	Percent     float64 `json:"percent,omitempty"`
	Phase       string  `json:"phase,omitempty"`
	Current     int     `json:"current,omitempty"`
	Total       int     `json:"total,omitempty"`
}

// HealthDto reports the overall daemon health status.
type HealthDto struct {
	Status  string           `json:"status"` // "healthy", "degraded", "unhealthy"
	Healthy bool             `json:"healthy"`
	Checks  []HealthCheckDto `json:"checks"`
}

// HealthCheckDto represents a single health check result.
type HealthCheckDto struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ExecResultDto is the response from executing a command in a container.
type ExecResultDto struct {
	ExitCode int64  `json:"exitCode"`
	Output   string `json:"output"`
}

// ExecRequestDto is the request to execute a command in a container.
type ExecRequestDto struct {
	Command []string `json:"command"`
}

// AppInspectDto contains detailed container inspection data.
type AppInspectDto struct {
	Name         string            `json:"name"`
	ContainerID  string            `json:"containerId"`
	Image        string            `json:"image"`
	Status       string            `json:"status"`
	State        string            `json:"state"`
	Ports        []string          `json:"ports"`
	Volumes      []string          `json:"volumes"`
	Env          []string          `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	RestartCount int               `json:"restartCount"`
	StartedAt    string            `json:"startedAt"`
	Uptime       int64             `json:"uptime"`
}

// ErrorDto is the standard error response format.
type ErrorDto struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Namespace lifecycle status values carried by NamespaceDto.Status.
// These mirror namespace.NsRuntimeStatus and are the single source of
// truth for the wire format — untyped so the namespace package can
// adopt them as its typed NsRuntimeStatus values (see namespace/runtime.go).
const (
	NsStatusStopped  = "STOPPED"
	NsStatusStarting = "STARTING"
	NsStatusRunning  = "RUNNING"
	NsStatusStopping = "STOPPING"
	NsStatusStalled  = "STALLED"
)

// Per-app lifecycle status values carried by AppDto.Status. Mirror of
// namespace.AppRuntimeStatus — same single-source-of-truth pattern as the
// NsStatus* constants above.
const (
	AppStatusReadyToPull    = "READY_TO_PULL"
	AppStatusPulling        = "PULLING"
	AppStatusPullFailed     = "PULL_FAILED"
	AppStatusReadyToStart   = "READY_TO_START"
	AppStatusDepsWaiting    = "DEPS_WAITING"
	AppStatusStarting       = "STARTING"
	AppStatusRunning        = "RUNNING"
	AppStatusFailed         = "FAILED"
	AppStatusStartFailed    = "START_FAILED"
	AppStatusStopping       = "STOPPING"
	AppStatusStoppingFailed = "STOPPING_FAILED"
	AppStatusStopped        = "STOPPED"
	// AppStatusUpdating is the in-flight recreate state: the runtime sent
	// SIGTERM to the old container because its deployment hash diverged from
	// the new desired definition, and the very next leg is READY_TO_PULL →
	// PULLING → READY_TO_START → STARTING. STOPPING is reserved for explicit
	// user-initiated stops; UPDATING marks transitions the runtime drives
	// itself so the daemon log reads the way the state machine actually
	// behaves rather than masking a stop via desiredNext lookahead.
	AppStatusUpdating = "UPDATING"
)

// ErrCodeAppNotFound and related constants are machine-readable error codes for API consumers.
const (
	ErrCodeAppNotFound        = "APP_NOT_FOUND"
	ErrCodeSnapshotInProgress = "SNAPSHOT_IN_PROGRESS"
	ErrCodeInvalidConfig      = "INVALID_CONFIG"
	ErrCodeInvalidRequest     = "INVALID_REQUEST"
	ErrCodeSSRFBlocked        = "SSRF_BLOCKED"
	ErrCodeRateLimited        = "RATE_LIMITED"
	ErrCodeNotConfigured      = "NOT_CONFIGURED"
	ErrCodeAppAlreadyRunning  = "APP_ALREADY_RUNNING"
	ErrCodeNamespaceRunning   = "NAMESPACE_RUNNING"
	ErrCodeCSRFMissing        = "CSRF_MISSING"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeNamespaceExists    = "NAMESPACE_EXISTS"
	ErrCodeReloadInProgress   = "RELOAD_IN_PROGRESS"
	ErrCodeDesktopOnly        = "DESKTOP_ONLY"
	ErrCodeWorkspaceExists    = "WORKSPACE_EXISTS"
	ErrCodeWorkspaceNotFound  = "WORKSPACE_NOT_FOUND"
	ErrCodeWorkspaceInUse     = "WORKSPACE_IN_USE"
	// ErrCodeEncryptionNotSetUp is returned by secret-write endpoints when the
	// SecretService has no master password yet (Kotlin parity — desktop never
	// auto-initializes encryption). The UI catches this and runs the
	// CreateMasterPwd flow before retrying the original save.
	ErrCodeEncryptionNotSetUp = "ENCRYPTION_NOT_SET_UP" //nolint:gosec // G101: error code constant, not a credential
)

// UpgradeRequestDto is the request body for the namespace upgrade endpoint.
type UpgradeRequestDto struct {
	BundleRef string `json:"bundleRef"`
}

// --- Welcome Screen ---

// NamespaceSummaryDto is a lightweight namespace representation for the welcome screen.
type NamespaceSummaryDto struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	BundleRef   string `json:"bundleRef"`
}

// WorkspaceDto describes a workspace for API consumers (desktop-only multi-workspace).
//
// RepoPullPeriod is an ISO 8601 duration string (e.g. "PT2H"); AuthType is
// "NONE" or "TOKEN" (TOKEN resolves a secret under key "ws:{id}:repo").
// Defaults applied at the storage layer when fields are empty.
type WorkspaceDto struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	RepoBranch     string `json:"repoBranch"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
	Active         bool   `json:"active"`
	Namespaces     int    `json:"namespaces"`
}

// WorkspaceCreateDto is the request body for POST /api/v1/workspaces.
// ID may be empty — the daemon derives a safe slug from Name.
type WorkspaceCreateDto struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	RepoBranch     string `json:"repoBranch,omitempty"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
}

// WorkspaceUpdateDto is the request body for PUT /api/v1/workspaces/{id}.
// Name + repo fields are optional — only non-empty fields are applied.
type WorkspaceUpdateDto struct {
	Name           string `json:"name,omitempty"`
	RepoURL        string `json:"repoUrl,omitempty"`
	RepoBranch     string `json:"repoBranch,omitempty"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
}

// QuickStartDto represents a quick-start template entry. BundleRef is the
// resolved "repo:key" reference the QS button surfaces as its subtitle
// (Kotlin parity: WelcomeScreen.kt:387 renders `namespaceConfig.bundleRef`).
type QuickStartDto struct {
	Name      string `json:"name"`
	Template  string `json:"template"`
	Snapshot  string `json:"snapshot,omitempty"`
	BundleRef string `json:"bundleRef,omitempty"`
}

// TemplateDto represents a namespace template.
type TemplateDto struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// --- Secrets ---

// SecretMetaDto contains non-sensitive secret metadata for API responses.
type SecretMetaDto struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"createdAt"`
}

// SecretCreateDto is the request body for creating or updating a secret.
//
// Username is set for BASIC_AUTH / REGISTRY_AUTH only; Value carries the
// password verbatim (Kotlin AuthSecret.Basic parity — passwords containing
// ':' must round-trip untouched).
type SecretCreateDto struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
	Value    string `json:"value"`
	Scope    string `json:"scope,omitempty"`
}

// --- Diagnostics ---

// DiagnosticCheckDto represents a single diagnostic check result.
type DiagnosticCheckDto struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warning", "error"
	Message string `json:"message"`
	Fixable bool   `json:"fixable"`
}

// DiagnosticsDto aggregates all diagnostic check results.
type DiagnosticsDto struct {
	Checks []DiagnosticCheckDto `json:"checks"`
}

// DiagFixResultDto reports the outcome of applying diagnostic fixes.
type DiagFixResultDto struct {
	Fixed   int    `json:"fixed"`
	Failed  int    `json:"failed"`
	Message string `json:"message"`
}

// --- Snapshots ---

// SnapshotDto represents a snapshot file with metadata.
type SnapshotDto struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Size      int64  `json:"size"`
}

// --- Namespace creation ---

// NamespaceCreateDto is the request body for creating a new namespace.
type NamespaceCreateDto struct {
	Name               string   `json:"name"`
	AuthType           string   `json:"authType"`
	Users              []string `json:"users,omitempty"`
	Host               string   `json:"host"`
	Port               int      `json:"port"`
	TLSEnabled         bool     `json:"tlsEnabled"`
	TLSMode            string   `json:"tlsMode,omitempty"` // "self-signed", "letsencrypt", "custom"
	PgAdminEnabled     bool     `json:"pgAdminEnabled"`
	BundleRepo         string   `json:"bundleRepo"`
	BundleKey          string   `json:"bundleKey"`
	WorkspaceID        string   `json:"workspaceId,omitempty"`
	Snapshot           string   `json:"snapshot,omitempty"`       // snapshot ID from workspace config
	Template           string   `json:"template,omitempty"`       // namespace template ID
	MasterPassword     string   `json:"masterPassword,omitempty"` // encryption master password
	UseDefaultPassword bool     `json:"useDefaultPassword"`       // use default "citeck" password
}

// SnapshotDownloadDto is the request body for downloading a snapshot from a URL.
type SnapshotDownloadDto struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Name   string `json:"name,omitempty"` // output file name (auto-generated if empty)
}

// NamespaceCreateDefaultsDto is the pre-filled form payload for the create
// dialog. Mirrors the Kotlin 1.x `toFormData(null)` path in NamespacesService:
// auto-generated "Citeck #N" name + bundle/auth defaults pulled from the
// workspace's "default" namespace template (with LATEST → first repo + LATEST
// fallback). Returned by GET /namespace/create-defaults.
type NamespaceCreateDefaultsDto struct {
	Name       string   `json:"name"`
	BundleRepo string   `json:"bundleRepo"`
	BundleKey  string   `json:"bundleKey"`
	AuthType   string   `json:"authType"`
	Users      []string `json:"users,omitempty"`
}

// NamespaceEditDto exposes the typed subset of namespace.yml that the Web
// UI's "edit namespace" form drives. Mirrors the field set the Kotlin
// EditNamespaceDialog exposed (name, bundleRef, authType, users, proxy host
// + port, TLS toggle, pgAdmin toggle). Round-trip safe: GET returns the
// current values; PUT applies them on top of the existing on-disk YAML so
// fields outside this DTO are preserved.
type NamespaceEditDto struct {
	Name           string   `json:"name"`
	BundleRepo     string   `json:"bundleRepo"`
	BundleKey      string   `json:"bundleKey"`
	AuthType       string   `json:"authType"`
	Users          []string `json:"users,omitempty"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	TLSEnabled     bool     `json:"tlsEnabled"`
	PgAdminEnabled bool     `json:"pgAdminEnabled"`
}

// BundleInfoDto describes a bundle repository and its available versions.
type BundleInfoDto struct {
	Repo     string   `json:"repo"`
	Versions []string `json:"versions"`
}

// ValidationErrorDto is returned when server-side form validation fails.
type ValidationErrorDto struct {
	Error  string          `json:"error"`
	Fields []FieldErrorDto `json:"fields"`
}

// FieldErrorDto identifies a specific form field validation error.
type FieldErrorDto struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// --- System / file-manager helpers ---

// OpenDirRequestDto is the request body for the "open directory in OS file
// manager" endpoint. The kind identifies a server-side allowlisted path so
// the request itself never carries a raw filesystem path: this avoids any
// path-traversal foothold and keeps the API stable when desktop and server
// modes resolve the directory differently.
type OpenDirRequestDto struct {
	// Kind selects which allowlisted directory to open.
	// Supported values: "volumes" (current namespace's volumes/runtime base).
	Kind string `json:"kind"`
}

// OpenDirResponseDto reports what happened. Path is always populated (even
// when Opened is false in server-mode) so the UI can show / copy it.
type OpenDirResponseDto struct {
	Opened bool   `json:"opened"`
	Path   string `json:"path"`
	// Mode is "desktop" (Wails / xdg-open used) or "server" (path returned only).
	Mode    string `json:"mode"`
	Message string `json:"message,omitempty"`
}
