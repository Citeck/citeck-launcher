package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Legacy state did not record its bundle repository. Bind it before changing
// the stored ref, while the previous repository is still known. State is saved
// first: a failed config write may leave useful metadata, never a mislabeled kit.
// Caller holds reloadMu, as on the surrounding namespace config write path.
func (d *Daemon) preserveCacheRepositoryBeforeChange(wsID, nsID string, before, after bundle.Ref) error {
	if before.IsEmpty() || before.Repo == after.Repo {
		return nil
	}
	act := d.active()
	if act.workspaceID == wsID && act.nsConfig != nil && act.nsConfig.ID == nsID &&
		act.runtime != nil && act.bundleDef != nil && !act.bundleDef.IsEmpty() {
		// The runtime owns live state. Persist under its lock, rather than
		// overwriting a snapshot that may have advanced since a store read.
		def := *act.bundleDef
		if def.SourceRepo == "" {
			def.SourceRepo = before.Repo
		}
		if err := act.runtime.SetCachedBundle(&def); err != nil {
			return fmt.Errorf("save previous bundle identity: %w", err)
		}
		return nil
	}
	raw, ok, err := d.store.LoadNamespaceState(wsID, nsID)
	if err != nil {
		return fmt.Errorf("read previous bundle identity: %w", err)
	}
	if !ok || raw == "" {
		return nil
	}
	var fields map[string]json.RawMessage
	if stateErr := json.Unmarshal([]byte(raw), &fields); stateErr != nil {
		return fmt.Errorf("parse previous bundle state: %w", stateErr)
	}
	var def *bundle.Def
	if data := fields["cachedBundle"]; len(data) > 0 {
		if bundleErr := json.Unmarshal(data, &def); bundleErr != nil {
			return fmt.Errorf("parse previous saved bundle: %w", bundleErr)
		}
	}
	if def == nil || def.IsEmpty() || def.SourceRepo != "" {
		return nil
	}
	// Modify only the metadata field; preserve unknown legacy bundle/state
	// fields as well as the denormalized namespace status.
	var cachedFields map[string]json.RawMessage
	if fieldsErr := json.Unmarshal(fields["cachedBundle"], &cachedFields); fieldsErr != nil {
		return fmt.Errorf("parse previous bundle fields: %w", fieldsErr)
	}
	cachedFields["sourceRepo"], err = json.Marshal(before.Repo)
	if err != nil {
		return fmt.Errorf("encode previous repository: %w", err)
	}
	fields["cachedBundle"], err = json.Marshal(cachedFields)
	if err != nil {
		return fmt.Errorf("encode previous bundle: %w", err)
	}
	updated, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode previous bundle state: %w", err)
	}
	rows, err := d.store.ListNamespaces(wsID)
	if err != nil {
		return fmt.Errorf("read previous namespace status: %w", err)
	}
	for _, row := range rows {
		if row.ID == nsID {
			if err := d.store.SaveNamespaceState(wsID, nsID, row.Status, string(updated)); err != nil {
				return fmt.Errorf("save previous bundle identity: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("previous namespace %q not found", nsID)
}
