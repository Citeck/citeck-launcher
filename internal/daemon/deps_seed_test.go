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

	dockervol "github.com/moby/moby/api/types/volume"

	"github.com/citeck/citeck-launcher/internal/bundle"
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
	// askedApps records every app whose container the probe was asked about.
	askedApps *[]string
}

func (f fakeProbe) record(ctx context.Context) {
	if f.seenCtx != nil {
		*f.seenCtx = ctx
	}
}

func (f fakeProbe) ContainerImage(ctx context.Context, app string) (image string, ok bool, err error) {
	f.record(ctx)
	if f.askedApps != nil {
		*f.askedApps = append(*f.askedApps, app)
	}
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
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, "postgres:17.11", got[deps.Postgres].Image)
	assert.Equal(t, "rabbitmq:4.2.9-management", got[deps.RabbitMQ].Image)
	_, hasZK := got[deps.Zookeeper]
	assert.False(t, hasZK, "no container, no volume → no pin")
}

func TestSeedFromPGVersionWhenNoContainer(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}}}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, "postgres:17", got[deps.Postgres].Image)
}

func TestSeedPrefers18LayoutUnlessSnapshotSaysOtherwise(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres2": {"PG_VERSION": "17\n"},
		"postgres3": {"18/docker/PG_VERSION": "18\n"},
	}}
	assert.Equal(t, "postgres:18", seedDependencyPins(context.Background(), nil, p, nil, nil)[deps.Postgres].Image)
	assert.Equal(t, "postgres:17", seedDependencyPins(context.Background(), nil, p, map[string]bool{"postgres2": true}, nil)[deps.Postgres].Image)
}

func TestSeedFallsBackToLegacyImageOnUnreadableData(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {}}, readErr: errors.New("permission denied")}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, deps.PostgresLegacyImage, got[deps.Postgres].Image)
}

func TestSeedUsesLegacyImageForDependenciesWithoutADataFile(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"rabbitmq2": {}, "zookeeper2": {}}}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, "rabbitmq:4.1-management", got[deps.RabbitMQ].Image)
	assert.Equal(t, "zookeeper:3.9", got[deps.Zookeeper].Image)
}

func TestSeedLeavesExistingPinsAlone(t *testing.T) {
	p := fakeProbe{containers: map[string]string{"postgres": "postgres:18"}}
	got := seedDependencyPins(context.Background(),
		map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, p, nil, nil)
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
	seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.NotEmpty(t, asked, "the seeder no longer probes volumes at all — this guard is out of date")
	assert.NotContains(t, asked, "", "an empty volume name matches the volumes ROOT in server mode")
}

// Keycloak's data IS the postgres data. A namespace with a postgres cluster has
// been running, so keycloak ran on the legacy image; one without any postgres
// data is fresh and takes the candidate.
func TestSeedKeycloakFollowsThePostgresData(t *testing.T) {
	withData := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}}}
	assert.Equal(t, keycloakLegacyImage(t),
		seedDependencyPins(context.Background(), nil, withData, nil, nil)[deps.Keycloak].Image)

	empty := fakeProbe{volumes: map[string]map[string]string{"postgres2": {}}}
	_, pinned := seedDependencyPins(context.Background(), nil, empty, nil, nil)[deps.Keycloak]
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
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, "postgres:17", got[deps.Postgres].Image, "the data still answers when the container probe cannot")
	for _, d := range deps.All() {
		if d.ID() == deps.Postgres {
			continue
		}
		assert.Equal(t, d.LegacyImage(), got[d.ID()].Image, string(d.ID()))
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
			got := seedDependencyPins(context.Background(), nil, p, nil, nil)
			for _, d := range deps.All() {
				assert.Equal(t, d.LegacyImage(), got[d.ID()].Image, string(d.ID()))
				assert.Equal(t, 1, got[d.ID()].Gen(),
					"a probe that could not answer may not invent a generation")
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
	assert.Empty(t, seedDependencyPins(context.Background(), nil, p, nil, nil))
}

// Ruling 6, arm 3 (already true before, kept explicit): a PG_VERSION that
// cannot be READ is a failure, not an absence.
func TestSeedAssumesTheLegacyImageWhenPGVersionIsGarbage(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "seventeen"}}}
	assert.Equal(t, deps.PostgresLegacyImage,
		seedDependencyPins(context.Background(), nil, p, nil, nil)[deps.Postgres].Image)
}

