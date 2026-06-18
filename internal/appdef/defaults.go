package appdef

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// Kotlin 1.x constructor defaults that a hand-authored / migrated probe inherits
// when the field is omitted (AppProbeDef.kt). Go zero-values would otherwise turn
// an omitted field into 0 — e.g. failureThreshold:0 is a probe that fails on the
// first check. We apply them on DECODE only (the editor PUT and migrated/persisted
// JSON); the generators set these explicitly, so generator output is unchanged.
// Probes are not part of the deployment hash, so this never recreates containers.
const (
	probeDefaultInitialDelaySeconds   = 5
	probeDefaultPeriodSeconds         = 10
	probeDefaultFailureThreshold      = 10_000
	probeDefaultTimeoutSeconds        = 5
	logConditionDefaultTimeoutSeconds = 60
)

// WithProbeDefaults returns a copy of the def with the Kotlin probe / log-condition
// defaults filled for every probe — the same fields UnmarshalJSON/UnmarshalYAML
// apply on decode. A generator-built def is never decoded, so its probes keep
// Go zero-values (initialDelaySeconds:0, periodSeconds:0) that omitempty drops;
// a patched def, having round-tripped through ApplyAppDefPatch's json.Unmarshal,
// carries the defaults. Encoding both sides of the app-config diff through this
// makes the change gutter compare like-for-like instead of painting
// initialDelaySeconds/periodSeconds as spurious "added" lines on the patched side.
//
// Probe pointers are copied before mutation so a shared cached generated def
// (GeneratedDef returns shared nested pointers) is never mutated in place.
func (d ApplicationDef) WithProbeDefaults() ApplicationDef {
	if len(d.StartupConditions) > 0 {
		scs := make([]StartupCondition, len(d.StartupConditions))
		copy(scs, d.StartupConditions)
		for i := range scs {
			if scs[i].Probe != nil {
				p := *scs[i].Probe
				p.applyDefaults()
				scs[i].Probe = &p
			}
			if scs[i].Log != nil {
				l := *scs[i].Log
				l.applyDefaults()
				scs[i].Log = &l
			}
		}
		d.StartupConditions = scs
	}
	if d.LivenessProbe != nil {
		p := *d.LivenessProbe
		p.applyDefaults()
		d.LivenessProbe = &p
	}
	return d
}

func (p *AppProbeDef) applyDefaults() {
	if p.InitialDelaySeconds == 0 {
		p.InitialDelaySeconds = probeDefaultInitialDelaySeconds
	}
	if p.PeriodSeconds == 0 {
		p.PeriodSeconds = probeDefaultPeriodSeconds
	}
	if p.FailureThreshold == 0 {
		p.FailureThreshold = probeDefaultFailureThreshold
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = probeDefaultTimeoutSeconds
	}
}

// UnmarshalYAML decodes the probe then fills Kotlin defaults for omitted fields.
func (p *AppProbeDef) UnmarshalYAML(value *yaml.Node) error {
	type raw AppProbeDef
	var r raw
	if err := value.Decode(&r); err != nil {
		return err //nolint:wrapcheck // decode error is self-describing
	}
	*p = AppProbeDef(r)
	p.applyDefaults()
	return nil
}

// UnmarshalJSON decodes the probe then fills Kotlin defaults for omitted fields.
func (p *AppProbeDef) UnmarshalJSON(data []byte) error {
	type raw AppProbeDef
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err //nolint:wrapcheck // decode error is self-describing
	}
	*p = AppProbeDef(r)
	p.applyDefaults()
	return nil
}

func (c *LogStartupCondition) applyDefaults() {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = logConditionDefaultTimeoutSeconds
	}
}

// UnmarshalYAML decodes the log condition then fills the Kotlin default timeout.
func (c *LogStartupCondition) UnmarshalYAML(value *yaml.Node) error {
	type raw LogStartupCondition
	var r raw
	if err := value.Decode(&r); err != nil {
		return err //nolint:wrapcheck // decode error is self-describing
	}
	*c = LogStartupCondition(r)
	c.applyDefaults()
	return nil
}

// UnmarshalJSON decodes the log condition then fills the Kotlin default timeout.
func (c *LogStartupCondition) UnmarshalJSON(data []byte) error {
	type raw LogStartupCondition
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err //nolint:wrapcheck // decode error is self-describing
	}
	*c = LogStartupCondition(r)
	c.applyDefaults()
	return nil
}
