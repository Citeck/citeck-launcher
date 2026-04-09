package daemon

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

func (d *Daemon) handleListVolumes(w http.ResponseWriter, _ *http.Request) {
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
	type volumeDto struct {
		Name string `json:"name"`
		Path string `json:"path"`
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

func (d *Daemon) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	volPath := filepath.Join(d.volumesDir(), name)
	if _, err := os.Stat(volPath); err != nil { //nolint:gosec // G703: name is validated by validateAppName above
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	// Refuse deletion if namespace is running — volumes may be mounted in containers
	if d.runtime != nil {
		status := d.runtime.Status()
		if status != namespace.NsStatusStopped {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete volume while namespace is running — stop the namespace first")
			return
		}
	}
	if err := os.RemoveAll(volPath); err != nil { //nolint:gosec // G703: name is validated by validateAppName above
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Volume %s deleted", name)})
}
