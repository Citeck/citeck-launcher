package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// volSizeMu serializes per-volume size computations. RunUtilsContainer uses a
// fixed container name, so concurrent calls would collide (one would tear down
// the other's container). The user computes sizes one row at a time anyway.
var volSizeMu sync.Mutex

// handleVolumeSize computes the on-disk size of a SINGLE volume on demand. It is
// the lazy counterpart to the (now size-free) volume list: the Web UI calls it
// when the user clicks "Compute" on a row, so the size of only THAT volume is
// measured (via `du` in a utils container) — not every volume via /system/df.
func (d *Daemon) handleVolumeSize(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !config.IsDesktopMode() || d.dockerClient == nil {
		writeJSON(w, map[string]int64{"size": -1})
		return
	}
	volSizeMu.Lock()
	defer volSizeMu.Unlock()
	size, err := d.dockerClient.VolumeSize(r.Context(), name)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, map[string]int64{"size": size})
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
			// Idempotent delete: if the volume is already gone, the desired end
			// state is reached, so report success. The volume list (GET
			// /volumes) is slow on some engines (/system/df), so a deleted row
			// can linger for seconds and get clicked again — that retry must not
			// surface a scary 500 for a volume that is, in fact, already deleted.
			if isNotFoundErr(err) {
				writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Volume %s already removed", name)})
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
	// docker SDK wraps "No such volume" responses in errdefs.NotFound.
	type notFounder interface{ NotFound() bool }
	var nf notFounder
	if errors.As(err, &nf) && nf.NotFound() {
		return true
	}
	// Fallback: VolumeRemove can return a plain error ("Error response from
	// daemon: get <name>: no such volume") that does NOT implement the errdefs
	// NotFound interface, so the typed check above misses it. Match the message.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such volume") || strings.Contains(msg, "volume not found")
}
