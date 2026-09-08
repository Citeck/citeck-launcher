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

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/docker"
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
type dockerDependencyProbe struct {
	dc          *docker.Client
	volumesBase string
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
		_, err := os.Stat(filepath.Join(p.volumesBase, "volumes", volume))
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
		data, err := os.ReadFile(filepath.Join(p.volumesBase, "volumes", volume, filepath.FromSlash(rel)))
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
	// the host first — same order as docker.Client.VolumeSize and the snapshot
	// package's ensureUtilsImage. Without it, the first data probe on a host
	// that never pulled the image would report a read FAILURE, and every
	// dependency would be seeded to its legacy image. A pull that fails IS a
	// probe failure (the caller then assumes the legacy image), which is why
	// the error is returned rather than swallowed.
	utilsImage := config.UtilsImage()
	if !p.dc.ImageExists(ctx, utilsImage) {
		if pullErr := p.dc.PullImage(ctx, utilsImage, nil); pullErr != nil {
			return "", fmt.Errorf("pull utils image %s: %w", utilsImage, pullErr)
		}
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

// resolveDependencyPins turns "what this namespace has persisted" into "what
// Generate must be told": the persisted pins plus a pin for every dependency
// that has none but does have data. It is the single wiring point shared by
// the load path (loadNamespace) and the reload path (doReloadEx) so the two
// cannot answer the question differently — a namespace whose pins were seeded
// on load and one whose data appeared afterwards (a snapshot import) reach the
// generator with the same map.
//
// Returns (all pins, only the seeded additions). The additions are reported
// separately because the two callers do different things with them: the load
// path only logs them and installs them through the NON-persisting
// RestoreDependencyState (persisting there would write r.status, which is
// still STOPPED before the caller acts on ShouldStart), the reload path
// persists each one.
func resolveDependencyPins(ctx context.Context, persisted map[deps.ID]string, probe dependencyProbe) (pins, seeded map[deps.ID]string) {
	ctx, cancel := context.WithTimeout(ctx, dependencySeedTimeout)
	defer cancel()

	pins = make(map[deps.ID]string, len(persisted)+len(deps.All()))
	maps.Copy(pins, persisted)
	seeded = seedDependencyPins(ctx, pins, probe, nil)
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

// seedDependencyPins answers "what does this namespace's data run on?" for
// every registered dependency that has no pin yet, and returns the pins to
// add. Order of evidence: the container (survives launcher upgrades), then the
// data itself (PostgreSQL's PG_VERSION), then the descriptor's legacy image.
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
// preferVolumes marks volumes a snapshot import just restored; when both
// postgres layouts hold a cluster the imported one is the truth.
func seedDependencyPins(ctx context.Context, existing map[deps.ID]string, probe dependencyProbe, preferVolumes map[string]bool) map[deps.ID]string {
	out := make(map[deps.ID]string)
	// Read at most once, and only if something needs it: the postgres data
	// answers for Keycloak too, whose state lives in that same database rather
	// than in a volume of its own.
	var pgPin string
	var pgVerdict seedVerdict
	pgRead := false
	postgresEvidence := func() (string, seedVerdict) {
		if !pgRead {
			pgPin, pgVerdict = postgresPinFromData(ctx, probe, preferVolumes)
			pgRead = true
		}
		return pgPin, pgVerdict
	}

	for _, d := range deps.All() {
		if img := existing[d.ID()]; img != "" {
			continue
		}
		containerFailed := false
		img, ok, err := probe.ContainerImage(ctx, d.AppName())
		if err != nil {
			containerFailed = true
			slog.Warn("Dependency seed: container inspect failed", "dependency", d.ID(), "err", err)
		}
		if ok && img != "" {
			out[d.ID()] = img
			continue
		}

		var pin string
		var verdict seedVerdict
		if d.ID() == deps.Postgres || d.ID() == deps.Keycloak {
			pin, verdict = postgresEvidence()
		} else {
			pin, verdict = volumePinFromData(ctx, probe, d)
		}
		if d.ID() == deps.Keycloak && verdict == seedFound {
			// Keycloak's own version is not recoverable from postgres data;
			// what the data proves is that this namespace HAS been running,
			// so it ran on the legacy image.
			pin = d.LegacyImage()
		}

		switch verdict {
		case seedFound:
			out[d.ID()] = pin
		case seedUnknown:
			slog.Warn("Dependency seed: data probe failed; assuming the legacy image",
				"dependency", d.ID(), "image", d.LegacyImage())
			out[d.ID()] = d.LegacyImage()
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

// volumePinFromData answers the data question for the dependencies whose data
// is a volume with no readable version marker: the volume's existence is the
// whole evidence, so it can only ever mean "the legacy image" or "nothing".
func volumePinFromData(ctx context.Context, probe dependencyProbe, d deps.Descriptor) (string, seedVerdict) {
	volume := legacyVolumeFor(d.ID())
	if volume == "" {
		// A dependency with no volume of its own. Never probe with an empty
		// name: in server mode that stats <volumesBase>/volumes/, which always
		// exists, and every namespace would be pinned to the legacy image.
		return "", seedNoData
	}
	exists, err := probe.VolumeExists(ctx, volume)
	if err != nil {
		slog.Warn("Dependency seed: volume check failed", "dependency", d.ID(), "volume", volume, "err", err)
		return "", seedUnknown
	}
	if !exists {
		return "", seedNoData
	}
	slog.Warn("Dependency seed: no container and no version file; assuming the legacy image",
		"dependency", d.ID(), "image", d.LegacyImage())
	return d.LegacyImage(), seedFound
}

// legacyVolumeFor is the data volume each non-postgres dependency has always
// used (generator_infra.go / generator_keycloak.go). Keycloak keeps its state
// in postgres, so it has no volume of its own and answers to the postgres
// evidence instead.
func legacyVolumeFor(id deps.ID) string {
	switch id {
	case deps.RabbitMQ:
		return "rabbitmq2"
	case deps.Zookeeper:
		return "zookeeper2"
	case deps.MongoDB:
		return "mongo2"
	default:
		return ""
	}
}

// postgresPinFromData reads PG_VERSION out of whichever postgres volume holds
// a cluster. A volume that exists but has no PG_VERSION holds no cluster (an
// empty bind dir left by a removed container) and is skipped; seedNoData means
// neither layout holds one, seedUnknown that a probe failed and the caller
// must assume rather than conclude.
func postgresPinFromData(ctx context.Context, probe dependencyProbe, preferVolumes map[string]bool) (string, seedVerdict) {
	layouts := []deps.PostgresLayout{deps.PostgresLayoutFor(18), deps.PostgresLayoutFor(17)}
	if preferVolumes[deps.PostgresVolumeLegacy] && !preferVolumes[deps.PostgresVolumeV18] {
		layouts[0], layouts[1] = layouts[1], layouts[0]
	}
	verdict := seedNoData
	for _, l := range layouts {
		exists, err := probe.VolumeExists(ctx, l.Volume)
		if err != nil {
			slog.Warn("Dependency seed: postgres volume check failed", "volume", l.Volume, "err", err)
			verdict = seedUnknown
			continue
		}
		if !exists {
			continue
		}
		raw, err := probe.ReadVolumeFile(ctx, l.Volume, l.PGVersionRel)
		if errors.Is(err, errVolumeFileNotFound) {
			// The volume is there but empty — no cluster in this layout.
			continue
		}
		if err != nil {
			slog.Warn("Dependency seed: PG_VERSION unreadable; assuming the legacy image",
				"volume", l.Volume, "err", err, "image", deps.PostgresLegacyImage)
			return deps.PostgresLegacyImage, seedFound
		}
		major, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			slog.Warn("Dependency seed: PG_VERSION not a number; assuming the legacy image",
				"volume", l.Volume, "raw", raw, "image", deps.PostgresLegacyImage)
			return deps.PostgresLegacyImage, seedFound
		}
		return "postgres:" + strconv.Itoa(major), seedFound
	}
	return "", verdict
}

// reseedAfterSnapshotImport re-derives the pins of every dependency whose
// data a snapshot just replaced. Without it a namespace already on 18 that
// imports a 17 snapshot keeps a pin of 18 over 17 data.
func reseedAfterSnapshotImport(ctx context.Context, rt *namespace.Runtime, probe dependencyProbe, importedVolumes []string) {
	imported := make(map[string]bool, len(importedVolumes))
	for _, v := range importedVolumes {
		imported[v] = true
	}
	existing := rt.DependencyPins()
	for _, d := range deps.All() {
		touched := false
		switch d.ID() {
		case deps.Postgres, deps.Keycloak:
			// Keycloak's state lives in the postgres data, so a restored
			// postgres volume moves its pin too.
			touched = imported[deps.PostgresVolumeLegacy] || imported[deps.PostgresVolumeV18]
		default:
			touched = imported[legacyVolumeFor(d.ID())]
		}
		if touched {
			delete(existing, d.ID())
		}
	}
	seedCtx, cancel := context.WithTimeout(ctx, dependencySeedTimeout)
	defer cancel()
	for id, img := range seedDependencyPins(seedCtx, existing, probe, imported) {
		slog.Info("Dependency pin re-seeded after snapshot import", "dependency", id, "image", img)
		rt.SetDependencyPin(id, img)
	}
}
