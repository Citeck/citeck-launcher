package namespace

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExportDirOnEveryApp: the export mount is not a JVM feature. A pg_dump from
// postgres, a rabbitmqctl report, a heap dump from a webapp — all of them need
// somewhere to put a file that the launcher can then hand to a human, so every
// generated container gets the same directory in the same place.
func TestExportDirOnEveryApp(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Observer:       ObserverProps{Enabled: true, Image: "citeck/observer:1.0"},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {Image: "nexus.citeck.ru/emodel:1.0"}}}
	wsCfg := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}}}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Applications)

	for _, app := range resp.Applications {
		want := "./" + ExportDirName + "/" + app.Name + ":" + ExportMountPath
		assert.Containsf(t, app.Volumes, want, "app %s must mount its export dir", app.Name)
		exportEnv, ok := app.Environments.Get(ExportDirEnv)
		assert.Truef(t, ok, "app %s must be told where to export to", app.Name)
		assert.Equalf(t, ExportMountPath, exportEnv, "app %s: wrong export path", app.Name)
	}
}

// TestExportContentDoesNotAffectDeploymentHash is the property the whole split
// between `app/<name>/` (input) and `export/<name>/` (output) exists to protect:
// a container writing its own artifacts must never trigger its own recreate.
//
// It holds by construction — computeVolumesContentHash hashes the GENERATED file
// map rather than the disk — and this test is here so that a future change to
// hash on-disk content fails loudly instead of turning every heap dump into a
// container restart.
func TestExportContentDoesNotAffectDeploymentHash(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {Image: "nexus.citeck.ru/emodel:1.0"}}}
	wsCfg := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}}}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	before := app.GetHash()

	// A runtime artifact appears in the export dir (and nowhere in the generated
	// file map, which is the point).
	files := make(map[string][]byte, len(resp.Files))
	maps.Copy(files, resp.Files)
	files[ExportDirName+"/emodel/heap.hprof.gz"] = []byte("pretend this is 2 GiB")

	app.VolumesContentHash = computeVolumesContentHash(app, files)
	assert.Equal(t, before, app.GetHash(), "a file in the export dir must not change the deployment hash")

	// Belt: nothing the generator produces lives under export/ in the first
	// place, so runtime artifacts cannot reach the hash even without the filter.
	for key := range resp.Files {
		assert.Falsef(t, isExportKey(key), "generated file %q must not live in the export tree", key)
	}
	// Braces: a props file still counts — the export filter must not have
	// widened into "stop hashing bind mounts".
	files["app/emodel/props/application-launcher.yml"] = []byte("changed")
	app.VolumesContentHash = computeVolumesContentHash(app, files)
	assert.NotEqual(t, before, app.GetHash(), "an edited props file must still recreate the container")
}

// The mount is written relative ("./export/<app>") so it resolves against the
// namespace runtime dir in both server and desktop layouts — an absolute path
// baked at generation time would not survive the two.
func TestExportMountIsRelative(t *testing.T) {
	b := &AppBuilder{Name: "postgres"}
	attachExportDir(b)
	require.Len(t, b.Volumes, 1)
	assert.True(t, strings.HasPrefix(b.Volumes[0], "./"), "got %q", b.Volumes[0])
}

// EnsureExportDir must leave a directory every container user can write to:
// postgres runs as uid 999, keycloak as 1000, webapps as root, and the launcher
// as none of those.
func TestEnsureExportDirIsWritableByAnyUser(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, EnsureExportDir(base, "postgres"))

	dir := ExportDirFor(base, "postgres")
	assert.Equal(t, filepath.Join(base, ExportDirName, "postgres"), dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o777), info.Mode().Perm(), "any container uid must be able to write")
	assert.NotZero(t, info.Mode()&os.ModeSticky, "sticky bit keeps one app from deleting another's exports")

	// Idempotent, and it repairs the mode of a directory that already exists
	// (e.g. one Docker created itself as root before this ran).
	require.NoError(t, os.Chmod(dir, 0o700))
	require.NoError(t, EnsureExportDir(base, "postgres"))
	info, err = os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o777), info.Mode().Perm())
}

// TestHeapDumpOnOOMIsEnabledByDefault: an OutOfMemoryError is the one failure
// where the evidence has to be collected at the moment it happens — nobody gets
// to attach afterwards. So the flags ship on by default rather than as
// something an operator must have thought of in advance.
func TestHeapDumpOnOOMIsEnabledByDefault(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps:        map[string]WebappProps{"emodel": {HeapSize: "2g"}},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {Image: "nexus.citeck.ru/emodel:1.0"}}}
	wsCfg := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}}}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, ok := app.Environments.Get("JAVA_OPTS")
	require.True(t, ok)
	assert.Contains(t, opts, "-XX:+HeapDumpOnOutOfMemoryError")
	// The dump must land in the export dir — that is the whole point: it lives on
	// the host and outlives the container that produced it.
	assert.Contains(t, opts, "-XX:HeapDumpPath="+ExportMountPath)
	// Gzip, because an ungzipped dump is as large as the heap and is written
	// exactly when the box is already under pressure.
	assert.Contains(t, opts, "-XX:HeapDumpGzipLevel=1")
	// Configured opts must survive: appending must not replace.
	assert.Contains(t, opts, "-Xmx2g")
}

