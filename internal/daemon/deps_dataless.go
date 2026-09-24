package daemon

import (
	"context"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// datalessPins answers, for every PINNED dependency, whether the launcher can
// PROVE its data is gone — the pins the generator must not hold anything back
// for (GenerateOpts.DatalessDependencies).
//
// A pin exists to protect data. Seeding only ever ADDS pins, so one written
// while a namespace ran outlives its volumes: delete them from the Volumes page
// and the pin still names the old version, the dashboard offers an "upgrade",
// and the migration it leads to refuses because the volume it would read is
// not there (reported from the field on 2026-09-24). This is the same state
// the edit gate already resolves for a hand edit (dependencyEditPinFollows) and
// it answers with the same three rules:
//
//   - PROOF means a definite answer: any probe error keeps the pin — an
//     unreachable Docker is "I could not ask", never "there is nothing";
//   - a container, or a volume that EXISTS (even an empty one), is data;
//   - the volume asked about is the pinned GENERATION's, the one the next
//     start mounts. A retained older volume is not this pin's data, and the
//     generation is kept, so the fresh volume never lands on it.
//
// Keycloak has no volume of its own: its state is the postgres database, so it
// is judged by the postgres data volume. An open migration journal releases
// nothing — half-built volumes and temp containers may be on the host.
//
// present (nil ⇒ all) limits the probes to the dependencies the namespace has.
func datalessPins(ctx context.Context, pins map[deps.ID]deps.DependencyState, probe dependencyProbe,
	present map[deps.ID]bool, journalOpen bool,
) map[deps.ID]bool {
	if journalOpen {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, dependencySeedTimeout)
	defer cancel()

	var out map[deps.ID]bool
	for id, st := range pins {
		if st.Image == "" || (present != nil && !present[id]) {
			continue
		}
		d, ok := deps.Lookup(id)
		if !ok {
			continue
		}
		home, homeGen := d, st.Gen()
		if d.VolumeBase() == "" {
			if id != deps.Keycloak {
				continue // data somewhere this function cannot see
			}
			pg, okPg := deps.Lookup(deps.Postgres)
			if !okPg {
				continue
			}
			home, homeGen = pg, pins[deps.Postgres].Gen()
		}
		if _, running, err := probe.ContainerImage(ctx, d.AppName()); err != nil || running {
			continue
		}
		volume := deps.VolumeName(home, homeGen)
		exists, err := probe.VolumeExists(ctx, volume)
		if err != nil || exists {
			continue
		}
		slog.Debug("Dependency pin has no data: no container and no data volume",
			"dependency", id, "pinned", st.Image, "volume", volume)
		if out == nil {
			out = make(map[deps.ID]bool)
		}
		out[id] = true
	}
	return out
}
