package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type mcpSettingsEditKey struct{}
type mcpSettingsEdit struct {
	Settings *launchermcp.AppSettings
	Reset    bool
}

var mcpEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func effectiveMCPDef(rt *namespace.Runtime, name string) (baseline, effective appdef.ApplicationDef, failure error) {
	base, ok := rt.GeneratedDef(name)
	if !ok {
		return base, base, errors.New("APP_NOT_FOUND")
	}
	effective, err := namespace.ApplyAppDefPatch(base, rt.AppPatch(name))
	if err != nil {
		return base, effective, fmt.Errorf("merge effective app settings: %w", err)
	}
	return base, effective, nil
}
func settingsFromDef(def appdef.ApplicationDef) launchermcp.AppSettings {
	memory := ""
	cpu := float64(0)
	if def.Resources != nil {
		memory = def.Resources.Limits.Memory
		cpu = def.Resources.Limits.CPU
	}
	java, _ := def.Environments.Get("JAVA_OPTS")
	profiles, _ := def.Environments.Get("SPRING_PROFILES_ACTIVE")
	env := map[string]*string{}
	for _, e := range def.Environments {
		if e.Key == "JAVA_OPTS" || e.Key == "SPRING_PROFILES_ACTIVE" {
			continue
		}
		value := e.Value
		env[e.Key] = &value
	}
	ports := append([]string{}, def.Ports...)
	return launchermcp.AppSettings{Memory: &memory, CPU: &cpu, JavaOpts: &java, Profiles: &profiles, Environment: env, Ports: &ports}
}
func (d *Daemon) mcpAppSettings(ctx context.Context, act activeNamespace, tool string, a launchermcp.Arguments) (any, error) {
	if !safeIDPattern.MatchString(a.App) {
		return nil, errors.New("invalid app id")
	}
	if tool == "application_settings_read" {
		d.appEditMu.Lock()
		defer d.appEditMu.Unlock()
		_, effective, err := effectiveMCPDef(act.runtime, a.App)
		if err != nil {
			return nil, err
		}
		return settingsFromDef(effective), nil
	}
	if tool == "application_settings_write" && a.Settings == nil {
		return nil, errors.New("settings required")
	}
	edit := mcpSettingsEdit{Settings: a.Settings, Reset: tool == "application_settings_reset"}
	return d.mcpInvoke(context.WithValue(ctx, mcpSettingsEditKey{}, edit), d.handlePutAppConfig, http.MethodPut, "name", a.App, []byte("{}"))
}

// Called from the native edit handler only AFTER its appEditMu and long-op
// gates: merge against the latest baseline+patch, not a stale client full def.
//
//nolint:gocyclo // Explicit field allowlist and independent validation preserve forbidden fields.
func (d *Daemon) mergeMCPSettings(rt *namespace.Runtime, name string, edit mcpSettingsEdit) (appdef.ApplicationDef, error) {
	base, def, err := effectiveMCPDef(rt, name)
	if err != nil {
		return def, err
	}
	def.Environments = slices.Clone(def.Environments)
	if edit.Reset {
		def.Resources = base.Resources
		def.Environments = slices.Clone(base.Environments)
		// Never reintroduce a public binding removed by another editor. Existing
		// public bindings are retained verbatim; only loopback ones reset.
		ports := []string{}
		for _, p := range def.Ports {
			if !mcpLoopbackPort(p) {
				ports = append(ports, p)
			}
		}
		for _, p := range base.Ports {
			if mcpLoopbackPort(p) {
				ports = append(ports, p)
			}
		}
		def.Ports = ports
		return def, nil
	}
	s := edit.Settings
	if s == nil {
		return def, errors.New("settings required")
	}
	if s.Memory != nil || s.CPU != nil {
		resources := appdef.AppResourcesDef{}
		if def.Resources != nil {
			resources = *def.Resources
		}
		def.Resources = &resources
	}
	if s.Memory != nil {
		if *s.Memory != "" && docker.ParseMemory(*s.Memory) <= 0 {
			return def, errors.New("invalid memory limit")
		}
		def.Resources.Limits.Memory = *s.Memory
	}
	if s.CPU != nil {
		if *s.CPU <= 0 {
			return def, errors.New("CPU must be positive; use reset to remove the cap")
		}
		if _, err = appdef.NanoCPUs(*s.CPU); err != nil {
			return def, fmt.Errorf("mcp settings: %w", err)
		}
		def.Resources.Limits.CPU = *s.CPU
	}
	if s.JavaOpts != nil {
		def.Environments.Set("JAVA_OPTS", *s.JavaOpts)
	}
	if s.Profiles != nil {
		def.Environments.Set("SPRING_PROFILES_ACTIVE", *s.Profiles)
	}
	for k, v := range s.Environment {
		if !mcpEnvName.MatchString(k) || k == "JAVA_OPTS" || k == "SPRING_PROFILES_ACTIVE" {
			return def, fmt.Errorf("invalid or separately typed environment key %q", k)
		}
		if v == nil {
			def.Environments = slices.DeleteFunc(def.Environments, func(e appdef.OrderedEntry) bool { return e.Key == k })
		} else {
			if strings.ContainsRune(*v, '\x00') {
				return def, errors.New("environment value contains NUL")
			}
			def.Environments.Set(k, *v)
		}
	}
	if s.Ports != nil {
		for _, p := range *s.Ports {
			if !mcpLoopbackPort(p) && !slices.Contains(def.Ports, p) {
				return def, errors.New("MCP can only add literal loopback port bindings")
			}
		}
		// Preserve external bindings on every edit, even if the client omitted them.
		ports := slices.Clone(*s.Ports)
		for _, p := range def.Ports {
			if !mcpLoopbackPort(p) && !slices.Contains(ports, p) {
				ports = append(ports, p)
			}
		}
		def.Ports = ports
	}
	return def, nil
}
func mcpLoopbackPort(s string) bool {
	p, err := appdef.ParsePortSpec(s)
	return err == nil && !p.AllInterfaces && (!p.HostIP.IsValid() || p.HostIP.IsLoopback())
}
