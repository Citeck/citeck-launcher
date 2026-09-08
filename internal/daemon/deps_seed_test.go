package daemon

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type fakeProbe struct {
	containers map[string]string            // app → image
	volumes    map[string]map[string]string // volume → rel path → content
	readErr    error
	// seenCtx records the context the last probe call was made with, so a test
	// can assert seeding is bounded.
	seenCtx *context.Context
}

func (f fakeProbe) record(ctx context.Context) {
	if f.seenCtx != nil {
		*f.seenCtx = ctx
	}
}

func (f fakeProbe) ContainerImage(ctx context.Context, app string) (image string, ok bool, err error) {
	f.record(ctx)
	img, ok := f.containers[app]
	return img, ok, nil
}

func (f fakeProbe) VolumeExists(ctx context.Context, volume string) (bool, error) {
	f.record(ctx)
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
		return "", errors.New("no such volume")
	}
	c, ok := files[rel]
	if !ok {
		return "", errors.New("no such file")
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
	p := volumeAlwaysExistsProbe{}
	got := seedDependencyPins(context.Background(), nil, p, nil)
	_, pinned := got[deps.Keycloak]
	assert.False(t, pinned, "keycloak has no volume; it must be seeded from a container only")
}

type volumeAlwaysExistsProbe struct{ fakeProbe }

func (volumeAlwaysExistsProbe) VolumeExists(context.Context, string) (bool, error) { return true, nil }

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
	assert.Equal(t, map[deps.ID]string{deps.RabbitMQ: "rabbitmq:4.1.2-management"}, seeded,
		"only the additions are reported as seeded")
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
