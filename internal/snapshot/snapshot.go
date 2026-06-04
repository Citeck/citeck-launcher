// Package snapshot implements volume backup/restore via ZIP archives.
//
// Snapshot format (compatible with Kotlin launcher):
//
//	snapshot.zip
//	├── meta.json         — NamespaceSnapshotMeta
//	├── postgres.tar.zst  — compressed tar of volume data
//	├── rabbitmq.tar.zst
//	└── ...
//
// Each volume is archived using a temporary `launcher-utils` container
// that provides tar/zstd/xz tools in a minimal Alpine image.
package snapshot

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
)

const (
	metaFileName   = "meta.json"
	compressionExt = "zst" // zstd by default
)

var launcherUtilsImage = config.UtilsImage()

// NamespaceSnapshotMeta is the top-level snapshot metadata.
type NamespaceSnapshotMeta struct {
	Volumes   []VolumeSnapshotMeta `json:"volumes"`
	CreatedAt time.Time            `json:"createdAt"`
}

// VolumeSnapshotMeta describes one volume in the snapshot.
type VolumeSnapshotMeta struct {
	Name     string `json:"name"`     // original volume name (from Docker label)
	RootStat string `json:"rootStat"` // "uid:gid|0755"
	DataFile string `json:"dataFile"` // "postgres.tar.zst"
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sanitizeFileName(name string) string {
	return unsafeChars.ReplaceAllString(name, "_")
}

// VolumeProgressFunc reports per-volume progress during Export/Import
// (distinct from ProgressFunc which reports byte-level download progress).
// current is 1-based volume index, total is the total volume count,
// volumeName identifies the volume being processed. Implementations
// must be cheap and non-blocking — callers invoke it on the hot path.
type VolumeProgressFunc func(current, total int, volumeName string)

// volumeOps is the subset of *docker.Client that snapshot export/import needs.
// Narrowing to an interface decouples the snapshot package from the concrete
// Docker client and lets tests substitute a fake.
type volumeOps interface {
	CreateVolume(ctx context.Context, originalName string) (string, error)
	ListVolumes(ctx context.Context) ([]docker.VolumeInfo, error)
	RunUtilsContainer(ctx context.Context, cmd, binds []string) (output string, exitCode int, err error)
	ImageExists(ctx context.Context, img string) bool
	PullImage(ctx context.Context, img string, auth *docker.RegistryAuth) error
}

// exportSource is one volume to archive: name is the snapshot volume name (the
// original, un-scoped name), sourceBind is the launcher-utils mount spec
// ("<host-or-volume>:/source:ro") the archiver reads from.
type exportSource struct {
	name       string
	sourceBind string
}

// exportSources enumerates the namespace's volumes to archive. Desktop mode
// reads the per-(ns,ws) scoped named Docker volumes the containers actually use
// (via ListVolumes); server mode scans the {volumesBase}/volumes/ bind dirs.
// This mirrors docker.CreateContainer's per-mode volume handling so a snapshot
// captures the data containers really mount.
func exportSources(ctx context.Context, dc volumeOps, volumesBase string) ([]exportSource, error) {
	if config.IsDesktopMode() {
		vols, err := dc.ListVolumes(ctx)
		if err != nil {
			return nil, fmt.Errorf("list volumes: %w", err)
		}
		out := make([]exportSource, 0, len(vols))
		for _, v := range vols {
			if v.OrigName == "" {
				continue // not a launcher-managed app volume — can't round-trip
			}
			out = append(out, exportSource{name: v.OrigName, sourceBind: v.Name + ":/source:ro"})
		}
		return out, nil
	}
	volumesDir := filepath.Join(volumesBase, "volumes")
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		return nil, fmt.Errorf("list volumes in %s: %w", volumesDir, err)
	}
	out := make([]exportSource, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, exportSource{name: e.Name(), sourceBind: filepath.Join(volumesDir, e.Name()) + ":/source:ro"})
		}
	}
	return out, nil
}

