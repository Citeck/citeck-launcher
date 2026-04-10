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

// actionSetting is a marker interface for settings whose Run() performs the
// whole action end-to-end (prompt, execute, report) and must bypass the
// diff/apply/confirm/reload flow that file-backed settings use. The
// canonical example is admin-password reset, which drives the keycloak
// admin API directly instead of mutating namespace.yml. runSingleSetting
// detects the marker via type assertion and returns immediately after Run().
type actionSetting interface {
	isActionSetting()
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
		&adminPasswordSetting{},
	}
}

// deepCopy creates a deep copy of any struct via JSON round-trip.
func deepCopy[T any](src *T) (*T, error) {
	data, err := json.Marshal(src)
	if err != nil {
		return nil, fmt.Errorf("marshal for deep copy: %w", err)
	}
	var cp T
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal deep copy: %w", err)
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
