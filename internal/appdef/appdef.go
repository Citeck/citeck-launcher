package appdef

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// ApplicationKind categorizes apps by their role.
type ApplicationKind int

// ApplicationKind constants categorize apps by their platform role.
const (
	KindCiteckCore          ApplicationKind = iota
	KindCiteckCoreExtension
	KindCiteckAdditional
	KindThirdParty
)

// IsCiteckApp returns true if the application kind is a Citeck platform app.
func (k ApplicationKind) IsCiteckApp() bool {
	return k == KindCiteckCore || k == KindCiteckCoreExtension || k == KindCiteckAdditional
}

// AppProbeDef defines a startup/liveness probe.
type AppProbeDef struct {
	Exec               *ExecProbeDef `json:"exec,omitempty" yaml:"exec,omitempty"`
	HTTP               *HTTPProbeDef `json:"http,omitempty" yaml:"http,omitempty"`
	InitialDelaySeconds int          `json:"initialDelaySeconds,omitempty" yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int          `json:"periodSeconds,omitempty" yaml:"periodSeconds,omitempty"`
	FailureThreshold    int          `json:"failureThreshold,omitempty" yaml:"failureThreshold,omitempty"`
	TimeoutSeconds      int          `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
}

// ExecProbeDef defines an exec-based probe.
type ExecProbeDef struct {
	Command []string `json:"command" yaml:"command"`
}

// HTTPProbeDef defines an HTTP-based probe.
type HTTPProbeDef struct {
	Path string `json:"path" yaml:"path"`
	Port int    `json:"port" yaml:"port"`
}

// StartupCondition defines how to detect app readiness.
type StartupCondition struct {
	Probe *AppProbeDef       `json:"probe,omitempty" yaml:"probe,omitempty"`
	Log   *LogStartupCondition `json:"log,omitempty" yaml:"log,omitempty"`
}

// LogStartupCondition detects readiness via log output pattern matching.
type LogStartupCondition struct {
	Pattern        string `json:"pattern" yaml:"pattern"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
}

// AppResourcesDef defines resource limits.
type AppResourcesDef struct {
	Limits LimitsDef `json:"limits" yaml:"limits"`
}

// LimitsDef defines resource limits (memory).
type LimitsDef struct {
	Memory string `json:"memory" yaml:"memory"`
}

// AppInitAction defines an action to run after container creation.
type AppInitAction struct {
	Exec    []string `json:"exec,omitempty" yaml:"exec,omitempty"`
	Trigger string   `json:"trigger,omitempty" yaml:"trigger,omitempty"` // "on_create" or "always"
}

// InitContainerDef defines an init container.
type InitContainerDef struct {
	Image        string            `json:"image" yaml:"image"`
	Environments map[string]string `json:"environments,omitempty" yaml:"environments,omitempty"`
	Volumes      []string          `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Kind         ApplicationKind   `json:"kind" yaml:"kind"`
	Cmd          []string          `json:"cmd,omitempty" yaml:"cmd,omitempty"`
}

// ApplicationDef is a fully resolved container definition.
type ApplicationDef struct {
	Name              string            `json:"name"`
	NetworkAliases    []string          `json:"networkAliases,omitempty"`
	Image             string            `json:"image"`
	ImageDigest       string            `json:"imageDigest,omitempty"` // Docker image digest for change detection
	Environments      map[string]string `json:"environments,omitempty"`
	Cmd               []string          `json:"cmd,omitempty"`
	Ports             []string          `json:"ports,omitempty"`
	Volumes           []string          `json:"volumes,omitempty"`
	VolumesContentHash string           `json:"volumesContentHash,omitempty"`
	InitActions       []AppInitAction   `json:"initActions,omitempty"`
	DependsOn         map[string]bool   `json:"dependsOn,omitempty"`
	StartupConditions []StartupCondition `json:"startupConditions,omitempty"`
	LivenessProbe     *AppProbeDef      `json:"livenessProbe,omitempty"`
	Resources         *AppResourcesDef  `json:"resources,omitempty"`
	Kind              ApplicationKind   `json:"kind"`
	ShmSize           string            `json:"shmSize,omitempty"`
	InitContainers    []InitContainerDef `json:"initContainers,omitempty"`
	IsInit            bool              `json:"-"` // true for init containers (no restart policy)
	StopTimeout       int               `json:"stopTimeout,omitempty" yaml:"stopTimeout,omitempty"` // seconds; 0 = default (10s webapps, 30s infra)
}

// GetHashInput returns the string used to compute the application definition hash (for debugging).
func (d *ApplicationDef) GetHashInput() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name=%s\n", d.Name)
	fmt.Fprintf(&b, "image=%s\n", d.Image)
	if d.ImageDigest != "" {
		fmt.Fprintf(&b, "imageDigest=%s\n", d.ImageDigest)
	}
	fmt.Fprintf(&b, "cmd=%s\n", strings.Join(d.Cmd, " "))
	fmt.Fprintf(&b, "shmSize=%s\n", d.ShmSize)
	fmt.Fprintf(&b, "vch=%s\n", d.VolumesContentHash)
	envKeys := make([]string, 0, len(d.Environments))
	for k := range d.Environments {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		fmt.Fprintf(&b, "env:%s=%s\n", k, d.Environments[k])
	}
	ports := append([]string(nil), d.Ports...)
	sort.Strings(ports)
	for _, p := range ports {
		fmt.Fprintf(&b, "port=%s\n", p)
	}
	for _, v := range d.Volumes {
		fmt.Fprintf(&b, "vol=%s\n", v)
	}
	depKeys := make([]string, 0, len(d.DependsOn))
	for k := range d.DependsOn {
		depKeys = append(depKeys, k)
	}
	sort.Strings(depKeys)
	for _, k := range depKeys {
		fmt.Fprintf(&b, "dep=%s\n", k)
	}
	for _, ia := range d.InitActions {
		fmt.Fprintf(&b, "initAction=%s\n", strings.Join(ia.Exec, " "))
	}
	for _, ic := range d.InitContainers {
		fmt.Fprintf(&b, "initContainer=%s\n", ic.Image)
	}
	if d.Resources != nil {
		fmt.Fprintf(&b, "mem=%s\n", d.Resources.Limits.Memory)
	}
	return b.String()
}

// GetHash computes a SHA-256 hash of the application definition for change detection.
func (d *ApplicationDef) GetHash() string {
	h := sha256.Sum256([]byte(d.GetHashInput()))
	return fmt.Sprintf("%x", h[:])
}

// App name constants.
const (
	AppProxy           = "proxy"
	AppGateway         = "gateway"
	AppEapps           = "eapps"
	AppEmodel          = "emodel"
	AppUiserv          = "uiserv"
	AppHistory         = "history"
	AppNotifications   = "notifications"
	AppTransformations = "transformations"
	AppEproc           = "eproc"
	AppPostgres        = "postgres"
	AppZookeeper       = "zookeeper"
	AppRabbitmq        = "rabbitmq"
	AppMongodb         = "mongo"
	AppMailhog         = "mailhog"
	AppKeycloak        = "keycloak"
	AppPgadmin         = "pgadmin"
	AppOnlyoffice      = "onlyoffice"
	AppAlfresco        = "alfresco"
	AppAlfPostgres     = "alf-postgres"
	AppAlfSolr         = "alf-solr"
	AppObserver        = "observer"
	AppObsPostgres     = "observer-postgres"
)
