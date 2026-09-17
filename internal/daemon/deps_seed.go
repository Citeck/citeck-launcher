package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// dependencySeedTimeout bounds one seeding pass. Seeding runs SYNCHRONOUSLY
// inside loadNamespace and doReloadEx and performs up to a handful of Docker
// calls per dependency; a hung engine must cost the namespace load a bounded
// wait rather than hanging it forever. What the expired deadline then yields
// is the same as any other probe failure — see seedDependencyPins: the legacy
// image where data MIGHT exist, no pin where the filesystem could still prove
// it does not.
const dependencySeedTimeout = 30 * time.Second

// errVolumeFileNotFound is the probe's "that file is not there" answer, as
// opposed to "I could not read it". The difference decides a pin: a volume
// with no PG_VERSION holds no cluster (server mode creates the bind directory
// before the first container start, and removing the container leaves it
// empty), so the candidate image applies — while a read that FAILED says
// nothing about the data and must fall back to the legacy image.
var errVolumeFileNotFound = errors.New("volume file not found")

// dependencyProbe is what seeding needs from Docker and the filesystem,
// narrowed so the rules are unit-testable without an engine.
type dependencyProbe interface {
	// ContainerImage returns the image of the namespace's container for app,
	// ok=false when no such container exists.
	ContainerImage(ctx context.Context, app string) (image string, ok bool, err error)
	// VolumeExists reports whether the plain-named data volume exists.
	VolumeExists(ctx context.Context, volume string) (bool, error)
	// ReadVolumeFile returns the content of a file inside a data volume. A
	// file (or volume) that is not there is reported as errVolumeFileNotFound;
	// every other error means the read failed.
	ReadVolumeFile(ctx context.Context, volume, rel string) (string, error)
}

// dockerDependencyProbe is the production probe and the ONE place that knows
// where a namespace's data physically lives: desktop keeps it in scoped named
// volumes (read through a utils container, since the host cannot see into
// them), server mode in a bind directory under <volumesBase>/volumes/<name>.
// Everything else — seeding, and later the migration engine — goes through the
// interface, so that rule cannot drift into a second copy.
//
// dc is the narrow depsDocker seam rather than *docker.Client so the probe and
// the migration Env (deps_env.go) share one fake — box a possibly-nil client
// with depsDockerOf, never by assigning the pointer directly: a typed nil
// would pass the `dc == nil` guards below.
type dockerDependencyProbe struct {
	dc          depsDocker
	volumesBase string
}

// volumeDir is where a plain-named data volume lives in SERVER mode. The one
// place that spelling exists; the migration Env asks for it rather than
// re-deriving it.
func (p dockerDependencyProbe) volumeDir(volume string) string {
	return filepath.Join(p.volumesBase, "volumes", volume)
}

func (p dockerDependencyProbe) ContainerImage(ctx context.Context, app string) (image string, ok bool, err error) {
	if p.dc == nil {
		return "", false, errors.New("no docker client")
	}
	info, err := p.dc.InspectContainer(ctx, p.dc.ContainerName(app))
	if err != nil {
		if isNotFoundErr(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect %s: %w", app, err)
	}
	if info.Config == nil {
		return "", false, nil
	}
	return info.Config.Image, true, nil
}

func (p dockerDependencyProbe) VolumeExists(ctx context.Context, volume string) (bool, error) {
	if !config.IsDesktopMode() {
		// The name is always a deps.VolumeName result — a registry VolumeBase
		// plus a decimal generation — so it can hold neither a separator nor a
		// dot, and the directory is the launcher-owned volumesBase. gosec sees
		// only that a request-scoped call reaches this stat (the dependency
		// list asks about the retained volume of every rollback offer on every
		// request) and cannot follow the value back to the fixed registry.
		//nolint:gosec // G703: registry-derived volume name under the launcher's own volumes directory
		_, err := os.Stat(p.volumeDir(volume))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("stat volume %s: %w", volume, err)
		}
		return true, nil
	}
	if p.dc == nil {
		return false, errors.New("no docker client")
	}
	v, err := p.dc.GetVolumeByOriginalName(ctx, volume)
	if err != nil {
		return false, fmt.Errorf("lookup volume %s: %w", volume, err)
	}
	return v != nil, nil
}

