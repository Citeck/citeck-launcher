package namespace

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
