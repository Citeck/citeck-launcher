package api

type ActionResultDto struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type AppDto struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Image    string   `json:"image"`
	Detached bool     `json:"detached"`
	CPU      string   `json:"cpu"`
	Memory   string   `json:"memory"`
	Kind     string   `json:"kind"`
	Ports    []string `json:"ports,omitempty"`
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
