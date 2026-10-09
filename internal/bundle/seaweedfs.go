package bundle

import (
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// SeaweedVolumeBase defaults to the declared name, without a generation suffix.
func (a AdditionalAppProps) SeaweedVolumeBase() string {
	if a.VolumeBase != "" {
		return a.VolumeBase
	}
	return strings.TrimSpace(a.Name)
}

var seaweedSettingName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

// Settings only tune a local mini process. Layout and service identity belong
// to the migration contract, never to operator-supplied flags.
func (a AdditionalAppProps) validateSeaweedDependency() error {
	if a.Type != AppTypeSeaweedFS {
		return nil
	}
	if !ValidSecretID(a.SeaweedVolumeBase()) {
		return fmt.Errorf("additionalApps[%q]: invalid dependency volumeBase", a.Name)
	}
	if len(a.Volumes) != 0 {
		return fmt.Errorf("additionalApps[%q]: SEAWEEDFS volumes are managed; use volumeBase", a.Name)
	}
	if len(a.Cmd) != 0 {
		return fmt.Errorf("additionalApps[%q]: SEAWEEDFS command is managed; use settings", a.Name)
	}
	for key, value := range a.Settings {
		if !seaweedSettingName.MatchString(key) || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("additionalApps[%q]: invalid SEAWEEDFS setting %q", a.Name, key)
		}
		switch key {
		case "dir", "ip", "ip.bind", "master.port", "master.port.grpc", "filer.port", "filer.port.grpc", "volume.port", "volume.port.grpc", "s3.port":
			return fmt.Errorf("additionalApps[%q]: SEAWEEDFS setting %q is managed by Launcher", a.Name, key)
		}
		if key == "admin.ui" {
			if _, err := strconv.ParseBool(value); err != nil {
				return fmt.Errorf("additionalApps[%q]: SEAWEEDFS admin.ui must be boolean", a.Name)
			}
		}
	}
	if len(a.InitActions)+len(a.InitContainers) > 0 {
		return fmt.Errorf("additionalApps[%q]: managed SEAWEEDFS cannot depend on init actions or containers", a.Name)
	}
	return nil
}

// WithSeaweedDefaults builds a fresh container profile without modifying the
// workspace. Settings are scalar YAML values decoded as strings, just like
// POSTGRES settings; each becomes one exec argument, never shell source.
func (a AdditionalAppProps) WithSeaweedDefaults() AdditionalAppProps {
	if a.Type != AppTypeSeaweedFS {
		return a
	}
	settings := map[string]string{
		"master.telemetry": "false", "master.volumeSizeLimitMB": "256",
		"disableHttp": "true", "webdav": "false", "admin.ui": "false",
		"s3.port.iceberg": "0", "s3.port.lance": "0",
	}
	maps.Copy(settings, a.Settings)
	// These flags cannot be overridden, including by in-memory callers.
	settings["dir"] = "/data"
	settings["ip"] = "127.0.0.1"
	a.Cmd = []string{"mini"}
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		a.Cmd = append(a.Cmd, "-"+key+"="+settings[key])
	}
	a.Environments = maps.Clone(a.Environments)
	if a.Environments == nil {
		a.Environments = map[string]string{}
	}
	if _, ok := a.Environments["GOMEMLIMIT"]; !ok {
		a.Environments["GOMEMLIMIT"] = "512MiB"
	}
	if a.Resources == nil {
		a.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	}
	if a.StopTimeout == 0 {
		a.StopTimeout = 15
	}
	if len(a.StartupConditions) == 0 {
		a.StartupConditions = []appdef.StartupCondition{{Probe: &appdef.AppProbeDef{
			HTTP: &appdef.HTTPProbeDef{Path: "/healthz", Port: 8333}, PeriodSeconds: 5, FailureThreshold: 60, TimeoutSeconds: 5,
		}}}
	}
	if a.LivenessProbe == nil {
		a.LivenessProbe = &appdef.AppProbeDef{HTTP: &appdef.HTTPProbeDef{Path: "/healthz", Port: 8333}, FailureThreshold: 7, TimeoutSeconds: 5}
	}
	return a
}

// SeaweedAdminEnabled reports whether settings explicitly enables the admin UI.
func (a AdditionalAppProps) SeaweedAdminEnabled() bool {
	enabled, _ := strconv.ParseBool(a.Settings["admin.ui"])
	return a.Type == AppTypeSeaweedFS && enabled
}
