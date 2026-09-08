package migratetest

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// The fake is shared by the plan tests, the daemon's crash-recovery tests and
// the integration harness, so its own contracts — the recorded call order, the
// per-call error injection and the exec routing — are pinned here rather than
// rediscovered (or silently relied on) three times.

func TestTheFakeRecordsWhatAPlanDidToTheWorld(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Running = true
	f.VolSize["postgres2"] = 2 << 30
	f.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17\n"}
	f.Files["/host/deps-migration/postgres/dump.sql"] = 1 << 20

	assert.Equal(t, "ns1", f.NamespaceID())
	assert.True(t, f.IsRunning())
	require.NoError(t, f.StopNamespace(ctx))
	assert.False(t, f.IsRunning())

	require.NoError(t, f.PullImage(ctx, "postgres:18", nil))
	assert.Equal(t, []string{"postgres:18"}, f.Pulled())

	def, err := f.GenerateDefFor(deps.Postgres, "postgres:18")
	require.NoError(t, err)
	assert.NotEmpty(t, def.Ports, "the default def keeps a published port for the env to strip")
	def.Ports = nil
	id, err := f.RunAppDef(ctx, def, "pg-dst", []string{"/host/d:/citeck/d"})
	require.NoError(t, err)
	assert.Equal(t, "id-pg-dst", id)
	running, err := f.ContainerRunning(ctx, "pg-dst")
	require.NoError(t, err)
	assert.True(t, running)

	require.NoError(t, f.CreateVolume(ctx, "postgres3"))
	exists, err := f.VolumeExists(ctx, "postgres3")
	require.NoError(t, err)
	assert.True(t, exists)
	size, err := f.VolumeSize(ctx, "postgres2")
	require.NoError(t, err)
	assert.Equal(t, int64(2<<30), size)
	free, err := f.VolumeFreeBytes(ctx, "postgres2")
	require.NoError(t, err)
	assert.Equal(t, f.FreeVolume, free)
	ver, err := f.ReadVolumeFile(ctx, "postgres2", "PG_VERSION")
	require.NoError(t, err)
	assert.Equal(t, "17\n", ver)

	assert.Equal(t, "/host/deps-migration/postgres", f.DumpDir(deps.Postgres))
	require.NoError(t, f.EnsureDir("/host/deps-migration/postgres"))
	fs, err := f.FileSize("/host/deps-migration/postgres/dump.sql")
	require.NoError(t, err)
	assert.Equal(t, int64(1<<20), fs)
	hostFree, err := f.HostFreeBytes()
	require.NoError(t, err)
	assert.Equal(t, f.FreeHost, hostFree)
	require.NoError(t, f.RemoveDir("/host/deps-migration/postgres"))
	assert.Empty(t, f.Dirs)

	require.NoError(t, f.StopRemove(ctx, "pg-dst"))
	require.NoError(t, f.RemoveVolume(ctx, "postgres3"))
	f.Record("custom")
	require.NoError(t, f.ReloadAndStart(ctx, true))
	assert.True(t, f.IsRunning())
	assert.Equal(t, []bool{true}, f.Reloads())

	assert.Equal(t, []string{
		"stopns", "pull:postgres:18", "run:pg-dst:postgres:18:/host/d:/citeck/d",
		"createvol:postgres3", "mkdir:/host/deps-migration/postgres",
		"rmdir:/host/deps-migration/postgres", "rm:pg-dst", "rmvol:postgres3",
		"custom", "reload:true",
	}, f.Log())
}

