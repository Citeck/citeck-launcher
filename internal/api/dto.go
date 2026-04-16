package api

// ActionResultDto is the response for simple action endpoints.
type ActionResultDto struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// AppDto represents an application in the namespace.
type AppDto struct {
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	StatusText   string   `json:"statusText,omitempty"`
	Image        string   `json:"image"`
	CPU          string   `json:"cpu"`
	Memory       string   `json:"memory"`
	Kind         string   `json:"kind"`
	Ports        []string `json:"ports,omitempty"`
	Edited       bool     `json:"edited,omitempty"`
	Locked       bool     `json:"locked,omitempty"`
	RestartCount int      `json:"restartCount,omitempty"`
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
}

// LinkDto represents a named URL link associated with a namespace.
type LinkDto struct {
	Name  string  `json:"name"`
	URL   string  `json:"url"`
	Icon  string  `json:"icon,omitempty"`
	Order float64 `json:"order"`
}

// EventDto represents a server-sent event for state changes.
type EventDto struct {
	Type        string `json:"type"`
	Seq         int64  `json:"seq"`
	Timestamp   int64  `json:"timestamp"`
	NamespaceID string `json:"namespaceId"`
	AppName     string `json:"appName"`
	Before      string `json:"before"`
	After       string `json:"after"`
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

// QuickStartDto represents a quick-start template entry.
type QuickStartDto struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	Snapshot string `json:"snapshot,omitempty"`
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
type SecretCreateDto struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	Scope string `json:"scope,omitempty"`
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