func (p dockerDependencyProbe) ReadVolumeFile(ctx context.Context, volume, rel string) (string, error) {
	if !config.IsDesktopMode() {
		//nolint:gosec // G304: path is the launcher-owned volumesBase plus registry-known volume/file names
		data, err := os.ReadFile(filepath.Join(p.volumeDir(volume), filepath.FromSlash(rel)))
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s in %s: %w", rel, volume, errVolumeFileNotFound)
		}
		if err != nil {
			return "", fmt.Errorf("read %s in %s: %w", rel, volume, err)
		}
		return string(data), nil
	}
	if p.dc == nil {
		return "", errors.New("no docker client")
	}
	v, err := p.dc.GetVolumeByOriginalName(ctx, volume)
	if err != nil {
		return "", fmt.Errorf("lookup volume %s: %w", volume, err)
	}
	if v == nil {
		return "", fmt.Errorf("%s in %s: %w", rel, volume, errVolumeFileNotFound)
	}
	// The read runs in the launcher-utils container, so the image must be on
	// the host first — the shared docker.Client.EnsureUtilsImage, same as
	// volume sizing, snapshots and the migration Env. Without it, the first
	// data probe on a host that never pulled the image would report a read
	// FAILURE, and every dependency would be seeded to its legacy image. A pull
	// that fails IS a probe failure (the caller then assumes the legacy image),
	// which is why the error is returned rather than swallowed.
	if imgErr := p.dc.EnsureUtilsImage(ctx); imgErr != nil {
		return "", fmt.Errorf("ensure utils image for %s: %w", volume, imgErr)
	}
	out, code, err := p.dc.RunUtilsContainer(ctx, []string{"cat", "/vol/" + rel}, []string{v.Name + ":/vol:ro"})
	if err != nil {
		return "", fmt.Errorf("read %s in %s: %w", rel, volume, err)
	}
	if code != 0 {
		return "", classifyCatFailure(volume, rel, code, out)
	}
	return out, nil
}

// classifyCatFailure turns a non-zero `cat` inside the utils container into
// either the not-found sentinel or a read failure. The distinction cannot be
// taken from the exit code — busybox `cat` exits 1 for both a missing file and
// a permission error — so it is taken from the message, which is the same
// wording in busybox and coreutils ("No such file or directory").
func classifyCatFailure(volume, rel string, code int, out string) error {
	msg := strings.TrimSpace(out)
	if strings.Contains(strings.ToLower(msg), "no such file") {
		return fmt.Errorf("%s in %s: %w", rel, volume, errVolumeFileNotFound)
	}
	return fmt.Errorf("cat %s in %s: exit %d: %s", rel, volume, code, msg)
}

// namespaceDependencies answers WHICH registered dependencies this namespace
// actually generates. Three of the six are conditional (see
// internal/namespace/generator.go): mongo is emitted only when
// Config.MongoEnabled(), keycloak only when the namespace authenticates
// through it, and qdrant only where the RAG webapp is generated.
//
// The first two conditions are in the namespace CONFIG. Qdrant's is not: RAG
// comes from the BUNDLE, so answering for it needs the bundle, the workspace
// config and the detach set as well — which is why they are parameters rather
// than something this could read off cfg. It is the same shape of question,
// asked one layer further out.
//
// It is derived from the configuration rather than from a generated app set
// because the pins are an INPUT to Generate — seeding runs before it on both
// the load and the reload path, so there is no generated set to consult yet,
// and the previous generation's would be stale for the very edit that turned
// one of them on. What Generate itself reads is what is read here, so this
// cannot disagree with it for the run it is seeding; that agreement is pinned
// by TestNamespaceDependenciesMatchesWhatTheGeneratorEmits, which drives the
// REAL generator over the configurations that move any of the three switches.
//
// A nil config means nothing is known yet and answers ALL of them — except
// qdrant, whose condition is answered from the bundle and stays false when
// there is no bundle to answer from. The asymmetry is deliberate and follows
// the cost of being wrong in each direction: a wrongly ABSENT pin hands data
// to the candidate image, so the default is present; but a namespace with no
// bundle names no qdrant image and therefore has no vector index to protect,
// while a
// wrongly PRESENT qdrant costs a container inspect plus a generation walk on
// EVERY load of every community stand, forever — the same bill the keycloak
// filter was written to stop paying.
func namespaceDependencies(cfg *namespace.Config, bun *bundle.Def,
	wsCfg *bundle.WorkspaceConfig,
) map[deps.ID]bool {
	present := make(map[deps.ID]bool, len(deps.All()))
	for _, d := range deps.All() {
		present[d.ID()] = true
	}
	present[deps.Qdrant] = namespace.WillGenerateQdrant(cfg, bun, wsCfg)
	if cfg == nil {
		return present
	}
	present[deps.MongoDB] = cfg.MongoEnabled()
	present[deps.Keycloak] = cfg.Authentication.Type == namespace.AuthKeycloak
	return present
}

