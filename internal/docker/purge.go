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

// orphanKey is the canonical (namespace, workspace) identity used by the
// startup orphan-sweep. Namespace is matched exactly (it is the label value
// written at creation); workspace is folded to lower case to mirror
// PurgeNamespace's case-insensitive workspace match.
func orphanKey(nsID, wsID string) string {
	return nsID + "\x00" + strings.ToLower(wsID)
}

// collectOrphanTargets reduces a set of launcher-resource label maps to the
// distinct (namespace, workspace) pairs that should be purged: those NOT in
// keep. Resources with an empty namespace label are skipped — they cannot be
// addressed by PurgeNamespace's exact-label filter and are too ambiguous to
// remove safely. Pure (no Docker calls) so the keep/dedup logic is unit-tested
// independently of the SDK.
func collectOrphanTargets(labelSets []map[string]string, keep map[string]bool) []orphanTarget {
	seen := map[string]bool{}
	var targets []orphanTarget
	for _, labels := range labelSets {
		ns := labels[LabelNamespace]
		ws := labels[LabelWorkspace]
		if ns == "" {
			continue
		}
		key := orphanKey(ns, ws)
		if keep[key] || seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, orphanTarget{ns: ns, ws: ws})
	}
	return targets
}

// orphanTarget is a (namespace, workspace) pair slated for purge.
type orphanTarget struct {
	ns string
	ws string
}

// SweepOrphans removes every launcher Docker resource whose (namespace,
// workspace) is NOT in keep — leftovers from namespaces that were deleted or
// whose storage was wiped (the migration-test churn) while their containers
// kept running (detach leaves them up). keep is built from storage via
// OrphanKey for every (workspace, namespace) that still exists, so the active
// namespace and every stored namespace are protected.
//
// Best-effort: enumeration failures are logged and skipped; each orphan pair is
// removed via PurgeNamespace (containers → named volumes → network). Returns
// the distinct namespace ids it purged, for the caller to log. Desktop-only by
// convention (the caller gates on IsDesktopMode); server mode has a single
// file-backed namespace and no orphan churn.
func (c *Client) SweepOrphans(ctx context.Context, keep map[string]bool) []string {
	launcherFilter := filters.NewArgs(filters.Arg("label", LabelLauncher+"=true"))
	var labelSets []map[string]string

	if cs, err := c.ListAllLauncherContainers(ctx); err != nil {
		slog.Warn("SweepOrphans: list containers failed", "err", err)
	} else {
		for _, ct := range cs {
			labelSets = append(labelSets, ct.Labels)
		}
	}
	if vl, err := c.cli.VolumeList(ctx, volume.ListOptions{Filters: launcherFilter}); err != nil {
		slog.Warn("SweepOrphans: list volumes failed", "err", err)
	} else {
		for _, v := range vl.Volumes {
			if v != nil {
				labelSets = append(labelSets, v.Labels)
			}
		}
	}
	if nets, err := c.cli.NetworkList(ctx, network.ListOptions{Filters: launcherFilter}); err != nil {
		slog.Warn("SweepOrphans: list networks failed", "err", err)
	} else {
		for _, n := range nets {
			labelSets = append(labelSets, n.Labels)
		}
	}

	targets := collectOrphanTargets(labelSets, keep)
	purged := make([]string, 0, len(targets))
	for _, t := range targets {
		slog.Info("SweepOrphans: purging orphaned namespace resources", "ns", t.ns, "ws", t.ws)
		c.PurgeNamespace(ctx, t.ns, t.ws)
		purged = append(purged, t.ns)
	}
	return purged
}

// OrphanKey builds the keep-set key for a (namespace, workspace) that still
// exists in storage, so SweepOrphans never removes its Docker resources. Must
// use the same canonicalization as the resource side (orphanKey).
func OrphanKey(nsID, wsID string) string {
	return orphanKey(nsID, wsID)
}
