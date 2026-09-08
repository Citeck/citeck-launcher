package daemon

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type fakeProbe struct {
	containers map[string]string            // app → image
	volumes    map[string]map[string]string // volume → rel path → content
	// containerErr / volumeErr / readErr make one probe FAIL, which is a
	// different answer from "nothing found" — see the ruling-6 arms below.
	containerErr error
	volumeErr    error
	readErr      error
	// seenCtx records the context the last probe call was made with, so a test
	// can assert seeding is bounded.
	seenCtx *context.Context
	// askedVolumes records every volume name the probe was asked about.
	askedVolumes *[]string
}

func (f fakeProbe) record(ctx context.Context) {
	if f.seenCtx != nil {
		*f.seenCtx = ctx
	}
}

func (f fakeProbe) ContainerImage(ctx context.Context, app string) (image string, ok bool, err error) {
	f.record(ctx)
	if f.containerErr != nil {
		return "", false, f.containerErr
	}
	img, ok := f.containers[app]
	return img, ok, nil
}

func (f fakeProbe) VolumeExists(ctx context.Context, volume string) (bool, error) {
	f.record(ctx)
	if f.askedVolumes != nil {
		*f.askedVolumes = append(*f.askedVolumes, volume)
	}
	if f.volumeErr != nil {
		return false, f.volumeErr
	}
	_, ok := f.volumes[volume]
	return ok, nil
}

func (f fakeProbe) ReadVolumeFile(ctx context.Context, volume, rel string) (string, error) {
	f.record(ctx)
	if f.readErr != nil {
		return "", f.readErr
	}
	files, ok := f.volumes[volume]
	if !ok {
		return "", fmt.Errorf("%s: %w", volume, errVolumeFileNotFound)
	}
	c, ok := files[rel]
	if !ok {
		return "", fmt.Errorf("%s in %s: %w", rel, volume, errVolumeFileNotFound)
	}
	return c, nil
}

func TestSeedFromRunningContainerWins(t *testing.T) {
	p := fakeProbe{
		containers: map[string]string{"postgres": "postgres:17.11", "rabbitmq": "rabbitmq:4.2.9-management"},
		volumes:    map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}},
	}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	assert.Equal(t, "postgres:17.11", got[deps.Postgres])
	assert.Equal(t, "rabbitmq:4.2.9-management", got[deps.RabbitMQ])
	_, hasZK := got[deps.Zookeeper]
	assert.False(t, hasZK, "no container, no volume → no pin")
}

func TestSeedFromPGVersionWhenNoContainer(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}}}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	assert.Equal(t, "postgres:17", got[deps.Postgres])
}

func TestSeedPrefers18LayoutUnlessSnapshotSaysOtherwise(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres2": {"PG_VERSION": "17\n"},
		"postgres3": {"18/docker/PG_VERSION": "18\n"},
	}}
	assert.Equal(t, "postgres:18", seedDependencyPins(context.Background(), nil, p, nil)[deps.Postgres])
	assert.Equal(t, "postgres:17", seedDependencyPins(context.Background(), nil, p, map[string]bool{"postgres2": true})[deps.Postgres])
}

func TestSeedFallsBackToLegacyImageOnUnreadableData(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {}}, readErr: errors.New("permission denied")}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	assert.Equal(t, deps.PostgresLegacyImage, got[deps.Postgres])
}

func TestSeedUsesLegacyImageForDependenciesWithoutADataFile(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"rabbitmq2": {}, "zookeeper2": {}}}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	assert.Equal(t, "rabbitmq:4.1-management", got[deps.RabbitMQ])
	assert.Equal(t, "zookeeper:3.9", got[deps.Zookeeper])
}

func TestSeedLeavesExistingPinsAlone(t *testing.T) {
	p := fakeProbe{containers: map[string]string{"postgres": "postgres:18"}}
	got := seedDependencyPins(context.Background(), map[deps.ID]string{deps.Postgres: "postgres:17.5"}, p, nil)
	_, touched := got[deps.Postgres]
	assert.False(t, touched)
}

