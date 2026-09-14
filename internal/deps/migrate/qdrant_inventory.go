package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// qdrantInventory is Qdrant's picture of its own data: every collection with
// the number of POINTS in it, and every alias with the collection it names.
//
// It is deliberately only those two. A collection's CONFIG is not data and a
// new version is entitled to rewrite it — measured across 1.14.1 → 1.15.5, the
// config gained `strict_mode_config` — so comparing configs would fail every
// real migration over a field the upgrade itself added. The collection's
// `status` is excluded for the same reason and one more: the same data reads
// "green" on 1.14.1 and "grey" on 1.15.5 right after boot, because grey means
// "optimizations not yet run", which is a statement about the server's
// housekeeping and not about whether the points survived.
type qdrantInventory struct {
	// Points is collection name → points_count.
	Points map[string]int64
	// Aliases is alias name → the collection it points at.
	Aliases map[string]string
}

// qdrantCollections is the subset of GET /collections the verify reads.
type qdrantCollections struct {
	Result struct {
		Collections []struct {
			Name string `json:"name"`
		} `json:"collections"`
	} `json:"result"`
}

// qdrantCollection is the subset of GET /collections/{name} the verify reads.
//
// PointsCount is a POINTER because the field is nullable: a collection whose
// shards are still loading answers null, and a null read as 0 would be
// reported as "every point is gone" — the loudest possible way to be wrong.
type qdrantCollection struct {
	Result struct {
		PointsCount *int64 `json:"points_count"`
	} `json:"result"`
}

// qdrantAliases is the subset of GET /aliases the verify reads.
type qdrantAliases struct {
	Result struct {
		Aliases []struct {
			AliasName      string `json:"alias_name"`
			CollectionName string `json:"collection_name"`
		} `json:"aliases"`
	} `json:"result"`
}

// readQdrantInventory lists what the server in container holds.
//
// Everything runs INSIDE the container over loopback, because a temp container
// publishes no ports. The transport is bash's /dev/tcp rather than curl: the
// official image is Debian bookworm but ships NEITHER curl NOR wget (verified
// on v1.14.1 and v1.15.5), and bash — which it does ship — can speak enough
// HTTP/1.0 to ask a question and read the answer.
func readQdrantInventory(ctx context.Context, env Env, container string) (Inventory, error) {
	inv := qdrantInventory{Points: map[string]int64{}, Aliases: map[string]string{}}

	var list qdrantCollections
	if err := qdrantGetJSON(ctx, env, container, "/collections", &list); err != nil {
		return inv, err
	}
	for _, c := range list.Result.Collections {
		var detail qdrantCollection
		if err := qdrantGetJSON(ctx, env, container, "/collections/"+c.Name, &detail); err != nil {
			return inv, err
		}
		if detail.Result.PointsCount == nil {
			return inv, fmt.Errorf("collection %q of %s reports no point count: its shards are not loaded",
				c.Name, container)
		}
		inv.Points[c.Name] = *detail.Result.PointsCount
	}

	var aliases qdrantAliases
	if err := qdrantGetJSON(ctx, env, container, "/aliases", &aliases); err != nil {
		return inv, err
	}
	for _, a := range aliases.Result.Aliases {
		inv.Aliases[a.AliasName] = a.CollectionName
	}
	return inv, nil
}

// qdrantGetJSON runs one GET against the server inside the container and
// decodes its answer.
//
// A body that is not JSON is an ERROR and never an empty result — the same
// rule the ZooKeeper inventory follows, and for the same reason: a server that
// answered with an error page must not be read as a server with no
// collections in it, which is the one verdict that must never be reached by
// accident.
func qdrantGetJSON(ctx context.Context, env Env, container, path string, into any) error {
	stdout, stderr, code, err := env.Exec(ctx, container, qdrantGetCmd(path))
	if err != nil {
		return fmt.Errorf("GET %s of %s: %w", path, container, err)
	}
	if code != 0 {
		return fmt.Errorf("GET %s of %s: exit %d: %s", path, container, code, tail(stderr))
	}
	if err := json.Unmarshal([]byte(stdout), into); err != nil {
		return fmt.Errorf("GET %s of %s: %w", path, container, err)
	}
	return nil
}

// Diff compares the collections before the upgrade with the ones after it.
//
// The asymmetry is the shared rule of every copy upgrade — missing is a
// problem, appeared is a note — because nothing writes to the copy but the
// upgrade itself. A point count that moved in EITHER direction is a problem,
// though: no client is connected to either container, so neither an insert nor
// a delete has any legitimate source, and a count that dropped is exactly the
// silent loss this whole plan exists to catch.
func (a qdrantInventory) Diff(after Inventory) (problems, notes []string) {
	b, ok := after.(qdrantInventory)
	if !ok {
		return []string{"the inventory of the upgraded server could not be compared with the original"}, nil
	}

	var missing, changed, appeared []string
	for name, before := range a.Points {
		got, present := b.Points[name]
		switch {
		case !present:
			missing = append(missing, name)
		case got != before:
			changed = append(changed, fmt.Sprintf("%s: %d → %d", name, before, got))
		}
	}
	for name := range b.Points {
		if _, present := a.Points[name]; !present {
			appeared = append(appeared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(changed)
	sort.Strings(appeared)

	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d collection(s) missing after the upgrade: %s",
			len(missing), namesPreview(missing)))
	}
	if len(changed) > 0 {
		problems = append(problems, fmt.Sprintf("%d collection(s) whose point count changed: %s",
			len(changed), namesPreview(changed)))
	}
	if len(appeared) > 0 {
		notes = append(notes, fmt.Sprintf("%d collection(s) that did not exist before: %s",
			len(appeared), namesPreview(appeared)))
	}

	var lostAliases, movedAliases, newAliases []string
	for alias, target := range a.Aliases {
		got, present := b.Aliases[alias]
		switch {
		case !present:
			lostAliases = append(lostAliases, alias)
		case got != target:
			movedAliases = append(movedAliases, fmt.Sprintf("%s: %s → %s", alias, target, got))
		}
	}
	for alias := range b.Aliases {
		if _, present := a.Aliases[alias]; !present {
			newAliases = append(newAliases, alias)
		}
	}
	sort.Strings(lostAliases)
	sort.Strings(movedAliases)
	sort.Strings(newAliases)

	// An alias is how the RAG service addresses a collection, so a lost or
	// repointed one is a search that silently returns nothing — the same class
	// of failure as a lost collection, not a cosmetic difference.
	if len(lostAliases) > 0 {
		problems = append(problems, fmt.Sprintf("%d alias(es) missing after the upgrade: %s",
			len(lostAliases), namesPreview(lostAliases)))
	}
	if len(movedAliases) > 0 {
		problems = append(problems, fmt.Sprintf("%d alias(es) now naming a different collection: %s",
			len(movedAliases), namesPreview(movedAliases)))
	}
	if len(newAliases) > 0 {
		notes = append(notes, fmt.Sprintf("%d alias(es) that did not exist before: %s",
			len(newAliases), namesPreview(newAliases)))
	}
	return problems, notes
}