// An explicitly configured heap-dump path wins — silently redirecting someone
// else's dump somewhere they are not looking is worse than not helping.
func TestHeapDumpOnOOMRespectsExplicitConfig(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps: map[string]WebappProps{
			"emodel": {JavaOpts: "-XX:+HeapDumpOnOutOfMemoryError -XX:HeapDumpPath=/mnt/dumps"},
		},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {Image: "nexus.citeck.ru/emodel:1.0"}}}
	wsCfg := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}}}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)

	opts, _ := app.Environments.Get("JAVA_OPTS")
	assert.Contains(t, opts, "-XX:HeapDumpPath=/mnt/dumps")
	assert.NotContains(t, opts, "-XX:HeapDumpPath="+ExportMountPath)
	assert.Equal(t, 1, strings.Count(opts, "HeapDumpOnOutOfMemoryError"), "flags must not be duplicated")
}

// TestRotateHeapDumpsKeepsOnlyTheNewest: a dump is the size of the live heap
// even gzipped, so an app in a crash loop would fill the disk overnight if every
// OOM left a file behind. Only the newest survives — and the older ones are
// removed only while it is still on disk, so there is never a window with no
// evidence at all.
//
// Freeing the canonical path is separately required: HotSpot refuses to
// overwrite an existing dump ("Unable to create …: File exists" — measured
// against a real JVM), and HeapDumpPath has no timestamp placeholder.
func TestRotateHeapDumpsKeepsOnlyTheNewest(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, EnsureExportDir(base, "emodel"))
	dir := ExportDirFor(base, "emodel")

	write := func(name, content string, age time.Duration) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		at := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(path, at, at))
		return path
	}
	// Two dumps from earlier crashes, plus the one HotSpot has just written at
	// its fixed path.
	write("java_pid7-20260810T000000Z.hprof.gz", "oldest", 48*time.Hour)
	write("java_pid7-20260811T000000Z.hprof.gz", "middle", 24*time.Hour)
	canonical := write("java_pid7.hprof.gz", "newest", 0)
	// Not a heap dump: someone else's file in a shared directory.
	keep := write("pg_dump.sql", "select 1", 72*time.Hour)

	at := time.Date(2026, 8, 12, 6, 30, 0, 0, time.UTC)
	kept, removed := RotateHeapDumps(base, "emodel", at)

	assert.Equal(t, 2, removed)
	assert.Equal(t, "java_pid7-20260812T063000Z.hprof.gz", kept)
	assert.NoFileExists(t, canonical, "the fixed dump path must be free for the next OOM")
	assert.FileExists(t, keep, "only heap dumps are managed here")

	content, err := os.ReadFile(filepath.Join(dir, kept))
	require.NoError(t, err)
	assert.Equal(t, "newest", string(content), "the surviving dump must be the newest one")

	dumps := listHeapDumps(t, dir)
	assert.Len(t, dumps, 1, "exactly one dump must remain at rest, got %v", dumps)
}

// A crash loop must converge: however many times an app OOMs, the export dir
// holds one dump, not one per crash.
func TestRotateHeapDumpsBoundsACrashLoop(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, EnsureExportDir(base, "eproc"))
	dir := ExportDirFor(base, "eproc")

	at := time.Date(2026, 8, 12, 6, 0, 0, 0, time.UTC)
	for i := range 10 {
		// Each iteration: the JVM OOMs and writes to the canonical path, then
		// the container is restarted and rotation runs.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "java_pid7.hprof.gz"),
			fmt.Appendf(nil, "crash %d", i), 0o600))
		RotateHeapDumps(base, "eproc", at.Add(time.Duration(i)*time.Minute))
		assert.LessOrEqual(t, len(listHeapDumps(t, dir)), 1, "after restart %d", i)
	}

	dumps := listHeapDumps(t, dir)
	require.Len(t, dumps, 1)
	content, err := os.ReadFile(filepath.Join(dir, dumps[0]))
	require.NoError(t, err)
	assert.Equal(t, "crash 9", string(content), "the surviving dump must be from the LAST crash")
}

// An already-moved dump keeps its name: the stamp says when the dump was taken,
// not when it was last shuffled, and re-stamping on every container start would
// destroy that.
func TestRotateHeapDumpsDoesNotRestampSurvivor(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, EnsureExportDir(base, "emodel"))
	dir := ExportDirFor(base, "emodel")
	name := "java_pid7-20260812T063000Z.hprof.gz"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("dump"), 0o600))

	kept, removed := RotateHeapDumps(base, "emodel", time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC))
	assert.Equal(t, 0, removed)
	assert.Equal(t, name, kept)
	assert.FileExists(t, filepath.Join(dir, name))
}

func TestRotateHeapDumpsHandlesMissingDir(t *testing.T) {
	kept, removed := RotateHeapDumps(t.TempDir(), "never-started", time.Now())
	assert.Empty(t, kept)
	assert.Zero(t, removed)
}

func listHeapDumps(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var dumps []string
	for _, e := range entries {
		if isHeapDumpName(e.Name()) {
			dumps = append(dumps, e.Name())
		}
	}
	return dumps
}
