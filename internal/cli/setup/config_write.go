package setup

import (
	"errors"
	"fmt"
	"os"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// namespaceWriteThroughDaemon uses the daemon's validation/persistence path whenever it
// is available. A refusal or failed write never falls back to a local file.
// Ordinary PUT saves still require the existing explicit reload afterwards.
var namespaceWriteThroughDaemon = func(before, next *namespace.Config, data []byte) (handled bool, err error) {
	c, err := client.New(client.Options{})
	if err != nil {
		if os.Getenv("CITECK_HOST") != "" {
			return true, fmt.Errorf("connect to configured daemon: %w", err)
		}
		return false, nil
	}
	defer c.Close()
	if !c.IsRunning() {
		return true, errors.New(i18n.T("cli.daemonNotRunningStart"))
	}
	current, err := c.GetStoredNamespaceConfig()
	if err != nil {
		return true, fmt.Errorf("read daemon namespace config: %w", err)
	}
	actual, err := namespace.ParseNamespaceConfig([]byte(current))
	if err != nil {
		return true, fmt.Errorf("parse daemon namespace config: %w", err)
	}
	// The input was read locally: do not save it against a different active
	// namespace or a config changed while the prompt was open.
	expected, err := structToJSONMap(before)
	if err != nil {
		return true, err
	}
	found, err := structToJSONMap(actual)
	if err != nil {
		return true, err
	}
	if !jsonEqual(expected, found) {
		return true, errors.New("daemon namespace config differs from the edit input")
	}
	_, err = c.PutNamespaceConfig(data)
	if err != nil {
		return true, fmt.Errorf("put daemon namespace config: %w", err)
	}
	return true, nil
}

// writeNamespaceConfig is shared by setup settings and history undo. Online
// writes pass through daemon gates; offline writes apply the same pure policy.
// Ordinary writes preserve apply-only versus explicit-reload semantics.
func writeNamespaceConfig(path string, before, next *namespace.Config) error {
	data, err := namespace.MarshalNamespaceConfig(next)
	if err != nil {
		return fmt.Errorf("marshal namespace config: %w", err)
	}
	if handled, err := namespaceWriteThroughDaemon(before, next, data); handled {
		if err != nil {
			return fmt.Errorf("save namespace config through daemon: %w", err)
		}
		return nil
	}
	transition := namespace.CheckContentStorageTransition(before.ContentStorageType(), before.BundleRef, next)
	if transition.Downgrade || namespace.ExternalS3Downgrade(before.S3 != nil, next) {
		return errors.New(i18n.T("contentStorage.msg.downgrade"))
	}
	if transition.RequiresAvailability {
		return errors.New(i18n.T("cli.daemonNotRunningStart"))
	}
	// namespace.yml may carry plaintext credentials; retain owner-only mode.
	if err := fsutil.AtomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write namespace config: %w", err)
	}
	return nil
}
