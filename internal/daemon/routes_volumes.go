package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type volumeDto struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
}

func (d *Daemon) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	if config.IsDesktopMode() {
		d.handleListVolumesDesktop(w, r)
		return
	}
	volDir := d.volumesDir()
	entries, err := os.ReadDir(volDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []any{})
			return
		}
		writeInternalError(w, err)
		return
	}
	var result []volumeDto
	for _, e := range entries {
		if e.IsDir() {
			result = append(result, volumeDto{
				Name: e.Name(),
				Path: filepath.Join(volDir, e.Name()),
			})
		}
	}
	if result == nil {
		result = []volumeDto{}
	}
	writeJSON(w, result)
}

// handleListVolumesDesktop lists Docker named volumes scoped to the current
// (workspace, namespace) — desktop mode stores volumes as Docker named volumes
// (not host bind mounts) so the filesystem path returned in `path` is the
// container engine's Mountpoint (informational only; the user cannot navigate
// there outside Docker tooling).
func (d *Daemon) handleListVolumesDesktop(w http.ResponseWriter, r *http.Request) {
	if d.dockerClient == nil {
		writeJSON(w, []any{})
		return
	}
	vols, err := d.dockerClient.ListVolumes(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	result := make([]volumeDto, 0, len(vols))
	for _, v := range vols {
		result = append(result, volumeDto{
			Name: v.Name,
			Path: v.MountPoint,
			Size: v.Size,
		})
	}
	writeJSON(w, result)
}

func (d *Daemon) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.runtime != nil {
		status := d.runtime.Status()
		if status != namespace.NsStatusStopped {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete volume while namespace is running — stop the namespace first")
			return
		}
	}
	if config.IsDesktopMode() {
		if d.dockerClient == nil {
			writeError(w, http.StatusNotFound, "volume not found")
			return
		}
		if err := d.dockerClient.RemoveVolume(r.Context(), name); err != nil {
			// Docker returns 404 for unknown volumes; surface that to the UI
			// instead of a generic 500 so the user sees "volume not found".
			if isNotFoundErr(err) {
				writeError(w, http.StatusNotFound, "volume not found")
				return
			}
			writeInternalError(w, err)
			return
		}
		writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Volume %s deleted", name)})
		return
	}
	volPath := filepath.Join(d.volumesDir(), name)
	if _, err := os.Stat(volPath); err != nil { //nolint:gosec // G703: name is validated by validateAppName above
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	if err := os.RemoveAll(volPath); err != nil { //nolint:gosec // G703: name is validated by validateAppName above
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Volume %s deleted", name)})
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	// docker SDK wraps "No such volume" responses in errdefs.NotFound; checking
	// the error string is brittle but avoids pulling in errdefs just for one route.
	type notFounder interface{ NotFound() bool }
	var nf notFounder
	if errors.As(err, &nf) {
		return nf.NotFound()
	}
	return false
}
