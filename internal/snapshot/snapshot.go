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

	"github.com/niceteck/citeck-launcher/internal/docker"
)

const (
	launcherUtilsImage = "registry.citeck.ru/community/launcher-utils:1.0"
	metaFileName       = "meta.json"
	compressionExt     = "zst" // zstd by default
)

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

// Export creates a snapshot ZIP of all namespace volumes.
// The namespace MUST be stopped before calling this.
func Export(ctx context.Context, dc *docker.Client, outputPath string) (*NamespaceSnapshotMeta, error) {
	// List namespace volumes
	volumes, err := dc.ListNamespaceVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	if len(volumes) == 0 {
		return nil, fmt.Errorf("no volumes found for this namespace")
	}

	// Ensure launcher-utils image is available
	if err := ensureUtilsImage(ctx, dc); err != nil {
		return nil, err
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
	for _, vol := range volumes {
		volName := vol.Name
		origName := vol.Labels[docker.LabelOrigName]
		if origName == "" {
			// Derive original name from volume name (strip prefix)
			origName = volName
		}

		dataFile := sanitizeFileName(origName) + ".tar." + compressionExt

		slog.Info("Exporting volume", "volume", volName, "file", dataFile)

		rootStat, err := exportVolume(ctx, dc, volName, filepath.Join(tmpDir, dataFile))
		if err != nil {
			return nil, fmt.Errorf("export volume %s: %w", volName, err)
		}

		meta.Volumes = append(meta.Volumes, VolumeSnapshotMeta{
			Name:     origName,
			RootStat: rootStat,
			DataFile: dataFile,
		})
	}

	// Write meta.json
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, metaFileName), metaData, 0o644); err != nil {
		return nil, fmt.Errorf("write meta.json: %w", err)
	}

	// Create ZIP archive
	if err := createZip(outputPath, tmpDir); err != nil {
		return nil, fmt.Errorf("create zip: %w", err)
	}

	slog.Info("Snapshot exported", "path", outputPath, "volumes", len(meta.Volumes))
	return &meta, nil
}

// Import restores volumes from a snapshot ZIP.
// The namespace MUST be stopped before calling this.
func Import(ctx context.Context, dc *docker.Client, zipPath string) (*NamespaceSnapshotMeta, error) {
	// Extract ZIP to temp dir
	tmpDir, err := os.MkdirTemp("", "citeck-snapshot-import-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := extractZip(zipPath, tmpDir); err != nil {
		return nil, fmt.Errorf("extract zip: %w", err)
	}

	// Read meta.json
	metaData, err := os.ReadFile(filepath.Join(tmpDir, metaFileName))
	if err != nil {
		return nil, fmt.Errorf("read meta.json: %w", err)
	}

	var meta NamespaceSnapshotMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("parse meta.json: %w", err)
	}

	if len(meta.Volumes) == 0 {
		return nil, fmt.Errorf("snapshot contains no volumes")
	}

	// Ensure launcher-utils image
	if err := ensureUtilsImage(ctx, dc); err != nil {
		return nil, err
	}

	// Import each volume
	for _, vol := range meta.Volumes {
		tarPath := filepath.Join(tmpDir, vol.DataFile)
		if _, err := os.Stat(tarPath); err != nil {
			return nil, fmt.Errorf("volume data file %s not found in snapshot", vol.DataFile)
		}

		slog.Info("Importing volume", "name", vol.Name, "file", vol.DataFile)

		if err := importVolume(ctx, dc, vol, tarPath); err != nil {
			return nil, fmt.Errorf("import volume %s: %w", vol.Name, err)
		}
	}

	slog.Info("Snapshot imported", "volumes", len(meta.Volumes))
	return &meta, nil
}

// exportVolume archives a single Docker volume using launcher-utils.
// Returns rootStat string ("uid:gid|0perms").
func exportVolume(ctx context.Context, dc *docker.Client, volumeName, outputPath string) (string, error) {
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
		volumeName + ":/source:ro",
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

// importVolume restores a single volume from a tar archive.
func importVolume(ctx context.Context, dc *docker.Client, vol VolumeSnapshotMeta, tarPath string) error {
	// Create the Docker volume with labels (name determined by current workspace/namespace)
	volumeName := dc.VolumeNameForRestore(vol.Name)
	if err := dc.CreateLabeledVolume(ctx, volumeName, vol.Name); err != nil {
		return fmt.Errorf("create volume %s: %w", volumeName, err)
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
		volumeName + ":/dest",
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
func ensureUtilsImage(ctx context.Context, dc *docker.Client) error {
	if dc.ImageExists(ctx, launcherUtilsImage) {
		return nil
	}
	slog.Info("Pulling launcher-utils image", "image", launcherUtilsImage)
	return dc.PullImage(ctx, launcherUtilsImage)
}

// createZip creates a ZIP file from all files in srcDir.
func createZip(zipPath, srcDir string) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Store // already compressed (zstd/xz)

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

// extractZip extracts a ZIP archive to destDir.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// Security: prevent zip slip
		destPath := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(destPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip slip detected: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0o755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		// Limit decompressed size to prevent zip bombs (10 GB per file)
		_, err = io.Copy(outFile, io.LimitReader(rc, 10<<30))
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// isValidChown validates "uid:gid" format (digits only).
var chownPattern = regexp.MustCompile(`^\d+:\d+$`)

func isValidChown(s string) bool { return chownPattern.MatchString(s) }

// isValidChmod validates octal permission format (e.g. "0755", "755").
var chmodPattern = regexp.MustCompile(`^0?[0-7]{3,4}$`)

func isValidChmod(s string) bool { return chmodPattern.MatchString(s) }
