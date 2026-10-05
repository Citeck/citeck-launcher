package setup

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
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
	// Bundle and Workspace are the namespace's bundle and workspace config as
	// resolved OFFLINE when setup started; Bundle is nil when the bundle could
	// not be read from disk. Read by the settings whose availability depends
	// on more than the app list (content-storage).
	Bundle    *bundle.Def
	Workspace *bundle.WorkspaceConfig
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

// daemonAppliedSetting is a marker interface for settings whose change the
// DAEMON must write and apply, because it acts beyond the file: today the
// content-storage switch, which the daemon re-attaches the S3 provider for and
// reloads, under its own write gate. runSingleSetting keeps the diff,
// validation, confirmation and history for it, but hands the write to
// applyThroughDaemon (content_storage.go) instead of writing namespace.yml
// itself, and offers no "apply config only" — the daemon applies it at once.
type daemonAppliedSetting interface {
	isDaemonAppliedSetting()
}

// allSettings returns the ordered list of all registered settings.
func allSettings() []Setting {
	return []Setting{
		&hostnameSetting{},
		&tlsSetting{},
		&portSetting{},
		&emailSetting{},
		&s3Setting{},
		&contentStorageSetting{},
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

// isUserAborted checks if an error is a user cancellation (Esc/Ctrl+C).
func isUserAborted(err error) bool {
	return errors.Is(err, prompt.ErrCanceled)
}

// hints returns localized key hints for prompt primitives. Falls back to
// empty Hints (which Run() fills with English defaults) when i18n is not
// yet loaded. Thin shim over prompt.HintsFromT to keep the local call
// sites terse.
func hints() prompt.Hints { return prompt.HintsFromT(i18n.T) }

// configFilePath returns the config file path for a target file.
func configFilePath(target TargetFile) string {
	if target == DaemonFile {
		return config.DaemonConfigPath()
	}
	return config.NamespaceConfigPath()
}

// backValue is the sentinel option value for "← Back" in all setup select menus.
const backValue = "_back"
