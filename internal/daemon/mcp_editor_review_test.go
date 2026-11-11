package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/stretchr/testify/require"
)

func mcpListedEditorPaths(t *testing.T, d *Daemon) []string {
	t.Helper()
	out, err := (mcpBackend{d}).Call(context.Background(), "application_files_list", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel"})
	require.NoError(t, err)
	rows := out.([]any)
	keys := make([]string, 0, len(rows))
	for _, raw := range rows {
		keys = append(keys, raw.(map[string]any)["path"].(string))
	}
	return keys
}

func TestMCPFullDiscoveryDoesNotFollowDirectories(t *testing.T) {
	for _, relative := range []string{"app/emodel", "app/emodel/props", "app/emodel/props/nested"} {
		t.Run(relative, func(t *testing.T) {
			d := mcpFixture(t)
			outside := t.TempDir()
			// Either intermediate app parent or the mount itself can be a link.
			secret := filepath.Join(outside, "private.conf")
			if relative == "app/emodel" {
				secret = filepath.Join(outside, "props", "private.conf")
				require.NoError(t, os.MkdirAll(filepath.Dir(secret), 0o755))
			}
			require.NoError(t, os.WriteFile(secret, []byte("private"), 0o644))
			link := filepath.Join(d.active().volumesBase, relative)
			require.NoError(t, os.RemoveAll(link))
			require.NoError(t, os.Symlink(outside, link))
			paths := mcpListedEditorPaths(t, d)
			if strings.HasSuffix(relative, "/nested") {
				require.Equal(t, []string{"./app/emodel/props/application-launcher.yml"}, paths)
			} else {
				require.Empty(t, paths)
			}
		})
	}
}

func TestMCPFullDiscoveryListsRegularFilesWithSymlinkedHome(t *testing.T) {
	d := mcpFixture(t)
	base := d.active().volumesBase
	props := filepath.Join(base, "app", "emodel", "props")
	require.NoError(t, os.MkdirAll(filepath.Join(props, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(props, "nested", "nginx.conf"), []byte("config"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(props, "archive.jar"), []byte("binary"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(props, "application-launcher.yml"), filepath.Join(props, "link.yml")))
	// Direct file mounts use the same no-follow open; duplicates are listed once.
	def, ok := d.active().runtime.GeneratedDef("emodel")
	require.True(t, ok)
	def.Volumes = append(def.Volumes,
		"./app/emodel/props/nested/nginx.conf:/etc/nginx.conf",
		"./app/emodel/props/link.yml:/etc/link.yml")
	d.active().runtime.SetGeneratedDefs([]appdef.ApplicationDef{def})
	homeLink := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.Symlink(base, homeLink))
	d.activeNs.volumesBase = homeLink
	require.Equal(t, []string{
		"./app/emodel/props/application-launcher.yml",
		"./app/emodel/props/nested/nginx.conf",
	}, mcpListedEditorPaths(t, d))
}

func TestMCPFullDiscoveryRefusesPartialLists(t *testing.T) {
	for _, limit := range []string{"entries", "depth"} {
		t.Run(limit, func(t *testing.T) {
			d := mcpFixture(t)
			props := filepath.Join(d.active().volumesBase, "app", "emodel", "props")
			if limit == "entries" {
				for i := range 500 {
					require.NoError(t, os.WriteFile(filepath.Join(props, fmt.Sprintf("file%d.conf", i)), nil, 0o644))
				}
			} else {
				require.NoError(t, os.MkdirAll(filepath.Join(props, strings.Repeat("nested/", 65)), 0o755))
			}
			out, err := (mcpBackend{d}).Call(context.Background(), "application_files_list", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel"})
			require.ErrorContains(t, err, "limit")
			require.Nil(t, out, "a refused traversal must not return a partial editable-file list")
		})
	}
}

func TestMCPFullFileWritesRetainNativeStructuralValidation(t *testing.T) {
	for _, ext := range []string{"yml", "yaml", "json"} {
		t.Run(ext, func(t *testing.T) {
			d := mcpFixture(t)
			key := "app/emodel/props/settings." + ext
			baseline := []byte("{\"original\": true}\n")
			d.active().runtime.SetLastGenFiles(map[string][]byte{key: baseline})
			file := filepath.Join(d.active().volumesBase, key)
			require.NoError(t, os.WriteFile(file, baseline, 0o644))
			a := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Path: key}
			for _, content := range []string{"", "[]", "scalar", "[invalid"} {
				a.Content = content
				_, err := (mcpBackend{d}).Call(context.Background(), "application_file_write", a)
				require.Error(t, err, content)
				data, e := os.ReadFile(file)
				require.NoError(t, e)
				require.Equal(t, baseline, data)
				require.Empty(t, d.active().runtime.FileEditsSnapshot())
			}
			a.Content = "{\"original\": false}\n"
			_, err := (mcpBackend{d}).Call(context.Background(), "application_file_write", a)
			require.NoError(t, err)
			require.Equal(t, "structural", d.active().runtime.FileEditsSnapshot()[key].Format)
		})
	}
}

func TestMCPFullTextFileAllowsEmptyContent(t *testing.T) {
	d := mcpFixture(t)
	key := "app/emodel/props/start.sh"
	d.active().runtime.SetLastGenFiles(map[string][]byte{key: []byte("echo original\n")})
	file := filepath.Join(d.active().volumesBase, key)
	require.NoError(t, os.WriteFile(file, []byte("echo original\n"), 0o644))
	_, err := (mcpBackend{d}).Call(context.Background(), "application_file_write", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Path: key, Content: ""})
	require.NoError(t, err)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Empty(t, data)
	require.Equal(t, "textual", d.active().runtime.FileEditsSnapshot()[key].Format)
}
