package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// errNamespaceNotFound is returned by loadNamespaceConfigFromStore when no
// config row/file exists for the (ws, ns) pair.
var errNamespaceNotFound = errors.New("namespace config not found")

// errLauncherTooOld carries the refusal out of persistNamespaceConfig so each
// route can map it to its own response shape.
type errLauncherTooOld struct {
	bundleRef string
	needs     string
	current   string
}

func (e *errLauncherTooOld) Error() string {
	return fmt.Sprintf("bundle %s needs launcher %s, this is %s", e.bundleRef, e.needs, e.current)
}

// writeLauncherTooOldError renders the refusal in the request's language and
// writes it as HTTP 409 + api.ErrCodeLauncherTooOld. Shared by every write
// route (create, edit, upgrade, raw config PUT) that maps *errLauncherTooOld.
func (d *Daemon) writeLauncherTooOldError(w http.ResponseWriter, r *http.Request, floor *errLauncherTooOld) {
	writeErrorCode(w, http.StatusConflict, api.ErrCodeLauncherTooOld,
		strings.Join(d.translatorFor(r).RenderAll([]msg.Message{
			msg.New("bundle.msg.launcherTooOld",
				"bundle", floor.bundleRef, "needs", floor.needs, "current", floor.current),
		}), " "))
}

// launcherFloorRefusal resolves the bundle the config names and reports why it
// may not be written, or nil when it may.
//
// Resolution is OFFLINE: this runs on every config write, including a rename,
// and a write that waits on git is a worse bug than a floor noticed one sync
// late. A bundle that is not on disk yields no refusal — there is nothing to
// read, and the create path already refuses an unsynced LATEST with
// BUNDLE_NOT_SYNCED.
func (d *Daemon) launcherFloorRefusal(wsID string, cfg *namespace.Config) *errLauncherTooOld {
	if cfg == nil || cfg.BundleRef.IsEmpty() || d.version == "" {
		return nil
	}
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(d.secretReaderFunc())).
		WithLauncherVersion(d.version)
	resolver.SetOffline(true)
	res, err := resolver.Resolve(cfg.BundleRef)
	if err != nil || res == nil || res.Bundle == nil {
		return nil
	}
	if !res.Bundle.NeedsNewerLauncher(d.version) {
		return nil
	}
	return &errLauncherTooOld{
		bundleRef: cfg.BundleRef.String(),
		needs:     res.Bundle.MinLauncherVersion,
		current:   d.version,
	}
}

// persistNamespaceConfig is the SINGLE write path for namespace config. It
// validates the EXACT bytes to be stored (catching bad input and any
// serializer defect) and only then writes them, with the name denormalized
// from the parsed config. Every create/edit/raw-edit path must go through it.
func (d *Daemon) persistNamespaceConfig(wsID, nsID string, bytesToStore []byte) error {
	cfg, err := namespace.ValidateYAML(bytesToStore)
	if err != nil {
		return fmt.Errorf("invalid namespace config: %w", err)
	}
	if refusal := d.launcherFloorRefusal(wsID, cfg); refusal != nil {
		return refusal
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