func TestTheFakeRoutesExecThroughExecFn(t *testing.T) {
	f := New()
	f.Containers["pg-src"] = appdef.ApplicationDef{Name: "postgres"}

	// No handler: a silent success, so a plan under test only has to script
	// the commands it actually cares about.
	stdout, stderr, code, err := f.Exec(context.Background(), "pg-src", []string{"true"})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Zero(t, code)

	f.ExecFn = func(container, cmdline string) (string, string, int, error) {
		assert.Equal(t, "pg-src", container)
		assert.Equal(t, "psql -c select 1", cmdline, "the handler sees the joined command line")
		return "1", "NOTICE: something", 3, nil
	}
	stdout, stderr, code, err = f.Exec(context.Background(), "pg-src", []string{"psql", "-c", "select 1"})
	require.NoError(t, err)
	assert.Equal(t, "1", stdout)
	assert.Equal(t, "NOTICE: something", stderr, "the streams stay separate, as in the real Env")
	assert.Equal(t, 3, code, "a command that ran and failed reports the code, not an error")

	// A container that was never started is a failure to run at all.
	_, _, code, err = f.Exec(context.Background(), "pg-dst", []string{"true"})
	require.Error(t, err)
	assert.Equal(t, -1, code)
}

func TestTheFakeRefusesADefWhosePortsWereNotStripped(t *testing.T) {
	f := New()
	def, err := f.GenerateDefFor(deps.Postgres, "postgres:18")
	require.NoError(t, err)
	_, err = f.RunAppDef(context.Background(), def, "pg-dst", nil)
	require.ErrorContains(t, err, "ports must be stripped")
	assert.Empty(t, f.Log(), "a refused call is not recorded")
}

func TestTheFakeInjectsAnErrorPerCall(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	f := New()
	f.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17\n"}
	f.Containers["pg-src"] = appdef.ApplicationDef{Name: "postgres"}
	f.FailOn["createvol:postgres3"] = boom
	f.FailOn["rmvol:postgres3"] = boom
	f.FailOn["rm:pg-src"] = boom
	f.FailOn["run:pg-dst"] = boom
	f.FailOn["pull:postgres:18"] = boom
	f.FailOn["mkdir:/host/x"] = boom
	f.FailOn["rmdir:/host/x"] = boom
	f.FailOn["readfile:postgres2/PG_VERSION"] = boom
	f.FailOn["gendef:postgres:18"] = boom
	f.FailOn["stopns:"] = boom
	f.FailOn["reload:"] = boom

	require.ErrorIs(t, f.CreateVolume(ctx, "postgres3"), boom)
	require.ErrorIs(t, f.RemoveVolume(ctx, "postgres3"), boom)
	require.ErrorIs(t, f.StopRemove(ctx, "pg-src"), boom)
	_, err := f.RunAppDef(ctx, appdef.ApplicationDef{}, "pg-dst", nil)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, f.PullImage(ctx, "postgres:18", nil), boom)
	require.ErrorIs(t, f.EnsureDir("/host/x"), boom)
	require.ErrorIs(t, f.RemoveDir("/host/x"), boom)
	_, err = f.ReadVolumeFile(ctx, "postgres2", "PG_VERSION")
	require.ErrorIs(t, err, boom)
	_, err = f.GenerateDefFor(deps.Postgres, "postgres:18")
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, f.StopNamespace(ctx), boom)
	require.ErrorIs(t, f.ReloadAndStart(ctx, true), boom)

	assert.Empty(t, f.Log(), "an injected failure changes nothing")
	assert.Contains(t, f.Containers, "pg-src")
	assert.NotContains(t, f.Volumes, "postgres3")
}

func TestTheFakeReportsAMissingVolumeOrFile(t *testing.T) {
	f := New()
	_, err := f.ReadVolumeFile(context.Background(), "postgres9", "PG_VERSION")
	require.ErrorContains(t, err, "no such volume")
	f.Volumes["postgres2"] = map[string]string{}
	_, err = f.ReadVolumeFile(context.Background(), "postgres2", "PG_VERSION")
	require.ErrorContains(t, err, "no such file")
}

func TestTheFakeReportsPullProgress(t *testing.T) {
	var pct []float64
	f := New()
	require.NoError(t, f.PullImage(context.Background(), "postgres:18", func(p float64) { pct = append(pct, p) }))
	assert.Equal(t, []float64{100}, pct)
}
