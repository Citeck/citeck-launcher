package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/docker"
)

// imagePullState tracks an in-flight or finished explicit image pull. Stored in
// Daemon.imagePulls keyed by image ref.
type imagePullState struct {
	done bool
	err  string
}

// appImageRef returns the image ref for an app, preferring the live runtime def
// and falling back to the resolved app defs so the popup works on a stopped /
// never-started namespace too.
func (d *Daemon) appImageRef(name string) (string, bool) {
	if app := d.findApp(name); app != nil {
		return app.Def.Image, true
	}
	for _, def := range d.active().appDefs {
		if def.Name == name {
			return def.Image, true
		}
	}
	return "", false
}

// handleAppImageInspect returns the local image details for an app's image, plus
// the pulling/error state of any explicit pull in flight.
func (d *Daemon) handleAppImageInspect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	ref, ok := d.appImageRef(name)
	if !ok {
		writeAppNotFound(w, name)
		return
	}

	dto := api.AppImageDto{Ref: ref}
	if dc := d.activeDockerClient(); dc != nil && ref != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		info := dc.InspectImageInfo(ctx, ref)
		if info.Present {
			dto.Present = true
			dto.ID = info.ID
			dto.RepoDigests = info.RepoDigests
			dto.Size = info.Size
			dto.OS = info.OS
			dto.Architecture = info.Architecture
			dto.Created = info.Created
			d.imagePulls.Delete(ref) // present now → drop any stale pull state
		}
	}
	if !dto.Present {
		dto.Pulling, dto.PullError = d.imagePullStatus(ref)
	}
	writeJSON(w, dto)
}

// imagePullStatus reports the in-flight / last-result state of an explicit pull.
func (d *Daemon) imagePullStatus(ref string) (pulling bool, pullErr string) {
	v, loaded := d.imagePulls.Load(ref)
	if !loaded {
		return false, ""
	}
	ps, _ := v.(*imagePullState)
	if ps == nil {
		return false, ""
	}
	if ps.done {
		return false, ps.err
	}
	return true, ""
}

// handleAppImagePull explicitly pulls an app's image — even a release tag that
// is already present is re-pulled on request. Runs in the background (pulls can
// take minutes); the UI polls handleAppImageInspect for pulling/error/present.
func (d *Daemon) handleAppImagePull(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	ref, ok := d.appImageRef(name)
	if !ok {
		writeAppNotFound(w, name)
		return
	}
	if ref == "" {
		writeError(w, http.StatusBadRequest, "app has no image")
		return
	}
	dc := d.activeDockerClient()
	if dc == nil {
		writeError(w, http.StatusServiceUnavailable, "docker not available")
		return
	}

	// Idempotent: a pull already in flight for this ref → report started.
	if v, loaded := d.imagePulls.Load(ref); loaded {
		if ps, _ := v.(*imagePullState); ps != nil && !ps.done {
			writeJSON(w, api.ActionResultDto{Success: true, Message: "pull already in progress"})
			return
		}
	}
	d.imagePulls.Store(ref, &imagePullState{})

	auth := d.resolveRegistryAuth(ref)
	d.bgWg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		errMsg := ""
		if err := dc.PullImage(ctx, ref, auth); err != nil {
			errMsg = err.Error()
		}
		d.imagePulls.Store(ref, &imagePullState{done: true, err: errMsg})
		if errMsg == "" {
			// The pull may have replaced the local image (new digest under the
			// same tag). Regenerate so the hash-diff recreates any running app
			// using it — auto-update after an explicit pull. Best-effort: skip
			// if a reload is already running (it will pick up the new image).
			if d.reloadMu.TryLock() {
				defer d.reloadMu.Unlock()
				if err := d.doReload(); err != nil {
					slog.Warn("Reload after image pull failed", "err", err)
				}
			}
		}
	})
	writeJSON(w, api.ActionResultDto{Success: true, Message: "pull started"})
}

// resolveRegistryAuth resolves Docker registry credentials for an image using
// the active workspace's reusable-credential bindings, mirroring how the runtime
// authenticates its own pulls. Returns nil when no credentials apply.
func (d *Daemon) resolveRegistryAuth(image string) *docker.RegistryAuth {
	act := d.active()
	if act.workspaceConfig == nil {
		return nil
	}
	bindings, _ := d.store.ListRegistryBindings(act.workspaceID)
	return makeRegistryAuthFunc(act.workspaceConfig, d.secretReaderFunc(), bindings)(image)
}