// Keycloak keeps its state in postgres, so it has no data volume of its own.
// Asking the probe about the empty volume name would answer YES in server mode
// (the volumes DIRECTORY exists), and every namespace would silently be pinned
// to the legacy Keycloak image forever.
func TestSeedNeverAsksAboutAnEmptyVolumeName(t *testing.T) {
	var asked []string
	p := fakeProbe{askedVolumes: &asked}
	seedDependencyPins(context.Background(), nil, p, nil)
	assert.NotEmpty(t, asked, "the seeder no longer probes volumes at all — this guard is out of date")
	assert.NotContains(t, asked, "", "an empty volume name matches the volumes ROOT in server mode")
}

// Keycloak's data IS the postgres data. A namespace with a postgres cluster has
// been running, so keycloak ran on the legacy image; one without any postgres
// data is fresh and takes the candidate.
func TestSeedKeycloakFollowsThePostgresData(t *testing.T) {
	withData := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}}}
	assert.Equal(t, "keycloak/keycloak:26",
		seedDependencyPins(context.Background(), nil, withData, nil)[deps.Keycloak])

	empty := fakeProbe{volumes: map[string]map[string]string{"postgres2": {}}}
	_, pinned := seedDependencyPins(context.Background(), nil, empty, nil)[deps.Keycloak]
	assert.False(t, pinned, "no postgres cluster → keycloak has no data either")
}

// An inspect failure over data that DOES exist must not read as "fresh" — that
// would hand the namespace to the candidate image and start PostgreSQL 18
// beside untouched 17 data.
func TestSeedAssumesTheLegacyImageWhenTheContainerProbeFailsOverExistingData(t *testing.T) {
	p := fakeProbe{
		containerErr: errors.New("dial unix /var/run/docker.sock: connection refused"),
		volumes: map[string]map[string]string{
			"postgres2": {"PG_VERSION": "17\n"}, "rabbitmq2": {}, "zookeeper2": {}, "mongo2": {},
		},
	}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	assert.Equal(t, "postgres:17", got[deps.Postgres], "the data still answers when the container probe cannot")
	for _, d := range deps.All() {
		if d.ID() == deps.Postgres {
			continue
		}
		assert.Equal(t, d.LegacyImage(), got[d.ID()], string(d.ID()))
	}
}

// A failed volume lookup (a transient VolumeList error on desktop, or the seed
// deadline expiring) leaves the data question OPEN, so it assumes the legacy
// image — including when the container probe failed too, which is the shape of
// a desktop Docker outage.
func TestSeedAssumesTheLegacyImageWhenTheVolumeProbeFails(t *testing.T) {
	for name, p := range map[string]fakeProbe{
		"volume probe alone":    {volumeErr: context.DeadlineExceeded},
		"docker down (desktop)": {volumeErr: context.DeadlineExceeded, containerErr: errors.New("connection refused")},
	} {
		t.Run(name, func(t *testing.T) {
			got := seedDependencyPins(context.Background(), nil, p, nil)
			for _, d := range deps.All() {
				assert.Equal(t, d.LegacyImage(), got[d.ID()], string(d.ID()))
			}
		})
	}
}

// The other half of that rule: when the FILESYSTEM can still answer — server
// mode stats <volumesBase>/volumes/<vol> without Docker — an absent volume
// proves there is nothing to protect, because a container cannot exist without
// its data volume. A fresh namespace first loaded during a Docker outage must
// therefore stay UNPINNED; pinning it to the legacy images would hold every
// minor-breaking dependency back (rabbitmq at 4.1 against a 4.2 bundle) with
// no path forward.
func TestSeedLeavesAFreshNamespaceUnpinnedWhenOnlyTheContainerProbeFails(t *testing.T) {
	p := fakeProbe{containerErr: errors.New("dial unix /var/run/docker.sock: connection refused")}
	assert.Empty(t, seedDependencyPins(context.Background(), nil, p, nil))
}

