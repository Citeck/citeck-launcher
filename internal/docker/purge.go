package docker

import (
	"context"
	"log/slog"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
)

// PurgeNamespace removes every Docker resource belonging to (nsID, wsID) —
// containers (any state), named volumes, and the network — selected by the
// citeck.launcher.namespace / .workspace labels. It works independently of the
// namespace this client is otherwise scoped to, because a DELETED namespace is
// often not the active one. Used by namespace delete so a removed namespace's
// data volumes (postgres, mongo, …) don't leak.
//
// Best-effort: every step is logged and continues on error, so a partial Docker
// failure never blocks the delete. Named data volumes are the whole point here,
// so they are removed with force=true (the namespace is already stopped, so
// nothing is mounting them; force only matters if a stray container lingers).
func (c *Client) PurgeNamespace(ctx context.Context, nsID, wsID string) {
	// Labels carry the raw namespace id (= c.namespace at creation time), not
	// the lowercased volume-name form, so filter on the raw id. Workspace is
	// matched case-insensitively, mirroring ListVolumes / Kotlin equals(...,true).
	nsFilter := filters.NewArgs(filters.Arg("label", LabelNamespace+"="+nsID))
	wsMatch := func(labels map[string]string) bool {
		return strings.EqualFold(labels[LabelWorkspace], wsID)
	}

	// Containers (running or stopped) — force-remove with their anonymous volumes.
	if cs, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: nsFilter}); err != nil {
		slog.Warn("PurgeNamespace: list containers failed", "ns", nsID, "err", err)
	} else {
		for _, ct := range cs {
			if !wsMatch(ct.Labels) {
				continue
			}
			if rmErr := c.cli.ContainerRemove(ctx, ct.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); rmErr != nil {
				slog.Warn("PurgeNamespace: remove container failed", "ns", nsID, "container", ct.ID, "err", rmErr)
			}
		}
	}

	// Named volumes — the actual namespace data.
	if vl, err := c.cli.VolumeList(ctx, volume.ListOptions{Filters: nsFilter}); err != nil {
		slog.Warn("PurgeNamespace: list volumes failed", "ns", nsID, "err", err)
	} else {
		for _, v := range vl.Volumes {
			if v == nil || !wsMatch(v.Labels) {
				continue
			}
			if rmErr := c.cli.VolumeRemove(ctx, v.Name, true); rmErr != nil {
				slog.Warn("PurgeNamespace: remove volume failed", "ns", nsID, "volume", v.Name, "err", rmErr)
			}
		}
	}

	// Network.
	if nets, err := c.cli.NetworkList(ctx, network.ListOptions{Filters: nsFilter}); err != nil {
		slog.Warn("PurgeNamespace: list networks failed", "ns", nsID, "err", err)
	} else {
		for _, n := range nets {
			if !wsMatch(n.Labels) {
				continue
			}
			if rmErr := c.cli.NetworkRemove(ctx, n.ID); rmErr != nil {
				slog.Warn("PurgeNamespace: remove network failed", "ns", nsID, "network", n.Name, "err", rmErr)
			}
		}
	}
}