// resolveDependencyPins turns "what this namespace has persisted" into "what
// Generate must be told": the persisted pins plus a pin for every dependency
// that has none but does have data. It is the single wiring point shared by
// the load path (loadNamespace) and the reload path (doReloadEx) so the two
// cannot answer the question differently — a namespace whose pins were seeded
// on load and one whose data appeared afterwards (a snapshot import) reach the
// generator with the same map.
//
// present narrows the work to the dependencies this namespace HAS (see
// namespaceDependencies). Seeding runs on every load and every probe costs a
// Docker call — a utils container on a desktop. The case that motivated the
// filter is a namespace that does not authenticate through Keycloak: keycloak
// had no container to inspect, fell through to the postgres data — which
// postgres itself never reads, because its own container answers first — and
// paid for that read on every load, forever.
//
// Returns (all pins, only the seeded additions). The additions are reported
// separately because the two callers do different things with them: the load
// path only logs them and installs them through the NON-persisting
// RestoreDependencyState (persisting there would write r.status, which is
// still STOPPED before the caller acts on ShouldStart), the reload path
// persists each one.
func resolveDependencyPins(ctx context.Context, persisted map[deps.ID]deps.DependencyState,
	probe dependencyProbe, present map[deps.ID]bool,
) (pins, seeded map[deps.ID]deps.DependencyState) {
	ctx, cancel := context.WithTimeout(ctx, dependencySeedTimeout)
	defer cancel()

	pins = make(map[deps.ID]deps.DependencyState, len(persisted)+len(deps.All()))
	maps.Copy(pins, persisted)
	seeded = seedDependencyPins(ctx, pins, probe, nil, present)
	maps.Copy(pins, seeded)
	return pins, seeded
}

// seedVerdict is what the evidence for one dependency adds up to.
type seedVerdict int

const (
	// seedNoData: every probe answered, and none of them found data. The
	// namespace is fresh for this dependency, so the candidate image applies
	// and no pin is written.
	seedNoData seedVerdict = iota
	// seedFound: an image was determined from the data.
	seedFound
	// seedUnknown: a probe FAILED, so nothing can be concluded.
	seedUnknown
)

// dataEvidence is what one dependency's DATA says about itself: which image it
// has been running (empty when the data cannot name one) and which GENERATION
// of the data volume holds it. Generation 0 means the walk found no volume at
// all, which VolumeName and DependencyState.Gen both read as generation 1.
type dataEvidence struct {
	image   string
	gen     int
	verdict seedVerdict
}