// Ruling 2: a volume that exists but holds no PG_VERSION holds no cluster —
// server mode creates the bind dir before the first container start, and a
// removed container leaves it empty. That is an absence, not a read failure,
// so the candidate applies.
func TestSeedTreatsAVolumeWithoutPGVersionAsEmpty(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {}, "postgres3": {}}}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
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
	assert.Equal(t, "postgres:17", seedDependencyPins(context.Background(), nil, p, nil, nil)[deps.Postgres].Image)
}

func TestSeedNothingForAFreshNamespace(t *testing.T) {
	assert.Empty(t, seedDependencyPins(context.Background(), nil, fakeProbe{}, nil, nil))
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
	reseedAfterSnapshotImport(context.Background(), rt, p, []string{"postgres2"}, nil)
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
// never pulled it reports a read FAILURE, and every dependency is seeded to its
// legacy image.
//
// Driven through the depsDocker seam rather than asserted on source positions:
// the whole call sequence is the contract, not merely "one line is above
// another". Both arms matter — the order on the happy path, and what a pull
// failure means, which is a read FAILURE and never an absence (an absence
// would hand the data to the candidate image).
func TestDesktopReadEnsuresTheUtilsImageBeforeRunningIt(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	fake.volumes["postgres2"] = &dockervol.Volume{Name: "citeck_volume_postgres2"}
	fake.utilsOut = "17\n"
	p := dockerDependencyProbe{dc: fake}

	raw, err := p.ReadVolumeFile(context.Background(), "postgres2", "PG_VERSION")
	require.NoError(t, err)
	assert.Equal(t, "17\n", raw)
	assert.Equal(t, []string{"getvol:postgres2", "ensureutils", "utils:cat /vol/PG_VERSION"}, fake.Calls(),
		"the image must be ensured BEFORE the container that needs it runs")

	failing := newFakeDepsDocker()
	failing.volumes["postgres2"] = &dockervol.Volume{Name: "citeck_volume_postgres2"}
	failing.ensureUtilsErr = errors.New("no route to registry")
	p2 := dockerDependencyProbe{dc: failing}

	_, err = p2.ReadVolumeFile(context.Background(), "postgres2", "PG_VERSION")
	require.Error(t, err)
	require.NotErrorIs(t, err, errVolumeFileNotFound,
		"a pull failure says nothing about the data; reading it as an absence applies the candidate image to it")
	assert.Equal(t, []string{"getvol:postgres2", "ensureutils"}, failing.Calls(),
		"the read must not be attempted once the image could not be ensured")
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
		map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, p, nil)

	assert.Equal(t, "postgres:17.5", pins[deps.Postgres].Image, "a persisted pin is never re-derived")
	assert.Equal(t, "rabbitmq:4.1.2-management", pins[deps.RabbitMQ].Image, "a missing pin is seeded from the data")
	assert.Equal(t, map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
		// keycloak's data is the postgres cluster this fixture has. It keeps
		// its state IN that database and has no volume of its own, so its pin
		// carries no generation.
		deps.Keycloak: {Image: keycloakLegacyImage(t)},
	}, seeded, "only the additions are reported as seeded")
}

func TestResolveDependencyPinsDoesNotAliasThePersistedMap(t *testing.T) {
	persisted := map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}
	p := fakeProbe{containers: map[string]string{"rabbitmq": "rabbitmq:4.1.2-management"}}
	pins, _ := resolveDependencyPins(context.Background(), persisted, p, nil)
	pins[deps.Keycloak] = deps.DependencyState{Image: keycloakLegacyImage(t)}
	assert.Equal(t, map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, persisted,
		"the caller's map must not be written through")
}

