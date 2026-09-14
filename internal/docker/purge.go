package docker

import (
	"context"
	"log/slog"
	"strings"

	"github.com/moby/moby/client"
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
	nsFilter := func() client.Filters {
		return make(client.Filters).Add("label", LabelNamespace+"="+nsID)
	}
	wsMatch := func(labels map[string]string) bool {
		return strings.EqualFold(labels[LabelWorkspace], wsID)
	}

	// Containers (running or stopped) — force-remove with their anonymous volumes.
	if cs, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: nsFilter()}); err != nil {
		slog.Warn("PurgeNamespace: list containers failed", "ns", nsID, "err", err)
	} else {
		for _, ct := range cs.Items {
			if !wsMatch(ct.Labels) {
				continue
			}
			if _, rmErr := c.cli.ContainerRemove(ctx, ct.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); rmErr != nil {
				slog.Warn("PurgeNamespace: remove container failed", "ns", nsID, "container", ct.ID, "err", rmErr)
			}
		}
	}

	// Named volumes — the actual namespace data.
	if vl, err := c.cli.VolumeList(ctx, client.VolumeListOptions{Filters: nsFilter()}); err != nil {
		slog.Warn("PurgeNamespace: list volumes failed", "ns", nsID, "err", err)
	} else {
		for _, v := range vl.Items {
			if !wsMatch(v.Labels) {
				continue
			}
			if _, rmErr := c.cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{Force: true}); rmErr != nil {
				slog.Warn("PurgeNamespace: remove volume failed", "ns", nsID, "volume", v.Name, "err", rmErr)
			}
		}
	}

	// Network.
	if nets, err := c.cli.NetworkList(ctx, client.NetworkListOptions{Filters: nsFilter()}); err != nil {
		slog.Warn("PurgeNamespace: list networks failed", "ns", nsID, "err", err)
	} else {
		for _, n := range nets.Items {
			if !wsMatch(n.Labels) {
				continue
			}
			if _, rmErr := c.cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{}); rmErr != nil {
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
// remove safely. Resources with an empty WORKSPACE label are skipped for a
// different reason: only a launcher in SERVER mode writes that (Client.workspace
// is empty there), so the pair belongs to a stand this desktop profile does not
// own and whose store it cannot read. Pure (no Docker calls) so the keep/dedup
// logic is unit-tested independently of the SDK.
func collectOrphanTargets(labelSets []map[string]string, keep map[string]bool) []OrphanTarget {
	seen := map[string]bool{}
	var targets []OrphanTarget
	for _, labels := range labelSets {
		ns := labels[LabelNamespace]
		ws := labels[LabelWorkspace]
		if ns == "" || ws == "" {
			continue
		}
		key := orphanKey(ns, ws)
		if keep[key] || seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, OrphanTarget{NS: ns, WS: ws})
	}
	return targets
}

// OrphanTarget is a (namespace, workspace) pair slated for purge.
type OrphanTarget struct {
	NS string
	WS string
}

// FindOrphans is the sweep's DECIDING phase: it enumerates every launcher
// container, named volume and network on the host and reduces them to the
// distinct (namespace, workspace) pairs that are NOT in keep — leftovers from
// namespaces that were deleted or whose storage was wiped (the migration-test
// churn) while their containers kept running (detach leaves them up). keep is
// built from storage via OrphanKey for every (workspace, namespace) that still
// exists, so the active namespace and every stored namespace are protected.
//
// It REMOVES NOTHING, which is what lets the caller bound it tightly: an
// enumeration that fails is logged and contributes no labels, so a Docker that
// cannot be reached simply decides that there is nothing to purge. Splitting it
// from the removals is the whole point — the two deserve very different
// budgets, and sharing one made an unreachable Docker cost the removal budget
// before the daemon could get on with the namespace.
func (c *Client) FindOrphans(ctx context.Context, keep map[string]bool) []OrphanTarget {
	launcherFilter := make(client.Filters).Add("label", LabelLauncher+"=true")
	var labelSets []map[string]string

	if cs, err := c.ListAllLauncherContainers(ctx); err != nil {
		slog.Warn("SweepOrphans: list containers failed", "err", err)
	} else {
		for _, ct := range cs {
			labelSets = append(labelSets, ct.Labels)
		}
	}
	if vl, err := c.cli.VolumeList(ctx, client.VolumeListOptions{Filters: launcherFilter}); err != nil {
		slog.Warn("SweepOrphans: list volumes failed", "err", err)
	} else {
		for _, v := range vl.Items {
			labelSets = append(labelSets, v.Labels)
		}
	}
	if nets, err := c.cli.NetworkList(ctx, client.NetworkListOptions{Filters: launcherFilter}); err != nil {
		slog.Warn("SweepOrphans: list networks failed", "err", err)
	} else {
		for _, n := range nets.Items {
			labelSets = append(labelSets, n.Labels)
		}
	}

	return collectOrphanTargets(labelSets, keep)
}

// RemoveOrphanContainers is the sweep's REMOVING phase, and it removes
// CONTAINERS and nothing else. The startup sweep exists to free the published
// host ports a leftover namespace is squatting, which is a property of its
// containers; reclaiming disk was never its job. It used to call PurgeNamespace
// — containers with RemoveVolumes, then every named volume of the pair, then
// the network — so a pair the keep set could not account for lost its data
// silently, at daemon start, with two INFO lines to show for it. Removing data
// is now reachable only through something the operator typed: deleting the
// namespace or the workspace, or `citeck clean --force`.
//
// RemoveVolumes is FALSE for the same reason: an anonymous volume is still the
// container's data, and nobody asked for it to go.
//
// Returns the distinct namespace ids it removed containers for, so the caller
// can log them. Desktop-only by convention (the caller gates on IsDesktopMode);
// server mode has a single file-backed namespace and no orphan churn.
func (c *Client) RemoveOrphanContainers(ctx context.Context, targets []OrphanTarget) []string {
	removed := make([]string, 0, len(targets))
	for _, t := range targets {
		nsFilter := make(client.Filters).Add("label", LabelNamespace+"="+t.NS)
		cs, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: nsFilter})
		if err != nil {
			slog.Warn("SweepOrphans: list containers failed", "ns", t.NS, "err", err)
			continue
		}
		acted := false
		for _, ct := range cs.Items {
			if !strings.EqualFold(ct.Labels[LabelWorkspace], t.WS) {
				continue
			}
			if !acted {
				slog.Info("SweepOrphans: removing containers of an orphaned namespace "+
					"(its volumes and network are kept)", "ns", t.NS, "ws", t.WS)
				acted = true
			}
			if _, rmErr := c.cli.ContainerRemove(ctx, ct.ID,
				client.ContainerRemoveOptions{Force: true, RemoveVolumes: false}); rmErr != nil {
				slog.Warn("SweepOrphans: remove container failed", "ns", t.NS, "container", ct.ID, "err", rmErr)
			}
		}
		if acted {
			removed = append(removed, t.NS)
		}
	}
	return removed
}

// OrphanKey builds the keep-set key for a (namespace, workspace) that still
// exists in storage, so SweepOrphans never removes its Docker resources. Must
// use the same canonicalization as the resource side (orphanKey).
func OrphanKey(nsID, wsID string) string {
	return orphanKey(nsID, wsID)
}