// Ruling 6, arm 3 (already true before, kept explicit): a PG_VERSION that
// cannot be READ is a failure, not an absence.
func TestSeedAssumesTheLegacyImageWhenPGVersionIsGarbage(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "seventeen"}}}
	assert.Equal(t, deps.PostgresLegacyImage,
		seedDependencyPins(context.Background(), nil, p, nil)[deps.Postgres])
}

// Ruling 2: a volume that exists but holds no PG_VERSION holds no cluster —
// server mode creates the bind dir before the first container start, and a
// removed container leaves it empty. That is an absence, not a read failure,
// so the candidate applies.
func TestSeedTreatsAVolumeWithoutPGVersionAsEmpty(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {}, "postgres3": {}}}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	_, pinned := got[deps.Postgres]
	assert.False(t, pinned, "an empty volume must not pin the namespace to the legacy image")
}

// ...and an empty NEW-layout volume beside a real 17 cluster must not shadow
// it: the 18 layout is checked first, finds no cluster, and the 17 data wins.
func TestSeedSkipsAnEmptyLayoutAndKeepsLookingForTheCluster(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres3": {},
		"postgres2": {"PG_VERSION": "17\n"},
	}}
	assert.Equal(t, "postgres:17", seedDependencyPins(context.Background(), nil, p, nil)[deps.Postgres])
}

func TestSeedNothingForAFreshNamespace(t *testing.T) {
	assert.Empty(t, seedDependencyPins(context.Background(), nil, fakeProbe{}, nil))
}

func TestReseedAfterSnapshotImportReplacesThePinOfImportedVolumes(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18"},
		deps.RabbitMQ: {Image: "rabbitmq:4.2.9-management"},
	}, nil, nil)
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres2": {"PG_VERSION": "17\n"},
		"postgres3": {"18/docker/PG_VERSION": "18\n"}, // left over on desktop
	}}
	reseedAfterSnapshotImport(context.Background(), rt, p, []string{"postgres2"})
	pins := rt.DependencyPins()
	assert.Equal(t, "postgres:17", pins[deps.Postgres], "the imported volume wins over the leftover 18 volume")
	assert.Equal(t, "rabbitmq:4.2.9-management", pins[deps.RabbitMQ], "untouched dependency keeps its pin")
}

// The server-mode probe: the not-found arm must be distinguishable from a read
// FAILURE, because the two lead to opposite pins (candidate vs legacy).
func TestServerProbeDistinguishesAMissingFileFromAFailedRead(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	base := t.TempDir()
	volDir := filepath.Join(base, "volumes", "postgres2")
	require.NoError(t, os.MkdirAll(volDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(volDir, "PG_VERSION"), []byte("17\n"), 0o600))
	// A directory where a file is expected reads back as a failure (EISDIR),
	// which is exactly the shape of "the data is there but unreadable".
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres3", "18", "docker", "PG_VERSION"), 0o755))

	p := dockerDependencyProbe{volumesBase: base}
	ctx := context.Background()

	exists, err := p.VolumeExists(ctx, "postgres2")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = p.VolumeExists(ctx, "rabbitmq2")
	require.NoError(t, err)
	assert.False(t, exists, "a missing volume is an answer, not an error")

	raw, err := p.ReadVolumeFile(ctx, "postgres2", "PG_VERSION")
	require.NoError(t, err)
	assert.Equal(t, "17\n", raw)

	_, err = p.ReadVolumeFile(ctx, "postgres3", "18/docker/PG_VERSION")
	require.Error(t, err)
	require.NotErrorIs(t, err, errVolumeFileNotFound, "an unreadable file is NOT an absent one")

	_, err = p.ReadVolumeFile(ctx, "postgres2", "nope/PG_VERSION")
	require.ErrorIs(t, err, errVolumeFileNotFound)
	_, err = p.ReadVolumeFile(ctx, "no-such-volume", "PG_VERSION")
	require.ErrorIs(t, err, errVolumeFileNotFound)
}

