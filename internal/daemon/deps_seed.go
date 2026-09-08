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
// wait (after which every dependency falls back to its legacy image, the
// pre-feature behavior) rather than hanging it forever.
const dependencySeedTimeout = 30 * time.Second

// dependencyProbe is what seeding needs from Docker and the filesystem,
// narrowed so the rules are unit-testable without an engine.
type dependencyProbe interface {
	// ContainerImage returns the image of the namespace's container for app,
	// ok=false when no such container exists.
	ContainerImage(ctx context.Context, app string) (image string, ok bool, err error)
	// VolumeExists reports whether the plain-named data volume exists.
	VolumeExists(ctx context.Context, volume string) (bool, error)
	// ReadVolumeFile returns the content of a file inside a data volume.
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
		return "", fmt.Errorf("volume %s: %w", volume, os.ErrNotExist)
	}
	out, code, err := p.dc.RunUtilsContainer(ctx, []string{"cat", "/vol/" + rel}, []string{v.Name + ":/vol:ro"})
	if err != nil {
		return "", fmt.Errorf("read %s in %s: %w", rel, volume, err)
	}
	if code != 0 {
		return "", fmt.Errorf("cat %s in %s: exit %d: %s", rel, volume, code, strings.TrimSpace(out))
	}
	return out, nil
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

// seedDependencyPins answers "what does this namespace's data run on?" for
// every registered dependency that has no pin yet, and returns the pins to
// add. Order of evidence: the container (survives launcher upgrades), then the
// data itself (PostgreSQL's PG_VERSION), then the descriptor's legacy image.
// No data at all means no pin — the namespace is fresh and the candidate
// applies. preferVolumes marks volumes a snapshot import just restored; when
// both postgres layouts exist on disk the imported one is the truth.
//
// Every probe failure degrades to the legacy image rather than to "no pin":
// seeding runs only where no pin exists, i.e. on pre-feature data, and the
// legacy image is exactly what that data has been running on. "No pin" would
// instead let a Docker outage start PostgreSQL 18 on an empty new-layout
// volume and settle the pin there, orphaning the 17 data.
func seedDependencyPins(ctx context.Context, existing map[deps.ID]string, probe dependencyProbe, preferVolumes map[string]bool) map[deps.ID]string {
	out := make(map[deps.ID]string)
	for _, d := range deps.All() {
		if img := existing[d.ID()]; img != "" {
			continue
		}
		img, ok, err := probe.ContainerImage(ctx, d.AppName())
		if err != nil {
			slog.Warn("Dependency seed: container inspect failed", "dependency", d.ID(), "err", err)
		}
		if ok && img != "" {
			out[d.ID()] = img
			continue
		}
		if d.ID() == deps.Postgres {
			if pin, found := postgresPinFromData(ctx, probe, preferVolumes); found {
				out[d.ID()] = pin
			}
			continue
		}
		volume := legacyVolumeFor(d.ID())
		if volume == "" {
			continue
		}
		exists, err := probe.VolumeExists(ctx, volume)
		if err != nil {
			slog.Warn("Dependency seed: volume check failed", "dependency", d.ID(), "volume", volume, "err", err)
		}
		if exists {
			slog.Warn("Dependency seed: no container and no version file; assuming the legacy image",
				"dependency", d.ID(), "image", d.LegacyImage())
			out[d.ID()] = d.LegacyImage()
		}
	}
	return out
}

// legacyVolumeFor is the data volume each non-postgres dependency has always
// used (generator_infra.go / generator_keycloak.go). Keycloak keeps its state
// in postgres, so it has no volume and is seeded only from a container.
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
// data. found=false only when neither volume exists.
func postgresPinFromData(ctx context.Context, probe dependencyProbe, preferVolumes map[string]bool) (string, bool) {
	layouts := []deps.PostgresLayout{deps.PostgresLayoutFor(18), deps.PostgresLayoutFor(17)}
	if preferVolumes[deps.PostgresVolumeLegacy] && !preferVolumes[deps.PostgresVolumeV18] {
		layouts[0], layouts[1] = layouts[1], layouts[0]
	}
	for _, l := range layouts {
		exists, err := probe.VolumeExists(ctx, l.Volume)
		if err != nil {
			slog.Warn("Dependency seed: postgres volume check failed", "volume", l.Volume, "err", err)
			continue
		}
		if !exists {
			continue
		}
		raw, err := probe.ReadVolumeFile(ctx, l.Volume, l.PGVersionRel)
		if err != nil {
			slog.Warn("Dependency seed: PG_VERSION unreadable; assuming the legacy image",
				"volume", l.Volume, "err", err, "image", deps.PostgresLegacyImage)
			return deps.PostgresLegacyImage, true
		}
		major, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			slog.Warn("Dependency seed: PG_VERSION not a number; assuming the legacy image",
				"volume", l.Volume, "raw", raw, "image", deps.PostgresLegacyImage)
			return deps.PostgresLegacyImage, true
		}
		return "postgres:" + strconv.Itoa(major), true
	}
	return "", false
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
		if d.ID() == deps.Postgres {
			touched = imported[deps.PostgresVolumeLegacy] || imported[deps.PostgresVolumeV18]
		} else {
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