// Export creates a snapshot ZIP of all namespace volumes.
// The namespace MUST be stopped before calling this.
// volumesBase is the runtime directory containing volumes/ subdirectory.
func Export(ctx context.Context, dc volumeOps, outputPath, volumesBase string, progress VolumeProgressFunc) (*NamespaceSnapshotMeta, error) {
	sources, err := exportSources(ctx, dc, volumesBase)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no volumes found to export")
	}

	// Ensure launcher-utils image is available
	if utilsErr := ensureUtilsImage(ctx, dc); utilsErr != nil {
		return nil, utilsErr
	}

	// Create temp dir for export
	tmpDir, err := os.MkdirTemp("", "citeck-snapshot-export-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	meta := NamespaceSnapshotMeta{
		CreatedAt: time.Now(),
	}

	// Export each volume via launcher-utils container
	for idx, src := range sources {
		dataFile := sanitizeFileName(src.name) + ".tar." + compressionExt

		slog.Info("Exporting volume", "volume", src.name, "file", dataFile)

		if progress != nil {
			progress(idx+1, len(sources), src.name)
		}

		rootStat, exportErr := exportVolume(ctx, dc, src.sourceBind, filepath.Join(tmpDir, dataFile))
		if exportErr != nil {
			return nil, fmt.Errorf("export volume %s: %w", src.name, exportErr)
		}

		meta.Volumes = append(meta.Volumes, VolumeSnapshotMeta{
			Name:     src.name,
			RootStat: rootStat,
			DataFile: dataFile,
		})
	}

	// Write meta.json
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	if writeErr := os.WriteFile(filepath.Join(tmpDir, metaFileName), metaData, 0o644); writeErr != nil { //nolint:gosec // meta.json needs to be readable
		return nil, fmt.Errorf("write meta.json: %w", writeErr)
	}

	// Create ZIP archive
	if zipErr := createZip(outputPath, tmpDir); zipErr != nil {
		return nil, fmt.Errorf("create zip: %w", zipErr)
	}

	// Write SHA256 sidecar file
	hash, err := FileSHA256(outputPath)
	if err != nil {
		slog.Warn("Failed to compute snapshot SHA256", "err", err)
	} else {
		sha256Path := outputPath + ".sha256"
		if wErr := fsutil.AtomicWriteFile(sha256Path, []byte(hash+"  "+filepath.Base(outputPath)+"\n"), 0o644); wErr != nil {
			slog.Warn("Failed to write SHA256 sidecar", "err", wErr)
		}
	}

	slog.Info("Snapshot exported", "path", outputPath, "volumes", len(meta.Volumes))
	return &meta, nil
}

// Import restores volumes from a snapshot ZIP into bind-mount directories.
// The namespace MUST be stopped before calling this.
func Import(ctx context.Context, dc volumeOps, zipPath, volumesBase string, progress VolumeProgressFunc) (*NamespaceSnapshotMeta, error) {
	// Estimate needed space (3x ZIP size) and check available disk
	if zipInfo, err := os.Stat(zipPath); err == nil {
		needed := zipInfo.Size() * 3
		if avail := availableDiskSpace(volumesBase); avail > 0 && avail < needed {
			return nil, fmt.Errorf("insufficient disk space: need ~%d MB, available %d MB",
				needed/(1024*1024), avail/(1024*1024))
		}
	}

	// Extract ZIP to temp dir
	tmpDir, err := os.MkdirTemp("", "citeck-snapshot-import-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if extractErr := extractZip(zipPath, tmpDir); extractErr != nil {
		return nil, fmt.Errorf("extract zip: %w", extractErr)
	}

	// Read meta.json
	metaData, err := os.ReadFile(filepath.Join(tmpDir, metaFileName)) //nolint:gosec // G304: tmpDir is an internal temp directory
	if err != nil {
		return nil, fmt.Errorf("read meta.json: %w", err)
	}

	var meta NamespaceSnapshotMeta
	if unmarshalErr := json.Unmarshal(metaData, &meta); unmarshalErr != nil {
		return nil, fmt.Errorf("parse meta.json: %w", unmarshalErr)
	}

	if len(meta.Volumes) == 0 {
		return nil, fmt.Errorf("snapshot contains no volumes")
	}

	// Validate volume metadata from untrusted meta.json before any filesystem operations
	for _, vol := range meta.Volumes {
		if valErr := validateVolumeSnapshotMeta(vol); valErr != nil {
			return nil, valErr
		}
	}

	// Ensure launcher-utils image
	if utilsErr := ensureUtilsImage(ctx, dc); utilsErr != nil {
		return nil, utilsErr
	}

	// Import each volume
	for idx, vol := range meta.Volumes {
		tarPath := filepath.Join(tmpDir, vol.DataFile)
		if _, err := os.Stat(tarPath); err != nil {
			return nil, fmt.Errorf("volume data file %s not found in snapshot", vol.DataFile)
		}

		slog.Info("Importing volume", "name", vol.Name, "file", vol.DataFile)

		if progress != nil {
			progress(idx+1, len(meta.Volumes), vol.Name)
		}

		if err := importVolume(ctx, dc, vol, tarPath, volumesBase); err != nil {
			return nil, fmt.Errorf("import volume %s: %w", vol.Name, err)
		}
	}

	slog.Info("Snapshot imported", "volumes", len(meta.Volumes))
	return &meta, nil
}