// The desktop probe reads through `cat` in a utils container, so its
// not-found/failure split is a classification of the command's output. busybox
// and coreutils both say "No such file or directory" and both exit 1, so the
// exit code cannot carry the distinction.
func TestDesktopCatFailureClassification(t *testing.T) {
	notFound := classifyCatFailure("postgres3", "18/docker/PG_VERSION", 1,
		"cat: can't open '/vol/18/docker/PG_VERSION': No such file or directory")
	require.ErrorIs(t, notFound, errVolumeFileNotFound)

	denied := classifyCatFailure("postgres2", "PG_VERSION", 1,
		"cat: can't open '/vol/PG_VERSION': Permission denied")
	require.Error(t, denied)
	require.NotErrorIs(t, denied, errVolumeFileNotFound, "a permission error is a read failure")
	assert.Contains(t, denied.Error(), "Permission denied", "the reason must survive into the warning")
}

// The desktop read runs `cat` inside the launcher-utils container, so the image
// has to be on the host first — otherwise the first data probe on a host that
// never pulled it reports a read FAILURE and every dependency is seeded to its
// legacy image. The call needs a real engine, so the ORDER is checked
// structurally: the shared docker.Client.EnsureUtilsImage before
// RunUtilsContainer, the same way volume sizing and snapshots do it.
func TestDesktopReadEnsuresTheUtilsImageBeforeRunningIt(t *testing.T) {
	fn := parseFuncDecl(t, "deps_seed.go", "ReadVolumeFile")
	ensurePos, runPos := -1, -1
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "EnsureUtilsImage":
			ensurePos = int(call.Pos())
		case "RunUtilsContainer":
			runPos = int(call.Pos())
		}
		return true
	})
	require.NotEqual(t, -1, runPos, "the desktop read no longer runs a utils container — this guard is out of date")
	require.NotEqual(t, -1, ensurePos, "ReadVolumeFile must ensure the utils image is present")
	assert.Less(t, ensurePos, runPos, "the image check must come before the container runs")
}

// The wiring helper both the load path and the reload path go through: what
// Generate is told is the persisted pins PLUS whatever seeding found, and the
// seeded half is reported separately so the caller can log/persist it.
func TestResolveDependencyPinsMergesPersistedWithSeeded(t *testing.T) {
	p := fakeProbe{
		containers: map[string]string{"rabbitmq": "rabbitmq:4.1.2-management"},
		volumes:    map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}},
	}
	pins, seeded := resolveDependencyPins(context.Background(),
		map[deps.ID]string{deps.Postgres: "postgres:17.5"}, p)

	assert.Equal(t, "postgres:17.5", pins[deps.Postgres], "a persisted pin is never re-derived")
	assert.Equal(t, "rabbitmq:4.1.2-management", pins[deps.RabbitMQ], "a missing pin is seeded from the data")
	assert.Equal(t, map[deps.ID]string{
		deps.RabbitMQ: "rabbitmq:4.1.2-management",
		// keycloak's data is the postgres cluster this fixture has.
		deps.Keycloak: "keycloak/keycloak:26",
	}, seeded, "only the additions are reported as seeded")
}

func TestResolveDependencyPinsDoesNotAliasThePersistedMap(t *testing.T) {
	persisted := map[deps.ID]string{deps.Postgres: "postgres:17.5"}
	p := fakeProbe{containers: map[string]string{"rabbitmq": "rabbitmq:4.1.2-management"}}
	pins, _ := resolveDependencyPins(context.Background(), persisted, p)
	pins[deps.Keycloak] = "keycloak/keycloak:26"
	assert.Equal(t, map[deps.ID]string{deps.Postgres: "postgres:17.5"}, persisted,
		"the caller's map must not be written through")
}

