package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestMCPFullConfigUsesNativeEditor(t *testing.T) {
	d := mcpFixture(t)
	b := mcpBackend{d}
	a := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel"}
	out, err := b.Call(context.Background(), "application_config_read", a)
	require.NoError(t, err)
	var def appdef.ApplicationDef
	require.NoError(t, yaml.Unmarshal([]byte(out.(map[string]any)["content"].(string)), &def))
	def.Image = "developer:2"
	def.Cmd = []string{"custom-command", "--debug"}
	def.Volumes = append(def.Volumes, "./app/emodel/debug.conf:/etc/debug.conf")
	def.Ports = []string{"*:5005:5005"}
	def.DependsOn = []string{"postgres"}
	data, err := yaml.Marshal(def)
	require.NoError(t, err)
	a.Content = string(data)
	_, err = b.Call(context.Background(), "application_config_write", a)
	require.NoError(t, err)
	_, effective, err := effectiveMCPDef(d.active().runtime, a.App)
	require.NoError(t, err)
	require.Equal(t, def.Image, effective.Image)
	require.Equal(t, def.Cmd, effective.Cmd)
	require.Equal(t, def.Volumes, effective.Volumes)
	require.Equal(t, def.Ports, effective.Ports)
	require.Equal(t, def.DependsOn, effective.DependsOn)
	before := d.active().runtime.AppPatch(a.App)
	for _, content := range []string{"image: ''", "image: test\nports: ['bad:port']", "[invalid"} {
		a.Content = content
		_, err = b.Call(context.Background(), "application_config_write", a)
		require.Error(t, err)
		require.JSONEq(t, string(before), string(d.active().runtime.AppPatch(a.App)))
	}
	_, err = b.Call(context.Background(), "application_config_reset", a)
	require.NoError(t, err)
	require.Empty(t, d.active().runtime.AppPatch(a.App))
}

func TestMCPFullFilesIncludeNonSpringAndNestedFiles(t *testing.T) {
	d := mcpFixture(t)
	rt := d.active().runtime
	def, ok := rt.GeneratedDef("emodel")
	require.True(t, ok)
	def.IsJVM = false // Non-JVM apps (such as proxy) get the same file editor.
	rt.SetGeneratedDefs([]appdef.ApplicationDef{def})
	key := "app/emodel/props/nested/start.sh"
	rt.SetLastGenFiles(map[string][]byte{key: []byte("#!/bin/sh\necho original\n")})
	file := filepath.Join(d.active().volumesBase, key)
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte("#!/bin/sh\necho original\n"), 0o644))
	b := mcpBackend{d}
	a := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Path: "./" + key}
	out, err := b.Call(context.Background(), "application_files_list", a)
	require.NoError(t, err)
	found := false
	for _, raw := range out.([]any) {
		if raw.(map[string]any)["path"] == a.Path {
			found = true
		}
	}
	require.True(t, found)
	a.Content = "#!/bin/sh\necho changed\n"
	out, err = b.Call(context.Background(), "application_file_write", a)
	require.NoError(t, err)
	require.Equal(t, true, out.(map[string]any)["applyRequired"])
	require.True(t, rt.IsFileEdited(key))
	out, err = b.Call(context.Background(), "application_file_read", a)
	require.NoError(t, err)
	require.Equal(t, a.Content, out.(map[string]string)["content"])
	require.Contains(t, out.(map[string]string)["baseline"], "original")
	_, err = b.Call(context.Background(), "application_file_reset", a)
	require.NoError(t, err)
	require.False(t, rt.IsFileEdited(key))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Contains(t, string(data), "original")
	for _, key := range []string{"../outside.sh", "/tmp/outside.sh", "app/other/start.sh", "app/emodel/props/archive.jar"} {
		a.Path = key
		_, err = b.Call(context.Background(), "application_file_write", a)
		require.Error(t, err, key)
	}
}

func TestMCPFullEditorStillRefusesBreakingDependencyImage(t *testing.T) {
	d, _, rt, _ := newPinnedEditGateDaemon(t)
	a := launchermcp.Arguments{Target: targetOf(d.active()), App: "postgres", Content: "image: postgres:18\n"}
	_, err := (mcpBackend{d}).Call(context.Background(), "application_config_write", a)
	require.ErrorContains(t, err, "citeck deps upgrade postgres")
	require.Empty(t, rt.AppPatch("postgres"))
}

func TestMCPFullFilesRefuseSymlinksAndOversizeReads(t *testing.T) {
	d := mcpFixture(t)
	b := mcpBackend{d}
	key := "app/emodel/props/test.conf"
	file := filepath.Join(d.active().volumesBase, key)
	a := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Path: key, Content: "changed"}
	require.NoError(t, os.WriteFile(file, []byte("original"), 0o644))
	// Native UI can edit a mounted text file even without a generated template.
	_, err := b.Call(context.Background(), "application_file_write", a)
	require.NoError(t, err)
	require.True(t, d.active().runtime.IsFileEdited(key))
	_, err = b.Call(context.Background(), "application_file_reset", a)
	require.NoError(t, err)
	require.False(t, d.active().runtime.IsFileEdited(key))
	require.NoError(t, os.WriteFile(file, make([]byte, launchermcp.MaxBytes+1), 0o644))
	_, err = b.Call(context.Background(), "application_file_read", a)
	require.ErrorContains(t, err, "exceeds")
	require.NoError(t, os.Remove(file))
	target := filepath.Join(t.TempDir(), "outside.conf")
	require.NoError(t, os.WriteFile(target, []byte("outside"), 0o644))
	require.NoError(t, os.Symlink(target, file))
	for _, tool := range []string{"application_file_read", "application_file_write", "application_file_reset"} {
		_, err = b.Call(context.Background(), tool, a)
		require.Error(t, err)
	}
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "outside", string(data))
}

func TestMCPFullEditorKeepsTargetAndOperationGates(t *testing.T) {
	d := mcpFixture(t)
	b := mcpBackend{d}
	a := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Content: "image: test"}
	for _, tool := range []string{"application_config_read", "application_config_write", "application_config_reset", "application_files_list", "application_file_read", "application_file_write", "application_file_reset"} {
		wrong := a
		wrong.WorkspaceID = "other"
		_, err := b.Call(context.Background(), tool, wrong)
		require.ErrorContains(t, err, "TARGET_CHANGED")
	}
	require.True(t, d.longOp.TryLock(longOpMigration))
	defer d.longOp.Unlock()
	_, err := b.Call(context.Background(), "application_config_write", a)
	require.Error(t, err)
	require.Empty(t, d.active().runtime.AppPatch(a.App))
}
