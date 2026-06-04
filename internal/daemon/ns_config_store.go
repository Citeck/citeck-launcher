package daemon

import (
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/namespace"
)

// errNamespaceNotFound is returned by loadNamespaceConfigFromStore when no
// config row/file exists for the (ws, ns) pair.
var errNamespaceNotFound = errors.New("namespace config not found")

// persistNamespaceConfig is the SINGLE write path for namespace config. It
// validates the EXACT bytes to be stored (catching bad input and any
// serializer defect) and only then writes them, with the name denormalized
// from the parsed config. Every create/edit/raw-edit path must go through it.
func (d *Daemon) persistNamespaceConfig(wsID, nsID string, bytesToStore []byte) error {
	cfg, err := namespace.ValidateYAML(bytesToStore)
	if err != nil {
		return fmt.Errorf("invalid namespace config: %w", err)
	}
	if err := d.store.SaveNamespaceConfig(wsID, nsID, cfg.Name, string(bytesToStore)); err != nil {
		return fmt.Errorf("save namespace config: %w", err)
	}
	return nil
}

// loadNamespaceConfigFromStore reads + parses a namespace config from the
// store. Returns errNamespaceNotFound when absent.
func (d *Daemon) loadNamespaceConfigFromStore(wsID, nsID string) (*namespace.Config, error) {
	raw, ok, err := d.store.LoadNamespaceConfig(wsID, nsID)
	if err != nil {
		return nil, fmt.Errorf("load namespace config: %w", err)
	}
	if !ok {
		return nil, errNamespaceNotFound
	}
	cfg, err := namespace.ParseNamespaceConfig([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse namespace config: %w", err)
	}
	return cfg, nil
}
