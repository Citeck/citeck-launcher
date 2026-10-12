package api

// AgentCapabilitiesDto is the versioned discovery document for automation.
// It describes existing routes; discovery never grants permission to mutate.
type AgentCapabilitiesDto struct {
	InterfaceVersion int                 `json:"interfaceVersion"`
	LauncherVersion  string              `json:"launcherVersion"`
	Mode             string              `json:"mode"`
	SocketPath       string              `json:"socketPath"`
	Instructions     []string            `json:"instructions"`
	Operations       []AgentOperationDto `json:"operations"`
	Routes           []AgentRouteDto     `json:"routes"`
}

// AgentRouteDto records an actually registered API route, including routes
// whose request contract is not part of the documented automation workflows.
type AgentRouteDto struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operationId,omitempty"`
}

// AgentOperationDto describes one supported automation workflow. Parameters
// carry JSON Schema value constraints; bodies use standard JSON Schema.
type AgentOperationDto struct {
	ID          string              `json:"id"`
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Description string              `json:"description"`
	Scope       string              `json:"scope"`
	ReadOnly    bool                `json:"readOnly"`
	Destructive bool                `json:"destructive"`
	Async       bool                `json:"async"`
	Supported   bool                `json:"supported"`
	Modes       []string            `json:"modes"`
	CLI         []string            `json:"cli,omitempty"`
	Parameters  []AgentParameterDto `json:"parameters,omitempty"`
	RequestBody map[string]any      `json:"requestBody,omitempty"`
	ContentType string              `json:"requestContentType,omitempty"`
	Response    string              `json:"response"`
	Notes       []string            `json:"notes,omitempty"`
}

// AgentParameterDto describes a path or query parameter without live values.
type AgentParameterDto struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Required    bool           `json:"required"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}