// Seeding runs synchronously inside loadNamespace and doReloadEx and talks to
// Docker; a hung engine must not hang the namespace load forever.
func TestResolveDependencyPinsBoundsTheProbe(t *testing.T) {
	var seen context.Context
	p := fakeProbe{seenCtx: &seen, containers: map[string]string{"postgres": "postgres:17.11"}}
	_, _ = resolveDependencyPins(context.Background(), nil, p)
	require.NotNil(t, seen)
	deadline, ok := seen.Deadline()
	require.True(t, ok, "the probe context must carry a deadline")
	assert.LessOrEqual(t, time.Until(deadline), dependencySeedTimeout)
}

// The wiring itself: loadNamespace and doReloadEx must BOTH resolve pins
// through the shared helper and hand the result to the generator, and the
// reload plan must pass the runtime's pins. Neither loadNamespace nor
// doReloadEx can be driven from a unit test (git, bundle resolve, real Docker
// client), so the call sites are checked structurally — a string match would
// pass on a commented-out line, an AST walk does not.
func TestPinsAreWiredIntoEveryGenerateCallSite(t *testing.T) {
	cases := []struct {
		file, fn      string
		wantResolveFn bool
	}{
		{"namespace_loader.go", "loadNamespace", true},
		{"server.go", "doReloadEx", true},
		{"routes_reloadplan.go", "resolveReloadPlanInputs", false},
	}
	for _, c := range cases {
		t.Run(c.fn, func(t *testing.T) {
			fn := parseFuncDecl(t, c.file, c.fn)
			assert.True(t, assignsField(fn, "genOpts", "DependencyPins"),
				"%s must set genOpts.DependencyPins — the generator falls back to the bundle image without it", c.fn)
			if c.wantResolveFn {
				assert.True(t, callsFunc(fn, "resolveDependencyPins"),
					"%s must resolve pins through the shared helper", c.fn)
			}
			if c.fn == "loadNamespace" {
				// SetDependencyPin persists, and persistState writes
				// Status: r.status — STOPPED at load time, before the caller
				// has acted on ShouldStart. The load path installs pins
				// through the non-persisting RestoreDependencyState instead.
				assert.False(t, callsMethod(fn, "SetDependencyPin"),
					"loadNamespace must not persist pins: that would overwrite the stored namespace status")
				assert.True(t, callsMethod(fn, "RestoreDependencyState"),
					"loadNamespace must install the resolved pins into the runtime")
			}
		})
	}
}

// The create-with-snapshot path has no runtime to re-seed against, so it
// depends on ordering: the import must finish BEFORE the namespace is
// activated, or the first Generate runs before the imported data exists and
// pins the namespace to the wrong version.
func TestCreateImportsTheSnapshotBeforeActivating(t *testing.T) {
	fn := parseFuncDecl(t, "routes_ns.go", "createNamespace")
	importPos, activatePos := -1, -1
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "importCreateSnapshot":
			importPos = int(call.Pos())
		case "autoActivateAfterCreate":
			activatePos = int(call.Pos())
		}
		return true
	})
	require.NotEqual(t, -1, importPos, "createNamespace no longer imports the snapshot")
	require.NotEqual(t, -1, activatePos, "createNamespace no longer activates the namespace")
	assert.Less(t, importPos, activatePos,
		"the snapshot import must run before activation so the first Generate sees the imported data")
}

func parseFuncDecl(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parse %s", file)
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("func %s not found in %s — this guard is out of date", name, file)
	return nil
}

// assignsField reports whether fn contains an assignment to <recv>.<field>.
func assignsField(fn *ast.FuncDecl, recv, field string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != field {
				continue
			}
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == recv {
				found = true
			}
		}
		return true
	})
	return found
}

// callsMethod reports whether fn contains a call to any <x>.<name>(...).
func callsMethod(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}

func callsFunc(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}
