package namespace

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
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

// HeapDumpJavaOpts is appended to every Citeck webapp's JAVA_OPTS so that a Java
// OutOfMemoryError leaves evidence behind without anyone having to prepare for
// it. The dump lands in the export dir, i.e. on the host, and survives the
// container that produced it.
//
// GzipLevel=1 is not a detail: an ungzipped dump is as large as the live heap
// (4 GiB for the bigger webapps), and it is written at the worst possible moment
// for disk pressure. Level 1 is the cheapest setting that still cuts it several
// times over — measured 1.34 MB for a 32 MiB heap.
//
// What this does NOT catch: a container OOM-kill. The cgroup limit is enforced
// by the kernel against the whole container, and the JVM never gets to run a
// handler — so an app that dies from unbounded direct memory or metaspace leaves
// nothing here. Only a Java-level OutOfMemoryError produces a dump.
const HeapDumpJavaOpts = "-XX:+HeapDumpOnOutOfMemoryError" +
	" -XX:HeapDumpPath=" + ExportMountPath +
	" -XX:HeapDumpGzipLevel=1"

// applyHeapDumpOnOOM appends HeapDumpJavaOpts to the app's JAVA_OPTS, unless the
// bundle or the user configured heap dumping themselves — an explicit
// HeapDumpPath elsewhere must win, or we would silently redirect it.
func applyHeapDumpOnOOM(app *AppBuilder) {
	opts, _ := app.Environments.Get("JAVA_OPTS")
	if strings.Contains(opts, "HeapDumpOnOutOfMemoryError") || strings.Contains(opts, "HeapDumpPath") {
		return
	}
	app.AddEnv("JAVA_OPTS", strings.TrimSpace(opts+" "+HeapDumpJavaOpts))
}

// RotateHeapDumps keeps exactly ONE heap dump per app — the newest — and frees
// the fixed path the next OutOfMemoryError will write to.
//
// Freeing the path is not tidy-up, it is required: HotSpot refuses to overwrite
// an existing dump ("Unable to create …: File exists" — measured), and
// HeapDumpPath has no timestamp placeholder (only %p, useless here since a fresh
// container hands the JVM the same pid every time). Without this the FIRST OOM
// would be the only one ever recorded and every later one — including the one an
// operator is watching right now — would be dropped with a line in the app log.
//
// Keeping only the newest is what makes a crash loop safe: a dump is the size of
// the live heap even gzipped, and an app that OOMs every two minutes would
// otherwise fill the disk by morning. The order matters — the older dumps are
// removed only while the newest one is still on disk, so there is never a moment
// with no evidence at all. At rest exactly one dump remains; between an OOM and
// the next container start there are two (the fresh one and the one it
// supersedes), which is the floor for any rename-based scheme.
//
// Returns the file kept (base name, empty if there were none) and how many were
// removed.
func RotateHeapDumps(volumesBase, appName string, now time.Time) (kept string, removed int) {
	dir := ExportDirFor(volumesBase, appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0 // no export dir yet — nothing to rotate
	}

	type dump struct {
		name    string
		modTime time.Time
		size    int64
	}
	dumps := make([]dump, 0, 2)
	for _, e := range entries {
		if e.IsDir() || !isHeapDumpName(e.Name()) {
			continue // not ours: a pg_dump, an operator's file, a subdirectory
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dumps = append(dumps, dump{name: e.Name(), modTime: info.ModTime(), size: info.Size()})
	}
	if len(dumps) == 0 {
		return "", 0
	}

	// Newest first; the name breaks ties so the choice stays deterministic when
	// a filesystem reports the same mtime for two files.
	slices.SortFunc(dumps, func(a, b dump) int {
		if c := b.modTime.Compare(a.modTime); c != 0 {
			return c
		}
		return strings.Compare(b.name, a.name)
	})

	newest := dumps[0]
	for _, d := range dumps[1:] {
		if err := os.Remove(filepath.Join(dir, d.name)); err != nil {
			slog.Warn("Failed to remove superseded heap dump", "app", appName, "file", d.name, "err", err)
			continue
		}
		slog.Info("Removed superseded heap dump", "app", appName, "file", d.name, "bytes", d.size)
		removed++
	}

	// Move the survivor off the path HotSpot will write to next. A dump that has
	// already been moved keeps its name — and with it the time it was TAKEN,
	// rather than the time it was last shuffled.
	base, ext, ok := splitHeapDumpName(newest.name)
	if !ok {
		return newest.name, removed
	}
	from := filepath.Join(dir, newest.name)
	to := filepath.Join(dir, base+"-"+now.UTC().Format("20060102T150405Z")+ext)
	if err := os.Rename(from, to); err != nil {
		slog.Warn("Failed to move heap dump off the write path", "app", appName, "file", newest.name, "err", err)
		return newest.name, removed
	}
	slog.Info("Kept latest heap dump", "app", appName, "file", filepath.Base(to), "bytes", newest.size)
	return filepath.Base(to), removed
}

// isHeapDumpName reports whether a file is a heap dump this package manages —
// either as HotSpot wrote it, or after being moved off the write path.
func isHeapDumpName(name string) bool {
	return strings.HasSuffix(name, ".hprof") || strings.HasSuffix(name, ".hprof.gz")
}

// rotatedDumpName matches a name this function has already rotated, so a dump
// does not collect a new timestamp on every container start (and stops carrying
// a stamp that says when it was last MOVED rather than when it was written).
var rotatedDumpName = regexp.MustCompile(`-\d{8}T\d{6}Z$`)

// splitHeapDumpName splits "java_pid7.hprof.gz" into ("java_pid7", ".hprof.gz").
// Only the names HotSpot itself writes are rotated — anything else in the export
// dir was put there by a user or an app and is none of our business.
func splitHeapDumpName(name string) (base, ext string, ok bool) {
	for _, suffix := range []string{".hprof.gz", ".hprof"} {
		trimmed, found := strings.CutSuffix(name, suffix)
		if !found {
			continue
		}
		if rotatedDumpName.MatchString(trimmed) {
			return "", "", false
		}
		return trimmed, suffix, true
	}
	return "", "", false
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
