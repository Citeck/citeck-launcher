package migratetest

import (
	"fmt"
	"strconv"
	"strings"
)

// PostgresInventory is what a scripted PostgreSQL answers the inventory
// queries a migration's verify step runs.
type PostgresInventory struct {
	Databases []string
	Roles     []string
	Tables    map[string]int // database → user-table count
}

// HealthyPostgres is the inventory of a small Citeck stand: two databases,
// their owner role plus the bootstrap superuser, and a table count per
// database. It is the arrangement most plan tests want on BOTH sides.
func HealthyPostgres() PostgresInventory {
	return PostgresInventory{
		Databases: []string{"citeck_emodel", "citeck_keycloak"},
		Roles:     []string{"citeck_emodel", "postgres"},
		Tables:    map[string]int{"citeck_emodel": 42, "citeck_keycloak": 91},
	}
}

// ToleratedRestoreError is the one line pg_dumpall's script always produces on
// a fresh cluster (it re-creates the bootstrap superuser) and the only one a
// restore is allowed to survive.
const ToleratedRestoreError = `psql:/citeck/depsmig/dump.sql:12: ERROR:  role "postgres" already exists`

// PostgresExec scripts a healthy PostgreSQL for a migration plan: the TCP
// readiness probe, the inventory queries, pg_dumpall, the restore and
// vacuumdb. Inventories are per container name, with the "" key as the
// default for any container the map does not mention — that is how a test
// makes the target differ from the source.
//
// Output follows the real tools: query results on stdout (the plan parses
// only that), psql's diagnostics on stderr. A test that needs one command to
// misbehave wraps the returned function rather than reimplementing the rest.
func PostgresExec(inv map[string]PostgresInventory) ExecFunc {
	return func(container, cmdline string) (string, string, int, error) {
		in, ok := inv[container]
		if !ok {
			in = inv[""]
		}
		switch {
		case strings.HasPrefix(cmdline, "pg_isready"):
			return "127.0.0.1:5432 - accepting connections", "", 0, nil
		case strings.HasPrefix(cmdline, "pg_dumpall"):
			return "", "", 0, nil
		case strings.HasPrefix(cmdline, "vacuumdb"):
			return "", "", 0, nil
		case strings.HasPrefix(cmdline, "psql") && strings.Contains(cmdline, " -f "):
			// A restore reports its errors on stderr even when it exits 0.
			return "", ToleratedRestoreError, 0, nil
		case strings.Contains(cmdline, "select 1"):
			return "1", "", 0, nil
		case strings.Contains(cmdline, "pg_database"):
			return lines(in.Databases), "", 0, nil
		case strings.Contains(cmdline, "pg_roles"):
			return lines(in.Roles), "", 0, nil
		case strings.Contains(cmdline, "information_schema.tables"):
			db := flagValue(cmdline, "-d")
			if db == "" {
				return "", fmt.Sprintf("psql: no database in %q", cmdline), 2, nil
			}
			return strconv.Itoa(in.Tables[db]) + "\n", "", 0, nil
		}
		return "", "", 0, nil
	}
}

func lines(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return strings.Join(ss, "\n") + "\n"
}

// flagValue reads the argument that follows flag in a joined command line.
func flagValue(cmdline, flag string) string {
	fields := strings.Fields(cmdline)
	for i, f := range fields {
		if f == flag && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
