package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The export directory: one bind mount, present on EVERY generated container,
// for artifacts the container produces and a human wants off the box — a heap
// dump, a pg_dump, a thread dump, a log slice.
//
// It is the OUTPUT counterpart of the runtime files under `app/<name>/`. Those
// are inputs: the launcher generates them, their content feeds
// VolumesContentHash, and editing one recreates the container. Nothing written
// here can do that — computeVolumesContentHash hashes the GENERATED file map,
// not the disk, so a file that only ever exists at runtime is invisible to the
// deployment hash by construction. The separate directory (and the name) is what
// makes that rule legible to whoever adds the next mount.
//
// Adding the mount itself DOES change GetHashInput, so the release that
// introduces it recreates every container once.
const (
	// ExportDirName is the directory under the namespace runtime files dir that
	// holds every app's export directory.
	ExportDirName = "export"
	// ExportMountPath is where it appears inside the container.
	ExportMountPath = "/citeck/export"
	// ExportDirEnv tells anything running in the container where to write —
	// scripts in an image should not have to hardcode our layout.
	ExportDirEnv = "CITECK_EXPORT_DIR"
	// ExportDirPerm is 1777 on purpose: containers run as their image's user
	// (postgres 999, keycloak 1000, webapps root), and a directory owned by the
	// launcher's uid would silently be unwritable for most of them — the failure
	// would surface only when someone actually tried to export something. The
	// sticky bit keeps one container from removing another's files. The
	// directory lives inside the launcher's own data dir, not in a shared
	// location.
	ExportDirPerm os.FileMode = os.ModeSticky | 0o777
)

// attachExportDir gives one app its export mount and the env var pointing at it.
func attachExportDir(b *AppBuilder) {
	b.AddVolume(fmt.Sprintf("./%s/%s:%s", ExportDirName, b.Name, ExportMountPath))
	b.AddEnv(ExportDirEnv, ExportMountPath)
}

// isExportKey reports whether a runtime-files key belongs to the export tree.
// Used by computeVolumesContentHash to keep output out of the deployment hash.
func isExportKey(key string) bool {
	return strings.HasPrefix(key, ExportDirName+"/")
}

// ExportDirFor returns the host path of an app's export directory.
func ExportDirFor(volumesBase, appName string) string {
	return filepath.Join(volumesBase, ExportDirName, appName)
}

// EnsureExportDir creates an app's export directory with permissions every
// container user can write to.
//
// Docker creates a missing bind-mount source itself, but as root and with the
// daemon's umask — which is exactly the case that leaves a non-root container
// unable to write. Creating it here first is what makes the mount usable.
func EnsureExportDir(volumesBase, appName string) error {
	dir := ExportDirFor(volumesBase, appName)
	if err := os.MkdirAll(dir, ExportDirPerm); err != nil {
		return fmt.Errorf("create export dir %s: %w", dir, err)
	}
	// MkdirAll applies the umask, and an existing directory keeps its old mode,
	// so set it explicitly.
	if err := os.Chmod(dir, ExportDirPerm); err != nil {
		return fmt.Errorf("chmod export dir %s: %w", dir, err)
	}
	return nil
}
