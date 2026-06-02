package appdef

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ApplicationKind categorizes apps by their role.
type ApplicationKind int

// ApplicationKind constants categorize apps by their platform role.
const (
	KindCiteckCore ApplicationKind = iota
	KindCiteckCoreExtension
	KindCiteckAdditional
	KindThirdParty
)

// IsCiteckApp returns true if the application kind is a Citeck platform app.
func (k ApplicationKind) IsCiteckApp() bool {
	return k == KindCiteckCore || k == KindCiteckCoreExtension || k == KindCiteckAdditional
}

// String returns the Kotlin-parity enum name (CITECK_CORE / THIRD_PARTY / ...)
// used when serializing to the human-edited config YAML. Unknown values fall
// back to their numeric form so the round-trip stays lossless.
func (k ApplicationKind) String() string {
	switch k {
	case KindCiteckCore:
		return "CITECK_CORE"
	case KindCiteckCoreExtension:
		return "CITECK_CORE_EXTENSION"
	case KindCiteckAdditional:
		return "CITECK_ADDITIONAL"
	case KindThirdParty:
		return "THIRD_PARTY"
	default:
		return strconv.Itoa(int(k))
	}
}

// ParseApplicationKind maps an enum name back to its ApplicationKind. It also
// accepts the legacy numeric form (e.g. "0") so configs written by older
// builds keep parsing. Unknown names default to KindThirdParty, matching the
// H2-migration translator's treatment of unrecognized/empty kinds.
func ParseApplicationKind(s string) ApplicationKind {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CITECK_CORE":
		return KindCiteckCore
	case "CITECK_CORE_EXTENSION":
		return KindCiteckCoreExtension
	case "CITECK_ADDITIONAL":
		return KindCiteckAdditional
	case "THIRD_PARTY", "":
		return KindThirdParty
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return ApplicationKind(n)
		}
		return KindThirdParty
	}
}

// MarshalYAML emits the enum name so the config editor shows readable text
// (kind: CITECK_CORE) instead of an opaque integer.
func (k ApplicationKind) MarshalYAML() (any, error) {
	return k.String(), nil
}

// UnmarshalYAML accepts both the string name and the legacy integer form, so
// existing numeric configs and freshly edited string configs both load.
func (k *ApplicationKind) UnmarshalYAML(value *yaml.Node) error {
	if n, err := strconv.Atoi(strings.TrimSpace(value.Value)); err == nil {
		*k = ApplicationKind(n)
		return nil
	}
	*k = ParseApplicationKind(value.Value)
	return nil
}

// AppProbeDef defines a startup/liveness probe.
type AppProbeDef struct {
	Exec                *ExecProbeDef `json:"exec,omitempty" yaml:"exec,omitempty"`
	HTTP                *HTTPProbeDef `json:"http,omitempty" yaml:"http,omitempty"`
	InitialDelaySeconds int           `json:"initialDelaySeconds,omitempty" yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int           `json:"periodSeconds,omitempty" yaml:"periodSeconds,omitempty"`
	FailureThreshold    int           `json:"failureThreshold,omitempty" yaml:"failureThreshold,omitempty"`
	TimeoutSeconds      int           `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
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
	Probe *AppProbeDef         `json:"probe,omitempty" yaml:"probe,omitempty"`
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
	Exec []string `json:"exec,omitempty" yaml:"exec,omitempty"`
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
	Name               string             `json:"name" yaml:"name"`
	NetworkAliases     []string           `json:"networkAliases,omitempty" yaml:"networkAliases,omitempty"`
	Image              string             `json:"image" yaml:"image"`
	ImageDigest        string             `json:"imageDigest,omitempty" yaml:"imageDigest,omitempty"` // Docker image digest for change detection
	Environments       map[string]string  `json:"environments,omitempty" yaml:"environments,omitempty"`
	Cmd                []string           `json:"cmd,omitempty" yaml:"cmd,omitempty"`
	Ports              []string           `json:"ports,omitempty" yaml:"ports,omitempty"`
	Volumes            []string           `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	VolumesContentHash string             `json:"volumesContentHash,omitempty" yaml:"volumesContentHash,omitempty"`
	InitActions        []AppInitAction    `json:"initActions,omitempty" yaml:"initActions,omitempty"`
	DependsOn          map[string]bool    `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	StartupConditions  []StartupCondition `json:"startupConditions,omitempty" yaml:"startupConditions,omitempty"`
	LivenessProbe      *AppProbeDef       `json:"livenessProbe,omitempty" yaml:"livenessProbe,omitempty"`
	Resources          *AppResourcesDef   `json:"resources,omitempty" yaml:"resources,omitempty"`
	Kind               ApplicationKind    `json:"kind" yaml:"kind"`
	ShmSize            string             `json:"shmSize,omitempty" yaml:"shmSize,omitempty"`
	InitContainers     []InitContainerDef `json:"initContainers,omitempty" yaml:"initContainers,omitempty"`
	IsInit             bool               `json:"-" yaml:"-"`                                         // true for init containers (no restart policy)
	StopTimeout        int                `json:"stopTimeout,omitempty" yaml:"stopTimeout,omitempty"` // seconds; 0 = default (15s webapps, 30s infra)
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
	AppMailpit         = "mailpit"
	AppKeycloak        = "keycloak"
	AppPgadmin         = "pgadmin"
	AppOnlyoffice      = "onlyoffice"
	AppAlfresco        = "alfresco"
	AppAlfPostgres     = "alf-postgres"
	AppAlfSolr         = "alf-solr"
	AppObserver        = "observer"
	AppObsPostgres     = "observer-postgres"
	AppContent         = "content"
	AppAi              = "ai"
	AppSttSidecar      = "stt-sidecar"
)
