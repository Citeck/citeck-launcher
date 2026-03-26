package api

type ActionResultDto struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type AppDto struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Image  string   `json:"image"`
	CPU    string   `json:"cpu"`
	Memory string   `json:"memory"`
	Kind   string   `json:"kind"`
	Ports  []string `json:"ports,omitempty"`
}

type DaemonStatusDto struct {
	Running    bool   `json:"running"`
	PID        int64  `json:"pid"`
	Uptime     int64  `json:"uptime"`
	Version    string `json:"version"`
	Workspace  string `json:"workspace"`
	SocketPath string `json:"socketPath"`
}

type NamespaceDto struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	BundleRef string    `json:"bundleRef"`
	Apps      []AppDto  `json:"apps"`
	Links     []LinkDto `json:"links,omitempty"`
}

type LinkDto struct {
	Name  string  `json:"name"`
	URL   string  `json:"url"`
	Icon  string  `json:"icon,omitempty"`
	Order float64 `json:"order"`
}

type EventDto struct {
	Type        string `json:"type"`
	Timestamp   int64  `json:"timestamp"`
	NamespaceID string `json:"namespaceId"`
	AppName     string `json:"appName"`
	Before      string `json:"before"`
	After       string `json:"after"`
}

type HealthDto struct {
	Healthy bool           `json:"healthy"`
	Checks  []HealthCheckDto `json:"checks"`
}

type HealthCheckDto struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ExecResultDto struct {
	ExitCode int64  `json:"exitCode"`
	Output   string `json:"output"`
}

type ExecRequestDto struct {
	Command []string `json:"command"`
}

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

type ErrorDto struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details"`
}

// --- Welcome Screen ---

type NamespaceSummaryDto struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	BundleRef   string `json:"bundleRef"`
}

type QuickStartDto struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	Snapshot string `json:"snapshot,omitempty"`
}

type TemplateDto struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// --- Secrets ---

type SecretMetaDto struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"createdAt"`
}

type SecretCreateDto struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	Scope string `json:"scope,omitempty"`
}

// --- Diagnostics ---

type DiagnosticCheckDto struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "error"
	Message string `json:"message"`
	Fixable bool   `json:"fixable"`
}

type DiagnosticsDto struct {
	Checks []DiagnosticCheckDto `json:"checks"`
}

type DiagFixResultDto struct {
	Fixed   int    `json:"fixed"`
	Failed  int    `json:"failed"`
	Message string `json:"message"`
}

// --- Snapshots ---

type SnapshotDto struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Size      int64  `json:"size"`
}

// --- Namespace creation ---

type NamespaceCreateDto struct {
	Name           string   `json:"name"`
	AuthType       string   `json:"authType"`
	Users          []string `json:"users,omitempty"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	TLSEnabled     bool     `json:"tlsEnabled"`
	TLSMode        string   `json:"tlsMode,omitempty"` // "self-signed", "letsencrypt", "custom"
	PgAdminEnabled bool     `json:"pgAdminEnabled"`
	BundleRepo     string   `json:"bundleRepo"`
	BundleKey      string   `json:"bundleKey"`
	WorkspaceID    string   `json:"workspaceId,omitempty"`
	Snapshot       string   `json:"snapshot,omitempty"`  // snapshot ID from workspace config
	Template       string   `json:"template,omitempty"`  // namespace template ID
}

type SnapshotDownloadDto struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Name   string `json:"name,omitempty"` // output file name (auto-generated if empty)
}

type BundleInfoDto struct {
	Repo     string   `json:"repo"`
	Versions []string `json:"versions"`
}

// ValidationErrorDto is returned when server-side form validation fails.
type ValidationErrorDto struct {
	Error  string          `json:"error"`
	Fields []FieldErrorDto `json:"fields"`
}

type FieldErrorDto struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}
