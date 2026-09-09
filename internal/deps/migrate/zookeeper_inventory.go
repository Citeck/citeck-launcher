package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// zkInventory is ZooKeeper's picture of its own data: every znode path, which
// of them are EPHEMERAL, and the server's own two counters.
//
// The split between persistent and ephemeral paths is the whole design of the
// verify. Ephemeral znodes belong to client sessions; the copy is booted with
// no clients at all, so their sessions expire while the migration runs and
// they are EXPECTED to be gone afterwards. A persistent path that disappeared
// is the failure that matters: that is where ecos/patches/<app>/results/*
// lives, and its absence silently re-runs a local patch on every webapp.
type zkInventory struct {
	Paths      []string
	Ephemerals []string
	ZnodeCount int
	DataSize   int64
}

// zkMonitor is the subset of /commands/monitor the verify reads.
type zkMonitor struct {
	ZnodeCount int   `json:"znode_count"`
	DataSize   int64 `json:"approximate_data_size"`
}

// zkDump is the subset of /commands/dump the verify reads. The key really is
// session_id_to_ephemeral_paths (verified against a real 3.9.5 server) — the
// name a reader expects, "ephemeral_nodes", does not exist.
type zkDump struct {
	SessionEphemerals map[string][]string `json:"session_id_to_ephemeral_paths"`
}

// readZkInventory lists what the server in container holds.
//
// Everything runs INSIDE the container against loopback: a temp container
// publishes no ports, and the image ships curl and zkCli.sh (verified).
func readZkInventory(ctx context.Context, env Env, container string) (Inventory, error) {
	inv := zkInventory{}

	var monitor zkMonitor
	if err := zkAdminJSON(ctx, env, container, "monitor", &monitor); err != nil {
		return inv, err
	}
	inv.ZnodeCount, inv.DataSize = monitor.ZnodeCount, monitor.DataSize

	var dump zkDump
	if err := zkAdminJSON(ctx, env, container, "dump", &dump); err != nil {
		return inv, err
	}
	for _, paths := range dump.SessionEphemerals {
		inv.Ephemerals = append(inv.Ephemerals, paths...)
	}
	sort.Strings(inv.Ephemerals)

	stdout, stderr, code, err := env.Exec(ctx, container,
		[]string{"zkCli.sh", "-server", zkClientServer, "ls", "-R", "/"})
	if err != nil {
		return inv, fmt.Errorf("list the znodes of %s: %w", container, err)
	}
	if code != 0 {
		return inv, fmt.Errorf("list the znodes of %s: exit %d: %s", container, code, tail(stderr+"\n"+stdout))
	}
	inv.Paths = zkPaths(stdout)
	return inv, nil
}

// zkAdminJSON runs one AdminServer command and decodes its answer.
//
// A body that is not JSON is an ERROR and never an empty result: an admin
// server that answered with an error page would otherwise be read as a server
// with no znodes in it, which is the one verdict that must never be reached by
// accident.
func zkAdminJSON(ctx context.Context, env Env, container, command string, into any) error {
	stdout, stderr, code, err := env.Exec(ctx, container, zkAdminCmd(command))
	if err != nil {
		return fmt.Errorf("%s of %s: %w", command, container, err)
	}
	if code != 0 {
		return fmt.Errorf("%s of %s: exit %d: %s", command, container, code, tail(stderr))
	}
	if err := json.Unmarshal([]byte(stdout), into); err != nil {
		return fmt.Errorf("%s of %s: %w", command, container, err)
	}
	return nil
}

// zkPaths reads the recursive listing out of zkCli's output.
//
// zkCli writes its own log lines to STDOUT, mixed in with the results
// (measured), so the results have to be recognized rather than merely
// collected. Every log line begins with a timestamp or a word, and a znode
// path begins with a slash — including the ones that CONTAIN a path
// ("Client environment:java.class.path=/apache-zookeeper-…"), which is why the
// test is on the PREFIX and not on containment.
func zkPaths(stdout string) []string {
	seen := map[string]bool{}
	var out []string
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if !strings.HasPrefix(line, "/") || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// Diff compares the tree before the upgrade with the tree after it.
//
// Only a lost PERSISTENT path fails. Everything else — an expired ephemeral, a
// path the new version created for itself, both counters — is reported and
// nothing more, because none of it is data the namespace put there.
func (a zkInventory) Diff(after Inventory) (problems, notes []string) {
	b, ok := after.(zkInventory)
	if !ok {
		return []string{"the inventory of the upgraded server could not be compared with the original"}, nil
	}
	persistent, _ := setDiff(a.Paths, a.Ephemerals)
	if missing, _ := setDiff(persistent, b.Paths); len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d persistent znode(s) missing after the upgrade: %s",
			len(missing), namesPreview(missing)))
	}
	if _, appeared := setDiff(a.Paths, b.Paths); len(appeared) > 0 {
		notes = append(notes, fmt.Sprintf("%d znode(s) that did not exist before: %s",
			len(appeared), namesPreview(appeared)))
	}
	gone, added := setDiff(a.Ephemerals, b.Ephemerals)
	if len(gone) > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d ephemeral znode(s) gone with the sessions that owned them: %s",
			len(gone), namesPreview(gone)))
	}
	if len(added) > 0 {
		notes = append(notes, fmt.Sprintf("%d new ephemeral znode(s): %s", len(added), namesPreview(added)))
	}
	if a.ZnodeCount != b.ZnodeCount {
		notes = append(notes, fmt.Sprintf("znode count %d → %d", a.ZnodeCount, b.ZnodeCount))
	}
	if a.DataSize != b.DataSize {
		notes = append(notes, fmt.Sprintf("approximate data size %d → %d bytes", a.DataSize, b.DataSize))
	}
	return problems, notes
}