// Seeding runs synchronously inside loadNamespace and doReloadEx and talks to
// Docker; a hung engine must not hang the namespace load forever.
func TestResolveDependencyPinsBoundsTheProbe(t *testing.T) {
	var seen context.Context
	p := fakeProbe{seenCtx: &seen, containers: map[string]string{"postgres": "postgres:17.11"}}
	_, _ = resolveDependencyPins(context.Background(), nil, p, nil)
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
//
// The two halves are checked as ONE fact: that the value handed to the
// generator is the one the helper produced. Asked separately — "something is
// assigned to genOpts.DependencyPins" and "resolveDependencyPins is called
// somewhere in this function" — a call site that resolves the pins and then
// assigns a DIFFERENT map passes both, which is precisely the defect this
// guard exists to catch (the generator would silently fall back to the
// bundle's images and move the data onto them).
func TestPinsAreWiredIntoEveryGenerateCallSite(t *testing.T) {
	cases := []struct {
		file, fn string
		// wantSource names the call the assigned value must come FROM: the
		// shared seeding helper on the two generate paths, and the runtime's
		// own live pins on the read-only reload plan, which must never re-seed.
		wantSource string
	}{
		{"namespace_loader.go", "loadNamespace", "resolveDependencyPins"},
		{"server.go", "doReloadEx", "resolveDependencyPins"},
		{"routes_reloadplan.go", "resolveReloadPlanInputs", "DependencyStates"},
	}
	for _, c := range cases {
		t.Run(c.fn, func(t *testing.T) {
			fn := parseFuncDecl(t, c.file, c.fn)
			assert.True(t, assignsField(fn, "genOpts", "DependencyStates"),
				"%s must set genOpts.DependencyStates — the generator falls back to the bundle image without it", c.fn)
			ok, why := assignsFieldFrom(fn, "genOpts", "DependencyStates", c.wantSource)
			assert.True(t, ok,
				"%s must assign genOpts.DependencyStates from %s(...): %s", c.fn, c.wantSource, why)
			if c.fn == "loadNamespace" {
				// SetDependencyState persists, and persistState writes
				// Status: r.status — STOPPED at load time, before the caller
				// has acted on ShouldStart. The load path installs pins
				// through the non-persisting RestoreDependencyState instead.
				assert.False(t, callsMethod(fn, "SetDependencyState"),
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

// assignsFieldFrom reports whether the value assigned to <recv>.<field> comes
// from a call to source — either directly, or through ONE identifier defined by
// an assignment whose right-hand side is that call (`pins, seeded :=
// source(...)` followed by `genOpts.Field = pins`, which is the production
// shape). source matches a plain function call (`resolveDependencyPins(...)`)
// or a method call (`act.runtime.DependencyPins()`).
//
// It deliberately does not chase further than one hop: a longer chain is not a
// shape this codebase uses, and silently following one would let the guard
// approve wiring nobody can read either. The returned string explains a false,
// because "the assertion failed" is useless on an AST walk.
func assignsFieldFrom(fn *ast.FuncDecl, recv, field, source string) (ok bool, why string) {
	rhs, found := assignedValue(fn, recv, field)
	if !found {
		return false, "no assignment to " + recv + "." + field
	}
	if callNamed(rhs, source) {
		return true, ""
	}
	ident, isIdent := rhs.(*ast.Ident)
	if !isIdent {
		return false, "assigned from an expression that is not " + source + "(...) and not a variable"
	}
	def, defined := definingCall(fn, ident.Name)
	if !defined {
		return false, ident.Name + " is not defined by any assignment in this function"
	}
	if !callNamed(def, source) {
		return false, ident.Name + " is defined from something other than " + source + "(...)"
	}
	return true, ""
}

// assignedValue returns the expression assigned to <recv>.<field>, matching the
// left-hand position to the right-hand one so a multi-value assignment cannot
// be read off by accident.
func assignedValue(fn *ast.FuncDecl, recv, field string) (ast.Expr, bool) {
	var found ast.Expr
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != len(assign.Rhs) {
			return true
		}
		for i, lhs := range assign.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != field {
				continue
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == recv {
				found = assign.Rhs[i]
			}
		}
		return true
	})
	return found, found != nil
}

// definingCall returns the right-hand side of the assignment that defines name
// (`a, b := f()` counts for both a and b — the call is the single RHS).
func definingCall(fn *ast.FuncDecl, name string) (ast.Expr, bool) {
	var found ast.Expr
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				found = assign.Rhs[0]
			}
		}
		return true
	})
	return found, found != nil
}

// callNamed reports whether e is a call to the function or method `name`.
func callNamed(e ast.Expr, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == name
	case *ast.SelectorExpr:
		return fun.Sel.Name == name
	}
	return false
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

// Seeding runs on EVERY namespace load, so a probe for a dependency the
// namespace does not even generate is a Docker call — a utils container on a
// desktop — bought forever for nothing. The shape that made it visible: a
// namespace whose authentication is not Keycloak has no keycloak container,
// so keycloak fell through to the postgres DATA, which nothing else reads
// (postgres' own container answers first), and paid for that read on every
// load.
func TestSeedSkipsDependenciesTheNamespaceDoesNotHave(t *testing.T) {
	var askedVolumes, askedApps []string
	p := fakeProbe{
		containers: map[string]string{
			"postgres": "postgres:17.11", "rabbitmq": "rabbitmq:4.1.2-management", "zookeeper": "zookeeper:3.9.5",
		},
		volumes:      map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}},
		askedVolumes: &askedVolumes,
		askedApps:    &askedApps,
	}
	// A namespace at config generation 2 with authentication off: no mongo, no
	// keycloak (namespaceDependencies is what answers this in production).
	present := map[deps.ID]bool{deps.Postgres: true, deps.RabbitMQ: true, deps.Zookeeper: true}

	got := seedDependencyPins(context.Background(), nil, p, nil, present)

	assert.Equal(t, "postgres:17.11", got[deps.Postgres].Image, "a dependency the namespace HAS is seeded exactly as before")
	assert.NotContains(t, got, deps.Keycloak, "a dependency the namespace does not generate gets no pin")
	assert.NotContains(t, got, deps.MongoDB)
	// The volume walk still runs for a dependency whose CONTAINER answered —
	// the container names an image and says nothing about which GENERATION of
	// the volume it has mounted — but it must never reach a dependency the
	// namespace does not have.
	require.NotEmpty(t, askedVolumes, "the generation still comes from the data — this guard is out of date without it")
	for _, v := range askedVolumes {
		id, _, ok := deps.ParseVolumeName(v)
		require.True(t, ok, "%q is not a dependency volume name", v)
		assert.True(t, present[id], "%s belongs to a dependency this namespace does not have", v)
	}
	assert.NotContains(t, askedApps, "keycloak", "an absent dependency is not even inspected")
	assert.NotContains(t, askedApps, "mongodb")
	assert.Contains(t, askedApps, "postgres")
}

