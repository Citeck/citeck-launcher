package migrate

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// pgInventory is the shape the verify step compares: what a logical dump must
// carry across. Row counts are deliberately not part of it — they need a full
// scan of every table, which on a real stand costs more than the migration.
type pgInventory struct {
	Databases []string
	Roles     []string
	Tables    map[string]int // database → user-table count
}

// psqlArgs builds a psql invocation against the TCP server. Everything goes
// over 127.0.0.1 rather than the Unix socket: `docker exec` runs as the
// image's default user (root), which peer authentication would reject, and
// during first-time init the socket is answered by a TEMPORARY server that is
// torn down again — the embedded pg_hba.conf trusts 127.0.0.1, and only the
// real server listens there.
//
// -q silences the greeting, -At prints unaligned, header-less rows: one value
// per line, which is what makes the output machine-parsable.
func psqlArgs(db, sql string) []string {
	return []string{"psql", "-h", "127.0.0.1", "-U", "postgres", "-d", db, "-q", "-At", "-c", sql}
}

// readInventory lists what the cluster in container holds.
func readInventory(ctx context.Context, env Env, container string) (pgInventory, error) {
	inv := pgInventory{Tables: map[string]int{}}
	dbs, err := execLines(ctx, env, container, psqlArgs("postgres",
		"SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY 1"))
	if err != nil {
		return inv, fmt.Errorf("list databases: %w", err)
	}
	inv.Databases = dbs
	roles, err := execLines(ctx, env, container, psqlArgs("postgres",
		`SELECT rolname FROM pg_roles WHERE rolname NOT LIKE 'pg\_%' ORDER BY 1`))
	if err != nil {
		return inv, fmt.Errorf("list roles: %w", err)
	}
	inv.Roles = roles
	for _, db := range dbs {
		out, err := execLines(ctx, env, container, psqlArgs(db,
			"SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')"))
		if err != nil {
			return inv, fmt.Errorf("count tables in %s: %w", db, err)
		}
		n := 0
		if len(out) > 0 {
			if n, err = strconv.Atoi(strings.TrimSpace(out[0])); err != nil {
				return inv, fmt.Errorf("count tables in %s: %q is not a number", db, out[0])
			}
		}
		inv.Tables[db] = n
	}
	return inv, nil
}

// execLines runs a query and returns its STDOUT lines, sorted. Only stdout:
// psql writes results there and diagnostics to stderr, so reading the merged
// streams would let a notice become a database name. A failed command is
// reported with its stderr, which is where the reason is.
func execLines(ctx context.Context, env Env, container string, cmd []string) ([]string, error) {
	stdout, stderr, code, err := env.Exec(ctx, container, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", cmd[0], container, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("%s in %s: exit %d: %s", cmd[0], container, code, tail(stderr))
	}
	var lines []string
	for l := range strings.SplitSeq(stdout, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return lines, nil
}

// diff lists how b (the target) differs from a (the source). Any difference is
// a failure: the restore replays one script into an empty cluster, so both
// more and fewer objects mean it did not do what it was asked.
func (a pgInventory) diff(b pgInventory) []string {
	var out []string
	if strings.Join(a.Databases, ",") != strings.Join(b.Databases, ",") {
		out = append(out, fmt.Sprintf("databases differ: source %v, target %v", a.Databases, b.Databases))
	}
	if strings.Join(a.Roles, ",") != strings.Join(b.Roles, ",") {
		out = append(out, fmt.Sprintf("roles differ: source %v, target %v", a.Roles, b.Roles))
	}
	dbs := make([]string, 0, len(a.Tables))
	for db := range a.Tables {
		dbs = append(dbs, db)
	}
	sort.Strings(dbs) // map order is random; a verdict must read the same every time
	for _, db := range dbs {
		if m, ok := b.Tables[db]; !ok || m != a.Tables[db] {
			out = append(out, fmt.Sprintf("table count differs in %s: source %d, target %d", db, a.Tables[db], m))
		}
	}
	return out
}
