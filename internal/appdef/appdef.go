package appdef

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// ApplicationKind categorizes apps by their role.
type ApplicationKind int

const (
	KindCiteckCore          ApplicationKind = iota
	KindCiteckCoreExtension
	KindCiteckAdditional
	KindThirdParty
)

func (k ApplicationKind) IsCiteckApp() bool {
	return k == KindCiteckCore || k == KindCiteckCoreExtension || k == KindCiteckAdditional
}

// AppProbeDef defines a startup/liveness probe.
type AppProbeDef struct {
	Exec               *ExecProbeDef `json:"exec,omitempty" yaml:"exec,omitempty"`
	HTTP               *HttpProbeDef `json:"http,omitempty" yaml:"http,omitempty"`
	InitialDelaySeconds int          `json:"initialDelaySeconds,omitempty" yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int          `json:"periodSeconds,omitempty" yaml:"periodSeconds,omitempty"`
	FailureThreshold    int          `json:"failureThreshold,omitempty" yaml:"failureThreshold,omitempty"`
	TimeoutSeconds      int          `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
}

func DefaultProbe() AppProbeDef {
	return AppProbeDef{
		InitialDelaySeconds: 5,
		PeriodSeconds:       10,
		FailureThreshold:    10000,
		TimeoutSeconds:      5,
	}
}

type ExecProbeDef struct {
	Command []string `json:"command" yaml:"command"`
}

type HttpProbeDef struct {
	Path string `json:"path" yaml:"path"`
	Port int    `json:"port" yaml:"port"`
}

// StartupCondition defines how to detect app readiness.
type StartupCondition struct {
	Probe *AppProbeDef       `json:"probe,omitempty" yaml:"probe,omitempty"`
	Log   *LogStartupCondition `json:"log,omitempty" yaml:"log,omitempty"`
}

type LogStartupCondition struct {
	Pattern        string `json:"pattern" yaml:"pattern"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
}

// AppResourcesDef defines resource limits.
type AppResourcesDef struct {
	Limits LimitsDef `json:"limits" yaml:"limits"`
}

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
}

// GetHash computes a SHA-256 hash of the application definition.
// Used to detect if a container needs recreation.
func (d *ApplicationDef) GetHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "name=%s\n", d.Name)
	fmt.Fprintf(h, "image=%s\n", d.Image)
	fmt.Fprintf(h, "cmd=%s\n", strings.Join(d.Cmd, " "))
	fmt.Fprintf(h, "shmSize=%s\n", d.ShmSize)
	fmt.Fprintf(h, "vch=%s\n", d.VolumesContentHash)

	// Sort environments for deterministic hash
	envKeys := make([]string, 0, len(d.Environments))
	for k := range d.Environments {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		fmt.Fprintf(h, "env:%s=%s\n", k, d.Environments[k])
	}

	sort.Strings(d.Ports)
	for _, p := range d.Ports {
		fmt.Fprintf(h, "port=%s\n", p)
	}
	for _, v := range d.Volumes {
		fmt.Fprintf(h, "vol=%s\n", v)
	}

	// Dependencies
	depKeys := make([]string, 0, len(d.DependsOn))
	for k := range d.DependsOn {
		depKeys = append(depKeys, k)
	}
	sort.Strings(depKeys)
	for _, k := range depKeys {
		fmt.Fprintf(h, "dep=%s\n", k)
	}

	// Init actions
	for _, ia := range d.InitActions {
		fmt.Fprintf(h, "initAction=%s\n", strings.Join(ia.Exec, " "))
	}

	// Init containers
	for _, ic := range d.InitContainers {
		fmt.Fprintf(h, "initContainer=%s\n", ic.Image)
	}

	// Resources
	if d.Resources != nil {
		fmt.Fprintf(h, "mem=%s\n", d.Resources.Limits.Memory)
	}

	return fmt.Sprintf("%x", h.Sum(nil))
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
)