// seedDependencyPins answers "what does this namespace's data run on?" for
// every registered dependency that has no pin yet, and returns the pins to
// add. Order of evidence for the IMAGE: the container (survives launcher
// upgrades and names the real registry), then the data itself (PostgreSQL's
// PG_VERSION), then the descriptor's legacy image.
//
// The GENERATION always comes from the data, whatever answered for the image.
// A container names an image and says nothing about which generation of the
// volume it has mounted, so a namespace that has been migrated and then lost
// its state file would otherwise be re-pinned at generation 1 — and the next
// reload would mount the PRE-migration volume under the post-migration image.
//
// The rule for failures is per EVIDENCE, not per probe, because the two
// mistakes are not symmetric. A pin that is wrongly LEGACY holds a namespace
// back from an upgrade it could have taken; a pin that is wrongly ABSENT hands
// the data to the candidate image — PostgreSQL 18 started on an empty
// new-layout volume beside untouched 17 data, with the RUNNING re-pin hook
// then settling the pin at 18. So:
//
//   - a probe failure that leaves the DATA question open (seedUnknown: the
//     volume lookup itself failed) ⇒ the legacy image, with a WARN. Seeding
//     only ever runs where no pin exists, i.e. on pre-feature data, and the
//     legacy image is exactly what that data has been running on;
//   - a SUCCESSFUL "there is nothing here" (seedNoData) ⇒ no pin, so the
//     candidate applies — even when the container probe failed. A container
//     cannot exist without its data volume (server mode creates
//     <volumesBase>/volumes/<vol> at container creation), so an absent volume,
//     answered without error, proves there is nothing to protect. This is the
//     fresh namespace first loaded while Docker is down: pinning it to the
//     legacy images would hold every minor-breaking dependency (rabbitmq at
//     4.1 against a 4.2 bundle) with no path back. On DESKTOP the same outage
//     also fails the volume lookup, which is seedUnknown, so the legacy
//     fallback still covers it there.
//
// preferVolumes marks volumes a snapshot import just restored; when more than
// one generation holds data the imported one is the truth.
//
// present (nil ⇒ all) limits the walk to the dependencies the namespace
// generates: one that is not part of it has no container to gate, so probing
// for its data buys nothing and costs a Docker call per namespace load.
func seedDependencyPins(ctx context.Context, existing map[deps.ID]deps.DependencyState, probe dependencyProbe,
	preferVolumes map[string]bool, present map[deps.ID]bool,
) map[deps.ID]deps.DependencyState {
	out := make(map[deps.ID]deps.DependencyState)
	// Read at most once, and only if something needs it: the postgres data
	// answers for Keycloak too, whose state lives in that same database rather
	// than in a volume of its own.
	var pgEvidence dataEvidence
	pgRead := false
	postgresEvidence := func() dataEvidence {
		if !pgRead {
			pgEvidence = postgresPinFromData(ctx, probe, preferVolumes)
			pgRead = true
		}
		return pgEvidence
	}

	for _, d := range deps.All() {
		if existing[d.ID()].Image != "" {
			continue
		}
		if present != nil && !present[d.ID()] {
			continue
		}
		containerFailed := false
		img, ok, err := probe.ContainerImage(ctx, d.AppName())
		if err != nil {
			containerFailed = true
			slog.Warn("Dependency seed: container inspect failed", "dependency", d.ID(), "err", err)
		}

		var ev dataEvidence
		switch d.ID() {
		case deps.Postgres, deps.Keycloak:
			ev = postgresEvidence()
		default:
			ev = volumePinFromData(ctx, probe, d, preferVolumes)
		}
		if d.ID() == deps.Keycloak {
			// Keycloak's own version is not recoverable from postgres data;
			// what the data proves is that this namespace HAS been running,
			// so it ran on the legacy image. Its generation comes from the
			// same place its volume does — nowhere: it has none, so the
			// postgres generation must not be copied onto its pin.
			ev.image = d.LegacyImage()
			ev.gen = 0
		}

		if ok && img != "" {
			// The container names the image; the DATA still names the
			// generation. A probe that could not answer carries none (the
			// evidence functions return seedUnknown with no generation), and
			// an absent generation reads as 1 — the one every namespace that
			// has never migrated runs.
			out[d.ID()] = deps.DependencyState{Image: img, VolumeGen: ev.gen}
			continue
		}

		switch ev.verdict {
		case seedFound:
			out[d.ID()] = deps.DependencyState{Image: ev.image, VolumeGen: ev.gen}
		case seedUnknown:
			// The legacy image is a Docker Hub reference the launcher invented
			// because it could not see a container, so on a private-registry
			// stand it may not even be pullable. The generator's rehomePin
			// covers the held-back case; naming the manual way out here covers
			// the rest, and it is not obvious from the outside.
			slog.Warn("Dependency seed: data probe failed; assuming the legacy image",
				"dependency", d.ID(), "image", d.LegacyImage(),
				"recovery", "`citeck edit "+d.AppName()+"` with a same-major image of the right registry is not a breaking change and replaces this guess")
			out[d.ID()] = deps.DependencyState{Image: d.LegacyImage(), VolumeGen: ev.gen}
		case seedNoData:
			// No pin either way — see the rule above. The warning is worth
			// keeping when the container probe failed, because that is the
			// one case where the conclusion rests on the filesystem alone.
			if containerFailed {
				slog.Warn("Dependency seed: container inspect failed, but the data volume is absent; leaving it unpinned",
					"dependency", d.ID())
			}
		}
	}
	return out
}

