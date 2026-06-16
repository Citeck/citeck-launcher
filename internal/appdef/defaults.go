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
