package namespace

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	dcontainer "github.com/moby/moby/api/types/container"
)

// portConflict describes a published host port required by one of our apps that
// is already published by a running launcher container in a DIFFERENT namespace.
type portConflict struct {
	hostPort  int
	app       string // our app that wants the port
	ns        string // foreign namespace currently holding it
	container string // foreign container name (for logs)
	id        string // foreign container ID (used to stop it)
}

// hostPortOf extracts the host-side port from a Docker port mapping spec.
// Accepts "host:container" ("1025:1025", "8070:80") and "ip:host:container"
// ("127.0.0.1:1025:1025"). Returns 0 for bare/exposed-only or unparseable specs.
func hostPortOf(spec string) int {
	parts := strings.Split(spec, ":")
	var hostPart string
	switch len(parts) {
	case 2:
		hostPart = parts[0]
	case 3:
		hostPart = parts[1]
	default:
		return 0
	}
	// A host range ("8000-8005") has no single port — ignore.
	n, err := strconv.Atoi(hostPart)
	if err != nil {
		return 0
	}
	return n
}

// detectHostPortConflicts reports PUBLISHED host ports required by `apps` that
// are already published by a running launcher container belonging to a
// namespace other than myNsID.
//
// Scope and safety:
//   - Only PUBLISHED host ports matter. Container-internal ports never collide
//     (each namespace runs on its own Docker network), so the want-set is built
//     from host-mapped specs only (hostPortOf == 0 for bare/exposed ports) and
//     the holder side uses PublicPort (0 when unpublished).
//   - Only containers WE created are ever considered: a missing
//     citeck.launcher label means it isn't ours, so it is skipped. Callers must
//     never stop a container that isn't a launcher container.
//   - Same-namespace holders are excluded — those are our own containers in the
//     current namespace, driven by the regenerate/adopt path.
//
// This catches leftovers from any source (a crashed/OOM-killed daemon, a
// detached upgrade, or another workspace) that the per-namespace start path is
// otherwise blind to, because mailhog / onlyoffice / pgadmin publish fixed host
// ports shared across all namespaces.
func detectHostPortConflicts(apps []appdef.ApplicationDef, foreign []dcontainer.Summary, myNsID string) []portConflict {
	want := make(map[int]string)
	for _, a := range apps {
		for _, p := range a.Ports {
			if hp := hostPortOf(p); hp > 0 {
				want[hp] = a.Name
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	var out []portConflict
	seen := make(map[int]bool)
	for _, c := range foreign {
		// Hard safety guard: only ever consider containers we created. Anything
		// without the launcher label is not ours and must never be touched.
		if c.Labels[docker.LabelLauncher] != "true" {
			continue
		}
		if c.Labels[docker.LabelNamespace] == myNsID {
			continue
		}
		if c.State != "running" {
			continue
		}
		for _, p := range c.Ports {
			hp := int(p.PublicPort) // host-published port; 0 when not published
			if hp == 0 || seen[hp] {
				continue
			}
			app, ok := want[hp]
			if !ok {
				continue
			}
			name := ""
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			out = append(out, portConflict{
				hostPort:  hp,
				app:       app,
				ns:        c.Labels[docker.LabelNamespace],
				container: name,
				id:        c.ID,
			})
			seen[hp] = true
		}
	}
	return out
}

// resolveHostPortConflicts stops+removes launcher containers from OTHER
// namespaces that hold a published host port one of our apps needs, before this
// namespace's start proceeds. It only ever touches containers we created (the
// launcher-label guard in detectHostPortConflicts), never third-party
// containers that happen to share a port. Best-effort: a failure to list or
// stop never blocks the start (the worst case degrades to the old cryptic
// "port already allocated" error).
func (r *Runtime) resolveHostPortConflicts(ctx context.Context, apps []appdef.ApplicationDef) {
	foreign, err := r.docker.ListAllLauncherContainers(ctx)
	if err != nil {
		// Best-effort: if we can't list containers, skip the pre-flight and let
		// start proceed (worst case degrades to the old "port already allocated"
		// error). Log so the silent degradation is visible in daemon logs.
		slog.Debug("Port-conflict pre-flight skipped: list launcher containers failed", "err", err)
		return
	}
	stopped := make(map[string]bool) // dedupe: one container may hold several ports
	for _, c := range detectHostPortConflicts(apps, foreign, r.nsID) {
		if c.id == "" || stopped[c.id] {
			continue
		}
		stopped[c.id] = true
		slog.Warn("Stopping our container from another namespace that holds a required host port",
			"forApp", c.app, "port", c.hostPort, "heldByNamespace", c.ns, "container", c.container)
		if err := r.docker.StopAndRemoveContainer(ctx, c.id, conflictStopTimeoutSec); err != nil {
			slog.Error("Failed to stop conflicting container; start may still fail on this port",
				"container", c.container, "err", err)
		}
	}
}

// conflictStopTimeoutSec is the docker stop timeout when freeing a port held by
// our own container from another namespace — short, since we're discarding it.
const conflictStopTimeoutSec = 5
