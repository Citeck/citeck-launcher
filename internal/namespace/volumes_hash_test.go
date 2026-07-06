package namespace

import (
	"slices"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

func TestCollectFileKeysFromVolumes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"named volume skipped", []string{"pgdata:/var/lib/postgresql/data"}, nil},
		{"absolute host path skipped", []string{"/etc/citeck:/etc/target"}, nil},
		{"simple bind", []string{"./postgres/postgresql.conf:/etc/postgresql/postgresql.conf"}, []string{"postgres/postgresql.conf"}},
		{"ro suffix stripped", []string{"./proxy/lua.lua:/etc/lua.lua:ro"}, []string{"proxy/lua.lua"}},
		{"mixed", []string{
			"pg:/data",
			"./postgres/init.sh:/init.sh",
			"./proxy/nginx.conf:/etc/nginx/nginx.conf",
		}, []string{"postgres/init.sh", "proxy/nginx.conf"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := collectFileKeysFromVolumes(c.in)
			if !slices.Equal(got, c.want) {
				t.Errorf("collectFileKeysFromVolumes(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestComputeVolumesContentHash_DedupsOwnAndInitContainerVolumes(t *testing.T) {
	files := map[string][]byte{
		"postgres/postgresql.conf": []byte("shared"),
		"keycloak/init.sh":         []byte("kc"),
	}
	app := appdef.ApplicationDef{
		Volumes: []string{"./postgres/postgresql.conf:/x"},
		InitContainers: []appdef.InitContainerDef{{
			Volumes: []string{"./postgres/postgresql.conf:/x", "./keycloak/init.sh:/y"},
		}},
	}
	h := computeVolumesContentHash(&app, files)
	if h == "" {
		t.Fatal("empty hash for non-empty file set")
	}
	// Reorder volumes — hash must be stable (sort).
	app2 := appdef.ApplicationDef{
		Volumes: []string{"./postgres/postgresql.conf:/x"},
		InitContainers: []appdef.InitContainerDef{{
			Volumes: []string{"./keycloak/init.sh:/y", "./postgres/postgresql.conf:/x"},
		}},
	}
	if computeVolumesContentHash(&app2, files) != h {
		t.Error("hash depends on volume order")
	}
}

func TestComputeVolumesContentHash_ChangesWithContent(t *testing.T) {
	app := appdef.ApplicationDef{
		Volumes: []string{"./postgres/postgresql.conf:/x"},
	}
	h1 := computeVolumesContentHash(&app, map[string][]byte{"postgres/postgresql.conf": []byte("v1")})
	h2 := computeVolumesContentHash(&app, map[string][]byte{"postgres/postgresql.conf": []byte("v2")})
	if h1 == h2 {
		t.Error("hash didn't change when content changed")
	}
}

func TestComputeVolumesContentHash_ChangesWithRename(t *testing.T) {
	// Two different paths with identical content must hash differently
	// (key participates in the hash so a rename invalidates it).
	app1 := appdef.ApplicationDef{Volumes: []string{"./a/file:/x"}}
	app2 := appdef.ApplicationDef{Volumes: []string{"./b/file:/x"}}
	files := map[string][]byte{"a/file": []byte("same"), "b/file": []byte("same")}
	h1 := computeVolumesContentHash(&app1, files)
	h2 := computeVolumesContentHash(&app2, files)
	if h1 == h2 {
		t.Error("hash same despite different file keys")
	}
}

func TestComputeVolumesContentHash_EmptyWhenNoBindMountFiles(t *testing.T) {
	app := appdef.ApplicationDef{
		Volumes: []string{"pgdata:/var/lib/postgresql/data"},
	}
	h := computeVolumesContentHash(&app, map[string][]byte{"postgres/postgresql.conf": []byte("unused")})
	if h != "" {
		t.Errorf("expected empty hash for named-volume-only app, got %q", h)
	}
}

// TestComputeVolumesContentHash_DirMountChangesWithContainedFile guards the
// Kotlin-parity directory-mount expansion: Spring webapps mount their whole
// "props/" directory (not each file), so editing application-launcher.yml must
// still flip VolumesContentHash — otherwise the reload hash diff misses the edit
// and the container is never recreated to pick it up.
func TestComputeVolumesContentHash_DirMountChangesWithContainedFile(t *testing.T) {
	app := appdef.ApplicationDef{
		Volumes: []string{"./app/uiserv/props:/run/java.io/spring-props/"},
	}
	h1 := computeVolumesContentHash(&app, map[string][]byte{
		"app/uiserv/props/application-launcher.yml": []byte("ecos: {}"),
	})
	h2 := computeVolumesContentHash(&app, map[string][]byte{
		"app/uiserv/props/application-launcher.yml": []byte("ecos: {}\ndebug: true"),
	})
	if h1 == "" {
		t.Fatal("dir-mounted file produced empty hash (directory not expanded)")
	}
	if h1 == h2 {
		t.Error("hash didn't change when a dir-mounted file's content changed")
	}
}

// TestComputeVolumesContentHash_DirMountPrefixIsComponentBoundary ensures the
// directory expansion matches on a path-component boundary, not a raw string
// prefix: "app/x/props" must not swallow files under a sibling "app/x/props2".
func TestComputeVolumesContentHash_DirMountPrefixIsComponentBoundary(t *testing.T) {
	app := appdef.ApplicationDef{
		Volumes: []string{"./app/x/props:/mnt"},
	}
	base := map[string][]byte{"app/x/props/a.yml": []byte("a")}
	sibling := map[string][]byte{
		"app/x/props/a.yml":      []byte("a"),
		"app/x/props2/other.yml": []byte("other"),
	}
	if computeVolumesContentHash(&app, base) != computeVolumesContentHash(&app, sibling) {
		t.Error("sibling directory 'props2' leaked into the 'props' mount hash")
	}
}

// TestExpandDirMountKeys covers exact-file passthrough, directory expansion,
// and dedup across file+dir mounts that resolve to the same files.
func TestExpandDirMountKeys(t *testing.T) {
	files := map[string][]byte{
		"postgres/postgresql.conf":                  {},
		"app/uiserv/props/application-launcher.yml": {},
		"app/uiserv/props/extra.yml":                {},
	}
	got := expandDirMountKeys([]string{
		"postgres/postgresql.conf", // exact file — kept
		"app/uiserv/props",         // directory — expands to both files under it
		"app/uiserv/props",         // duplicate dir — deduped
	}, files)
	slices.Sort(got)
	want := []string{
		"app/uiserv/props/application-launcher.yml",
		"app/uiserv/props/extra.yml",
		"postgres/postgresql.conf",
	}
	if !slices.Equal(got, want) {
		t.Errorf("expandDirMountKeys = %v, want %v", got, want)
	}
}

// Smoke-test to catch accidental use of strings.EqualFold etc. in future
// refactors of collectFileKeysFromVolumes — file paths are case-sensitive.
func TestCollectFileKeysFromVolumes_CaseSensitive(t *testing.T) {
	got := collectFileKeysFromVolumes([]string{"./Postgres/File.Conf:/x"})
	if len(got) != 1 || got[0] != "Postgres/File.Conf" {
		t.Errorf("got %v, want [Postgres/File.Conf]", got)
	}
	if !strings.EqualFold(got[0], "postgres/file.conf") {
		t.Errorf("sanity check failed: %q", got[0])
	}
}