// The filter must not change the answer for a dependency that IS present: the
// tri-state verdict rules are what protect the data, and narrowing the walk is
// only allowed to remove work.
func TestSeedIsUnchangedForTheDependenciesThatArePresent(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres2": {"PG_VERSION": "17\n"}, "rabbitmq2": {}, "zookeeper2": {}, "mongo2": {},
	}}
	all := namespaceDependencies(nil)
	full := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, full, seedDependencyPins(context.Background(), nil, p, nil, all),
		"an all-inclusive filter is the same as no filter")

	present := map[deps.ID]bool{deps.Postgres: true, deps.Keycloak: true, deps.RabbitMQ: true, deps.Zookeeper: true}
	narrowed := seedDependencyPins(context.Background(), nil, p, nil, present)
	for id, img := range narrowed {
		assert.Equal(t, full[id], img, "%s must be seeded to the same image either way", id)
	}
	assert.NotContains(t, narrowed, deps.MongoDB)
}

// namespaceDependencies restates a rule that LIVES in the generator, and the
// pins it scopes are an input to that generator, so the two are checked
// against each other by running the real thing: for every configuration that
// moves either conditional switch, the predicted set must be exactly the set
// Generate emitted. A dependency wrongly predicted ABSENT is the dangerous
// direction — no pin means the candidate image is applied to existing data.
func TestNamespaceDependenciesMatchesWhatTheGeneratorEmits(t *testing.T) {
	on, off := true, false
	cases := map[string]*namespace.Config{
		"v1 default":            {ID: "ns"},
		"v1 + keycloak":         {ID: "ns", Authentication: namespace.AuthenticationProps{Type: namespace.AuthKeycloak}},
		"v1 + mongo off":        {ID: "ns", MongoDB: namespace.MongoDbProps{Enabled: &off}},
		"v2 default":            {ID: "ns", APIVersion: "v2"},
		"v2 + keycloak":         {ID: "ns", APIVersion: "v2", Authentication: namespace.AuthenticationProps{Type: namespace.AuthKeycloak}},
		"v2 + mongo on":         {ID: "ns", APIVersion: "v2", MongoDB: namespace.MongoDbProps{Enabled: &on}},
		"unparsable apiVersion": {ID: "ns", APIVersion: "banana"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			resp, err := namespace.Generate(cfg, &bundle.EmptyDef, &bundle.WorkspaceConfig{}, namespace.SystemSecrets{})
			require.NoError(t, err)

			generated := map[deps.ID]bool{}
			for id := range resp.Dependencies {
				generated[id] = true
			}
			predicted := map[deps.ID]bool{}
			for id, ok := range namespaceDependencies(cfg) {
				if ok {
					predicted[id] = true
				}
			}
			assert.Equal(t, generated, predicted,
				"the seeding filter and the generator must agree about which dependencies this namespace has")
		})
	}
}