// exportVolume archives a single volume directory using launcher-utils.
// hostPath is the absolute path to the volume directory on the host.
// Returns rootStat string ("uid:gid|0perms").
func exportVolume(ctx context.Context, dc volumeOps, sourceBind, outputPath string) (string, error) {
	destDir := filepath.Dir(outputPath)
	dataFile := filepath.Base(outputPath)

	// Determine tar compression flag
	tarFlag := "--zstd"
	if strings.HasSuffix(dataFile, ".tar.xz") {
		tarFlag = "--xz"
	}

	// Command: archive volume contents + capture root ownership
	cmd := []string{"sh", "-c", fmt.Sprintf(
		`cd /source && find . -mindepth 1 -printf '%%P\n' | tar %s -cvf "/dest/%s" -T - 2>/dev/null; stat -c "%%u:%%g|0%%a" /source`,
		tarFlag, dataFile,
	)}

	output, exitCode, err := dc.RunUtilsContainer(ctx, cmd, []string{
		sourceBind,
		destDir + ":/dest",
	})
	if err != nil {
		return "", fmt.Errorf("run export container: %w", err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("export container exited with code %d: %s", exitCode, output)
	}

	// Parse rootStat from the last line of output
	rootStat := "0:0|0755" // default
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 {
		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if strings.Contains(lastLine, ":") && strings.Contains(lastLine, "|") {
			rootStat = lastLine
		}
	}

	// Verify the tar file was created
	if _, err := os.Stat(outputPath); err != nil {
		return "", fmt.Errorf("tar file not created: %w", err)
	}

	return rootStat, nil
}

// importVolume restores a single volume from a tar archive into a bind-mount directory.
func importVolume(ctx context.Context, dc volumeOps, vol VolumeSnapshotMeta, tarPath, volumesBase string) error {
	// Resolve the restore target. Desktop containers mount a per-(ns,ws)
	// scoped named Docker volume (see docker.CreateContainer's desktop branch),
	// so restore INTO that volume — restoring to a {volumesBase}/volumes/<name>
	// bind dir (the server layout) would land in a directory no desktop
	// container mounts, and the imported data would be invisible. Server mode
	// keeps the bind-mount dir.
	var destBind string
	if config.IsDesktopMode() {
		scopedName, err := dc.CreateVolume(ctx, vol.Name)
		if err != nil {
			return fmt.Errorf("create volume %s: %w", vol.Name, err)
		}
		destBind = scopedName + ":/dest"
	} else {
		hostDir := filepath.Join(volumesBase, "volumes", vol.Name)
		if err := os.MkdirAll(hostDir, 0o755); err != nil { //nolint:gosec // G301: volume dirs need 0o755 for Docker access
			return fmt.Errorf("create volume dir %s: %w", hostDir, err)
		}
		destBind = hostDir + ":/dest"
	}

	// Parse rootStat — validate format to prevent shell injection from crafted snapshots.
	// Expected format: "uid:gid|0nnn" (e.g. "999:999|0755")
	chownCmd := ""
	chmodCmd := ""
	if vol.RootStat != "" {
		parts := strings.SplitN(vol.RootStat, "|", 2)
		if len(parts) == 2 && isValidChown(parts[0]) && isValidChmod(parts[1]) {
			chownCmd = fmt.Sprintf("chown %s /dest && ", parts[0])
			chmodCmd = fmt.Sprintf("chmod %s /dest && ", parts[1])
		}
	}

	// Determine tar decompression flag
	tarFlag := "--zstd"
	if strings.HasSuffix(vol.DataFile, ".tar.xz") {
		tarFlag = "--xz"
	}

	tarDir := filepath.Dir(tarPath)
	tarFile := filepath.Base(tarPath)

	cmd := []string{"sh", "-c", fmt.Sprintf(
		`%s%star %s -xf "/source/%s" -C /dest`,
		chownCmd, chmodCmd, tarFlag, tarFile,
	)}

	output, exitCode, err := dc.RunUtilsContainer(ctx, cmd, []string{
		destBind,
		tarDir + ":/source:ro",
	})
	if err != nil {
		return fmt.Errorf("run import container: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("import container exited with code %d: %s", exitCode, output)
	}

	return nil
}

// ensureUtilsImage pulls the launcher-utils image if not present.
func ensureUtilsImage(ctx context.Context, dc volumeOps) error {
	if dc.ImageExists(ctx, launcherUtilsImage) {
		return nil
	}
	slog.Info("Pulling launcher-utils image", "image", launcherUtilsImage)
	if err := dc.PullImage(ctx, launcherUtilsImage, nil); err != nil {
		return fmt.Errorf("pull utils image: %w", err)
	}
	return nil
}

// createZip creates a ZIP file from all files in srcDir.
func createZip(zipPath, srcDir string) error {
	f, err := os.Create(zipPath) //nolint:gosec // G304: zipPath is an internal snapshot path
	if err != nil {
		return fmt.Errorf("create zip %s: %w", zipPath, err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return fmt.Errorf("relative path %s: %w", path, relErr)
		}

		header, headerErr := zip.FileInfoHeader(info)
		if headerErr != nil {
			return fmt.Errorf("zip header for %s: %w", relPath, headerErr)
		}
		header.Name = relPath
		header.Method = zip.Store // already compressed (zstd/xz)

		writer, createErr := w.CreateHeader(header)
		if createErr != nil {
			return fmt.Errorf("create zip entry %s: %w", relPath, createErr)
		}

		file, openErr := os.Open(path) //nolint:gosec // G304: path comes from internal filesystem walk
		if openErr != nil {
			return fmt.Errorf("open %s: %w", path, openErr)
		}
		defer file.Close()

		if _, copyErr := io.Copy(writer, file); copyErr != nil {
			return fmt.Errorf("write %s to zip: %w", relPath, copyErr)
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk %s: %w", srcDir, walkErr)
	}
	return nil
}

// maxExtractSize is the aggregate extraction size limit (50 GB) to prevent zip bombs.
const maxExtractSize int64 = 50 << 30

// extractZip extracts a ZIP archive to destDir.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	var totalWritten int64

	for _, f := range r.File {
		// Security: prevent zip slip
		destPath := filepath.Join(destDir, f.Name) //nolint:gosec // zip slip prevented by prefix check below
		if !strings.HasPrefix(filepath.Clean(destPath)+string(os.PathSeparator), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip slip detected: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0o755) //nolint:gosec // G301: extraction dirs need 0o755
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil { //nolint:gosec // G301: extraction dirs need 0o755
			return fmt.Errorf("mkdir for %s: %w", f.Name, err)
		}

		outFile, err := os.Create(destPath) //nolint:gosec // G304: destPath is validated against zip slip above
		if err != nil {
			return fmt.Errorf("create %s: %w", destPath, err)
		}

		rc, err := f.Open()
		if err != nil {
			_ = outFile.Close()
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}

		// Limit per-file (10 GB) and aggregate (50 GB)
		remaining := maxExtractSize - totalWritten
		if remaining <= 0 {
			_ = rc.Close()
			_ = outFile.Close()
			return fmt.Errorf("zip extraction aborted: aggregate size exceeds %d GB", maxExtractSize>>30)
		}
		perFileLimit := min(remaining, int64(10<<30))
		n, err := io.Copy(outFile, io.LimitReader(rc, perFileLimit))
		totalWritten += n
		_ = rc.Close()
		_ = outFile.Close()
		if err != nil {
			return fmt.Errorf("extract %s: %w", f.Name, err)
		}
	}

	return nil
}

// validateVolumeSnapshotMeta rejects untrusted meta.json entries with path traversal
// or shell-injection characters before they reach any filesystem or shell operation.
func validateVolumeSnapshotMeta(vol VolumeSnapshotMeta) error {
	if vol.Name == "" || filepath.Base(vol.Name) != vol.Name {
		return fmt.Errorf("invalid volume name in snapshot: %q", vol.Name)
	}
	if sanitizeFileName(vol.Name) != vol.Name {
		return fmt.Errorf("volume name contains unsafe characters: %q", vol.Name)
	}
	if vol.DataFile == "" || filepath.Base(vol.DataFile) != vol.DataFile {
		return fmt.Errorf("invalid dataFile in snapshot: %q", vol.DataFile)
	}
	if sanitizeFileName(vol.DataFile) != vol.DataFile {
		return fmt.Errorf("dataFile contains unsafe characters: %q", vol.DataFile)
	}
	return nil
}

// isValidChown validates "uid:gid" format (digits only).
var chownPattern = regexp.MustCompile(`^\d+:\d+$`)

func isValidChown(s string) bool { return chownPattern.MatchString(s) }

// isValidChmod validates octal permission format (e.g. "0755", "755").
var chmodPattern = regexp.MustCompile(`^0?[0-7]{3,4}$`)

func isValidChmod(s string) bool { return chmodPattern.MatchString(s) }
