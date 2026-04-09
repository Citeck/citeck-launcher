package setup

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
)

// TargetFile identifies which config file a setting modifies.
type TargetFile int

// TargetFile constants identify which config file a setting modifies.
const (
	NamespaceFile TargetFile = iota
	DaemonFile
)

// setupContext holds per-run mutable state passed through the call chain,
// replacing package-level variables.
type setupContext struct {
	PendingSecrets map[string]string
	CurrentApps    []string
}

// Setting is the interface that all setup settings implement.
type Setting interface {
	ID() string
	Title() string
	Description() string
	TargetFile() TargetFile
	CurrentValue(cfg *namespace.Config, dcfg *config.DaemonConfig) string
	Available(cfg *namespace.Config, apps []string) bool
	Run(ctx *setupContext, cfg *namespace.Config, dcfg *config.DaemonConfig) error
}

// allSettings returns the ordered list of all registered settings.
func allSettings() []Setting {
	return []Setting{
		&hostnameSetting{},
		&tlsSetting{},
		&portSetting{},
		&emailSetting{},
		&s3Setting{},
		&authSetting{},
		&resourcesSetting{},
		&languageSetting{},
	}
}

// deepCopyConfig creates a deep copy of a namespace Config via JSON round-trip.
func deepCopyConfig(cfg *namespace.Config) (*namespace.Config, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config for copy: %w", err)
	}
	var cp namespace.Config
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal config copy: %w", err)
	}
	return &cp, nil
}

// deepCopyDaemonConfig creates a deep copy of a DaemonConfig via JSON round-trip.
func deepCopyDaemonConfig(cfg *config.DaemonConfig) (*config.DaemonConfig, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal daemon config for copy: %w", err)
	}
	var cp config.DaemonConfig
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal daemon config copy: %w", err)
	}
	return &cp, nil
}

// isUserAborted checks if a huh error is a user cancellation (Esc/Ctrl+C).
func isUserAborted(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

// configFilePath returns the config file path for a target file.
func configFilePath(target TargetFile) string {
	if target == DaemonFile {
		return config.DaemonConfigPath()
	}
	return config.NamespaceConfigPath()
}
