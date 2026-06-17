package h2migrate

import (
	"encoding/json"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// Why a translation layer: Kotlin's ApplicationDef differs from Go's wire shape
// in three ways that would silently corrupt user-edited app definitions on
// import:
//   - `kind` is a string enum in Kotlin (CITECK_CORE / THIRD_PARTY / ...) but
//     an int constant in Go.
//   - `dependsOn` is a JSON array in both Kotlin (Set<String>) and Go
//     (appdef.StringSet, which marshals as a sorted list); we still translate
//     element-by-element to drop empties and re-key into the Go set.
//   - `initActions` uses Jackson polymorphic typing in Kotlin
//     (`{"type":"exec-shell","command":"..."}`) but a flat exec-list shape in
//     Go (`{"exec":["sh","-c","..."]}`).
//
// We keep the Go shape unchanged (it is the runtime contract) and translate at
// the migration boundary.

// kotlinApplicationDef mirrors the Jackson-serialized shape produced by
// Kotlin's ApplicationDef.Builder; only fields we forward into Go are listed.
type kotlinApplicationDef struct {
	Name               string                    `json:"name"`
	Image              string                    `json:"image"`
	Environments       map[string]string         `json:"environments"`
	Cmd                []string                  `json:"cmd"`
	Ports              []string                  `json:"ports"`
	Volumes            []string                  `json:"volumes"`
	VolumesContentHash string                    `json:"volumesContentHash"`
	InitActions        []kotlinAppInitAction     `json:"initActions"`
	DependsOn          []string                  `json:"dependsOn"`
	StartupConditions  []appdef.StartupCondition `json:"startupConditions"`
	LivenessProbe      *appdef.AppProbeDef       `json:"livenessProbe"`
	Resources          *appdef.AppResourcesDef   `json:"resources"`
	Kind               string                    `json:"kind"`
	ShmSize            string                    `json:"shmSize"`
	InitContainers     []kotlinInitContainer     `json:"initContainers"`
}

// kotlinAppInitAction matches Jackson polymorphic AppInitAction (sealed class
// + JsonTypeInfo PROPERTY=type, NAME=exec-shell).
type kotlinAppInitAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// kotlinInitContainer mirrors the InitContainerDef Jackson shape used in v1.x.
// Volumes is a list of strings (Kotlin uses List<String> for bind mounts);
// `kind` is the same string enum as ApplicationDef.kind.
type kotlinInitContainer struct {
	Image        string            `json:"image"`
	Environments map[string]string `json:"environments"`
	Volumes      []string          `json:"volumes"`
	Kind         string            `json:"kind"`
	Cmd          []string          `json:"cmd"`
}

// kotlinKindToGo maps the Kotlin enum name to the Go ApplicationKind constant.
// Unknown values fall back to KindThirdParty (matches Kotlin Builder default).
func kotlinKindToGo(name string) appdef.ApplicationKind {
	switch name {
	case "CITECK_CORE":
		return appdef.KindCiteckCore
	case "CITECK_CORE_EXTENSION":
		return appdef.KindCiteckCoreExtension
	case "CITECK_ADDITIONAL":
		return appdef.KindCiteckAdditional
	case "THIRD_PARTY", "":
		return appdef.KindThirdParty
	}
	return appdef.KindThirdParty
}

// decodeKotlinApplicationDef parses a Jackson-shaped ApplicationDef JSON blob
// into Go's appdef.ApplicationDef. The Go contract is preserved verbatim;
// only the wire shape is rewritten.
func decodeKotlinApplicationDef(data []byte) (appdef.ApplicationDef, error) {
	var k kotlinApplicationDef
	if err := json.Unmarshal(data, &k); err != nil {
		return appdef.ApplicationDef{}, fmt.Errorf("unmarshal kotlin appdef: %w", err)
	}
	out := appdef.ApplicationDef{
		Name:               k.Name,
		Image:              k.Image,
		Environments:       appdef.OrderedMapFromMap(k.Environments),
		Cmd:                k.Cmd,
		Ports:              k.Ports,
		Volumes:            k.Volumes,
		VolumesContentHash: k.VolumesContentHash,
		StartupConditions:  k.StartupConditions,
		LivenessProbe:      k.LivenessProbe,
		Resources:          k.Resources,
		Kind:               kotlinKindToGo(k.Kind),
		ShmSize:            k.ShmSize,
	}
	if len(k.DependsOn) > 0 {
		out.DependsOn = make(appdef.StringSet, 0, len(k.DependsOn))
		for _, dep := range k.DependsOn {
			if dep != "" {
				out.DependsOn.Add(dep)
			}
		}
	}
	if len(k.InitActions) > 0 {
		out.InitActions = make([]appdef.AppInitAction, 0, len(k.InitActions))
		for _, ia := range k.InitActions {
			// Kotlin's only sealed-class variant is ExecShell; the command is a
			// single shell string, so we wrap it in sh -c on the Go side to
			// preserve semantics (NamespaceGenerator does the same for new
			// init actions — see generator.go init container builder).
			if ia.Command == "" {
				continue
			}
			out.InitActions = append(out.InitActions, appdef.AppInitAction{
				Exec: []string{"sh", "-c", ia.Command},
			})
		}
	}
	if len(k.InitContainers) > 0 {
		out.InitContainers = make([]appdef.InitContainerDef, 0, len(k.InitContainers))
		for _, ic := range k.InitContainers {
			out.InitContainers = append(out.InitContainers, appdef.InitContainerDef{
				Image:        ic.Image,
				Environments: appdef.OrderedMapFromMap(ic.Environments),
				Volumes:      ic.Volumes,
				Kind:         kotlinKindToGo(ic.Kind),
				Cmd:          ic.Cmd,
			})
		}
	}
	return out, nil
}