// generationOrder is the order the seeding probe asks about a dependency's
// data volumes: DESCENDING from deps.MaxProbedVolumeGen, with any generation a
// snapshot import just restored moved to the front.
//
// Descending, and NOT ascending-until-the-first-gap, because a gap is a state
// the launcher actively creates: `citeck deps` tells the operator they may
// delete the old volume once they trust the new version. An ascending walk
// would answer "generation 1" for a namespace whose generation-1 volume is
// gone, and the generator would then create a brand-new EMPTY rabbitmq2 beside
// the live rabbitmq3 — the empty-cluster-beside-real-data failure this whole
// design exists to prevent.
//
// The imported generations come first because a snapshot import is newer
// evidence than a volume that merely still exists: re-seeding after an import
// is exactly the case where the highest generation is not the truth.
func generationOrder(d deps.Descriptor, preferVolumes map[string]bool) []int {
	preferred := make([]int, 0, len(preferVolumes))
	rest := make([]int, 0, deps.MaxProbedVolumeGen)
	for gen := deps.MaxProbedVolumeGen; gen >= 1; gen-- {
		if preferVolumes[deps.VolumeName(d, gen)] {
			preferred = append(preferred, gen)
			continue
		}
		rest = append(rest, gen)
	}
	return append(preferred, rest...)
}

// volumePinFromData answers the data question for the dependencies whose data
// is a volume with no readable version marker: the volume's EXISTENCE is the
// whole evidence, so it can only ever mean "the legacy image, at the
// generation the volume was found in" or "nothing".
func volumePinFromData(ctx context.Context, probe dependencyProbe, d deps.Descriptor,
	preferVolumes map[string]bool,
) dataEvidence {
	if d.VolumeBase() == "" {
		// A dependency with no volume of its own. Never probe with an empty
		// name: in server mode that stats <volumesBase>/volumes/, which always
		// exists, and every namespace would be pinned to the legacy image.
		return dataEvidence{verdict: seedNoData}
	}
	failed := false
	for _, gen := range generationOrder(d, preferVolumes) {
		volume := deps.VolumeName(d, gen)
		exists, err := probe.VolumeExists(ctx, volume)
		if err != nil {
			slog.Warn("Dependency seed: volume check failed", "dependency", d.ID(), "volume", volume, "err", err)
			failed = true
			continue
		}
		if !exists {
			continue
		}
		slog.Warn("Dependency seed: no container and no version file; assuming the legacy image",
			"dependency", d.ID(), "image", d.LegacyImage(), "volume", volume,
			"recovery", "`citeck edit "+d.AppName()+"` with a same-major image of the right registry is not a breaking change and replaces this guess")
		return dataEvidence{image: d.LegacyImage(), gen: gen, verdict: seedFound}
	}
	if failed {
		// No generation, deliberately: a probe that could not answer has
		// settled nothing, and an absent generation reads as 1 — the one every
		// pre-counter namespace has. Carrying the generation the interrupted
		// walk happened to reach would invent a volume out of a failure.
		return dataEvidence{verdict: seedUnknown}
	}
	return dataEvidence{verdict: seedNoData}
}

