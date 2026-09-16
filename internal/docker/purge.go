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
		return labelsMatchPair(labels, nsID, wsID)
	}

	// Containers (running or stopped) — force-remove with their anonymous volumes.
	c.removeNamespaceContainers(ctx, nsID, wsID, "PurgeNamespace", true, nil)

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
// CONTAINER on the host and reduces them to the distinct (namespace, workspace)
// pairs that are NOT in keep — leftovers from namespaces that were deleted or
// whose storage was wiped (the migration-test churn) while their containers
// kept running (detach leaves them up). keep is
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
	var labelSets []map[string]string

	// CONTAINERS ONLY, because containers are all the removing phase acts on.
	// It used to enumerate named volumes and networks as well — which was right
	// while the sweep purged them — and a pair whose containers are already gone
	// now yields a target that removes nothing and logs nothing. Two host-wide
	// Docker calls on every desktop start, for a decision that cannot change.
	if cs, err := c.ListAllLauncherContainers(ctx); err != nil {
		slog.Warn("SweepOrphans: list containers failed", "err", err)
	} else {
		for _, ct := range cs {
			labelSets = append(labelSets, ct.Labels)
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
		acted := c.removeNamespaceContainers(ctx, t.NS, t.WS, "SweepOrphans", false, func() {
			slog.Info("SweepOrphans: removing containers of an orphaned namespace "+
				"(its volumes and network are kept)", "ns", t.NS, "ws", t.WS)
		})
		if acted {
			removed = append(removed, t.NS)
		}
	}
	return removed
}

// removeNamespaceContainers force-removes every container labeled with nsID
// whose WORKSPACE label matches wsID, and reports whether it removed any.
//
// It is the ONE spelling of "which containers belong to this pair". The match
// on the workspace is case-insensitive because that is what orphanKey
// lower-cases to compare, and the sweep and the purge disagreeing about it
// would mean one of them acting on a container the other considers somebody
// else's — so the rule may not exist twice.
//
// removeVolumes is the callers' only real difference: a purge takes the
// container's anonymous volumes with it (its data is being deleted on purpose),
// the startup sweep does not (it is only freeing ports). onFirst, when set,
// runs once before the first removal, which is how a caller keeps its own
// "here is what I am about to do" line without this function guessing at it.
func (c *Client) removeNamespaceContainers(
	ctx context.Context, nsID, wsID, logPrefix string, removeVolumes bool, onFirst func(),
) bool {
	nsFilter := make(client.Filters).Add("label", LabelNamespace+"="+nsID)
	cs, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: nsFilter})
	if err != nil {
		slog.Warn(logPrefix+": list containers failed", "ns", nsID, "err", err)
		return false
	}
	acted := false
	for _, ct := range cs.Items {
		if !labelsMatchPair(ct.Labels, nsID, wsID) {
			continue
		}
		if !acted {
			if onFirst != nil {
				onFirst()
			}
			acted = true
		}
		if _, rmErr := c.cli.ContainerRemove(ctx, ct.ID,
			client.ContainerRemoveOptions{Force: true, RemoveVolumes: removeVolumes}); rmErr != nil {
			slog.Warn(logPrefix+": remove container failed", "ns", nsID, "container", ct.ID, "err", rmErr)
		}
	}
	return acted
}

// labelsMatchPair is the ONE membership rule: do these resource labels name
// this (namespace, workspace) pair? The namespace is matched exactly — labels
// carry the raw id — and the WORKSPACE is folded, because orphanKey lower-cases
// it to build the keep set, so a pair that matches in the keep set and not here
// (or the reverse) is the sweep and the purge disagreeing about whose container
// they are looking at. The Docker-side label filter already narrows by
// namespace; re-asserting it here costs nothing and keeps the whole rule
// readable — and testable — in one place.
func labelsMatchPair(labels map[string]string, nsID, wsID string) bool {
	return labels[LabelNamespace] == nsID && strings.EqualFold(labels[LabelWorkspace], wsID)
}

// OrphanKey builds the keep-set key for a (namespace, workspace) that still
// exists in storage, so SweepOrphans never removes its Docker resources. Must
// use the same canonicalization as the resource side (orphanKey).
func OrphanKey(nsID, wsID string) string {
	return orphanKey(nsID, wsID)
}