// --- the volume generation --------------------------------------------------

// TestSeedTakesTheHighestGenerationEvenWithAGapBelowIt is why the walk is
// DESCENDING and not ascending-until-the-first-gap.
//
// A gap is a state the launcher actively creates: `citeck deps` tells the
// operator they may delete the old volume once they trust the new version. An
// ascending walk would stop at the missing generation-1 volume and answer
// "generation 1", and the generator would then create a brand-new EMPTY
// rabbitmq2 beside the live rabbitmq3 — a broker reporting healthy with none
// of the namespace's queues in it, which is the empty-cluster-beside-real-data
// failure this whole design exists to prevent.
func TestSeedTakesTheHighestGenerationEvenWithAGapBelowIt(t *testing.T) {
	// The ordinary shape right after a migration: BOTH volumes are on disk,
	// because the launcher never deletes the old one. Only a descending walk
	// answers 2 here; an ascending one answers 1 and remounts the data the
	// migration moved away from.
	both := fakeProbe{volumes: map[string]map[string]string{"rabbitmq2": {}, "rabbitmq3": {}}}
	got := seedDependencyPins(context.Background(), nil, both, nil, nil)
	assert.Equal(t, 2, got[deps.RabbitMQ].Gen(), "the HIGHEST existing generation is the data")
	assert.Equal(t, "rabbitmq:4.1-management", got[deps.RabbitMQ].Image)

	// And once the operator takes `citeck deps` up on reclaiming the old
	// volume, the sequence has a hole in it. A walk that stopped at the first
	// gap would answer generation 1, and the generator would create a
	// brand-new EMPTY rabbitmq2 beside the live rabbitmq3.
	gapped := fakeProbe{volumes: map[string]map[string]string{"rabbitmq3": {}}}
	got = seedDependencyPins(context.Background(), nil, gapped, nil, nil)
	assert.Equal(t, 2, got[deps.RabbitMQ].Gen(), "a missing generation below is not the end of the walk")
}

// The container names the IMAGE and says nothing about which generation of the
// volume it has mounted, so the generation still comes from the data. Without
// this a namespace that had been migrated and then lost its state file would
// be re-pinned at generation 1, and the next reload would mount the
// PRE-migration volume under the post-migration image — PostgreSQL 18 over a
// 17 data directory.
func TestSeedTakesTheGenerationFromTheDataEvenWhenTheContainerNamesTheImage(t *testing.T) {
	p := fakeProbe{
		containers: map[string]string{"postgres": "postgres:18.6", "rabbitmq": "rabbitmq:4.2.9-management"},
		volumes: map[string]map[string]string{
			"postgres3": {"18/docker/PG_VERSION": "18\n"},
			"rabbitmq3": {},
		},
	}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, deps.DependencyState{Image: "postgres:18.6", VolumeGen: 2}, got[deps.Postgres])
	assert.Equal(t, deps.DependencyState{Image: "rabbitmq:4.2.9-management", VolumeGen: 2}, got[deps.RabbitMQ])
}

// For PostgreSQL the two questions compose: the generation is the highest
// volume that HOLDS A CLUSTER, not merely the highest that exists. A
// half-built volume left behind by a rollback that could not finish has no
// PG_VERSION, and pinning the namespace to it would mount an empty directory
// as if it were the data.
func TestSeedSkipsAGenerationWhoseVolumeHoldsNoCluster(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres3": {},                     // the abandoned half-built volume
		"postgres2": {"PG_VERSION": "17\n"}, // the real cluster
	}}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	assert.Equal(t, deps.DependencyState{Image: "postgres:17", VolumeGen: 1}, got[deps.Postgres])
}