// postgresPinFromData reads PG_VERSION out of whichever generation of the
// postgres volume holds a cluster. A volume that exists but has no PG_VERSION
// holds no cluster (an empty bind dir left by a removed container) and the
// search continues into the next generation; seedNoData means none of them
// holds one, seedUnknown that a probe failed and the caller must assume rather
// than conclude.
//
// The two questions compose here and only here: the GENERATION is the highest
// volume that holds a cluster — not merely the highest that exists, since a
// half-built volume left behind by a rollback that could not finish has no
// PG_VERSION — and the MAJOR read out of that volume is the pin's image.
func postgresPinFromData(ctx context.Context, probe dependencyProbe, preferVolumes map[string]bool) dataEvidence {
	d, ok := deps.Lookup(deps.Postgres)
	if !ok {
		return dataEvidence{verdict: seedNoData}
	}
	failed := false
	for _, gen := range generationOrder(d, preferVolumes) {
		volume := deps.VolumeName(d, gen)
		exists, err := probe.VolumeExists(ctx, volume)
		if err != nil {
			slog.Warn("Dependency seed: postgres volume check failed", "volume", volume, "err", err)
			failed = true
			continue
		}
		if !exists {
			continue
		}
		// The probe ORDER inside a volume — newest layout first — is the
		// registry's to state, not this function's: teaching the launcher
		// about a new layout, or about a new major inside one, is then an edit
		// to internal/deps and nowhere else.
		for _, rel := range deps.KnownPostgresDataPaths() {
			raw, err := probe.ReadVolumeFile(ctx, volume, rel)
			if errors.Is(err, errVolumeFileNotFound) {
				// No cluster at this path in this volume.
				continue
			}
			if err != nil {
				slog.Warn("Dependency seed: PG_VERSION unreadable; assuming the legacy image",
					"volume", volume, "err", err, "image", deps.PostgresLegacyImage)
				return dataEvidence{image: deps.PostgresLegacyImage, gen: gen, verdict: seedFound}
			}
			major, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				slog.Warn("Dependency seed: PG_VERSION not a number; assuming the legacy image",
					"volume", volume, "raw", raw, "image", deps.PostgresLegacyImage)
				return dataEvidence{image: deps.PostgresLegacyImage, gen: gen, verdict: seedFound}
			}
			return dataEvidence{image: "postgres:" + strconv.Itoa(major), gen: gen, verdict: seedFound}
		}
	}
	if failed {
		// No generation — see volumePinFromData for why a failure invents none.
		return dataEvidence{verdict: seedUnknown}
	}
	return dataEvidence{verdict: seedNoData}
}

// reseedAfterSnapshotImport re-derives the pins — image AND volume generation —
// of every dependency whose data a snapshot just replaced. Without it a
// namespace already on 18 that imports a 17 snapshot keeps a pin of 18 over 17
// data, and a namespace at generation 2 that imports a generation-1 snapshot
// keeps mounting a volume the import did not fill.
//
// Which dependency a restored volume belongs to is asked of the registry
// (deps.ParseVolumeName) rather than of a table here: the volume names are a
// function of the generation now, so a hard-coded list would have gone stale
// the first time anybody migrated anything.
func reseedAfterSnapshotImport(ctx context.Context, rt *namespace.Runtime, probe dependencyProbe,
	importedVolumes []string, present map[deps.ID]bool,
) {
	imported := make(map[string]bool, len(importedVolumes))
	touched := make(map[deps.ID]bool, len(importedVolumes))
	for _, v := range importedVolumes {
		imported[v] = true
		if id, _, ok := deps.ParseVolumeName(v); ok {
			touched[id] = true
		}
	}
	// Keycloak's state lives in the postgres data, so a restored postgres
	// volume moves its pin too.
	if touched[deps.Postgres] {
		touched[deps.Keycloak] = true
	}
	existing := rt.DependencyStates()
	for id := range touched {
		delete(existing, id)
	}
	seedCtx, cancel := context.WithTimeout(ctx, dependencySeedTimeout)
	defer cancel()
	for id, st := range seedDependencyPins(seedCtx, existing, probe, imported, present) {
		slog.Info("Dependency pin re-seeded after snapshot import",
			"dependency", id, "image", st.Image, "volumeGen", st.Gen())
		rt.SetDependencyState(id, st)
	}
}