// Keycloak answers to the postgres DATA, but it has no volume of its own — so
// the postgres generation must not be copied onto its pin, where it would name
// a volume nobody mounts.
func TestSeedGivesKeycloakNoGeneration(t *testing.T) {
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres3": {"18/docker/PG_VERSION": "18\n"},
	}}
	got := seedDependencyPins(context.Background(), nil, p, nil, nil)
	require.Equal(t, 2, got[deps.Postgres].VolumeGen, "the fixture's postgres data IS at generation 2")
	assert.Zero(t, got[deps.Keycloak].VolumeGen, "keycloak keeps its state in that database, not in a volume")
	assert.Equal(t, keycloakLegacyImage(t), got[deps.Keycloak].Image)
}

// The bounded walk is the cost this design accepted (a generation advances only
// when an operator runs a migration, and ten is more upgrades than any of these
// dependencies has shipped since the launcher existed). It is asserted so that
// widening it is a deliberate act: on a DESKTOP every one of these is a Docker
// API round trip, and the walk runs on the load of any namespace with no pin.
func TestSeedProbesABoundedNumberOfGenerations(t *testing.T) {
	var asked []string
	p := fakeProbe{askedVolumes: &asked}
	seedDependencyPins(context.Background(), nil, p, nil, nil)

	perDependency := map[deps.ID]int{}
	for _, v := range asked {
		id, gen, ok := deps.ParseVolumeName(v)
		require.True(t, ok, "%q is not a dependency volume name", v)
		require.LessOrEqual(t, gen, deps.MaxProbedVolumeGen)
		perDependency[id]++
	}
	// postgres, rabbitmq, zookeeper and mongodb have volumes; keycloak does
	// not and must never be probed with an empty name (in server mode that
	// stats the volumes ROOT, which always exists).
	assert.Len(t, perDependency, 4)
	assert.NotContains(t, perDependency, deps.Keycloak)
	for id, n := range perDependency {
		assert.Equal(t, deps.MaxProbedVolumeGen, n, "%s", id)
	}
}

// A snapshot import re-derives BOTH halves of the pin, and which dependency a
// restored volume belongs to is asked of the registry rather than of a table
// here — the names are a function of the generation now, so a hard-coded list
// would have gone stale the first time anybody migrated anything.
func TestReseedAfterSnapshotImportDerivesTheGenerationFromTheImportedVolume(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 2},
		deps.RabbitMQ: {Image: "rabbitmq:4.2.9-management", VolumeGen: 2},
	}, nil, nil)
	p := fakeProbe{volumes: map[string]map[string]string{
		"postgres2": {"PG_VERSION": "17\n"},
		"postgres3": {"18/docker/PG_VERSION": "18\n"}, // left over from the migration
		"rabbitmq3": {},
	}}

	reseedAfterSnapshotImport(context.Background(), rt, p, []string{"postgres2"}, nil)

	states := rt.DependencyStates()
	assert.Equal(t, deps.DependencyState{Image: "postgres:17", VolumeGen: 1}, states[deps.Postgres],
		"the imported volume wins over the leftover generation-2 volume, image AND generation")
	assert.Equal(t, deps.DependencyState{Image: "rabbitmq:4.2.9-management", VolumeGen: 2}, states[deps.RabbitMQ],
		"an untouched dependency keeps its whole pin")
}

// A volume name that belongs to no registered dependency ("pgadmin2") must not
// move any pin: pgadmin is not a dependency and its volume is not dependency
// data.
func TestReseedAfterSnapshotImportIgnoresAVolumeThatIsNotADependencys(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 2},
	}, nil, nil)
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}}}

	reseedAfterSnapshotImport(context.Background(), rt, p, []string{"pgadmin2"}, nil)

	assert.Equal(t, deps.DependencyState{Image: "postgres:18", VolumeGen: 2},
		rt.DependencyStates()[deps.Postgres], "nothing a dependency owns was imported")
}

// keycloakLegacyImage is what the REGISTRY says a namespace with postgres data
// and no keycloak container has been running. It is asked rather than spelled
// out because the seeding contract is "the descriptor's legacy image", not any
// particular string — and that string moved once already
// (keycloak/keycloak:26 is a 404 on Docker Hub, there is no bare-major
// Keycloak tag), so a literal here would have failed for the fix.
func keycloakLegacyImage(t *testing.T) string {
	t.Helper()
	d, ok := deps.Lookup(deps.Keycloak)
	require.True(t, ok)
	return d.LegacyImage()
}
