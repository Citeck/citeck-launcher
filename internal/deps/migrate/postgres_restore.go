package migrate

import (
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// pgCreds is who this plan talks to a cluster AS.
//
// It exists because a namespace can hold more than one PostgreSQL and they do
// NOT share a superuser. The official image creates the role named by
// POSTGRES_USER (default "postgres") and a database named by POSTGRES_DB
// (default: the same name), so the observer's cluster — POSTGRES_USER=observer
// — has no "postgres" role and no "postgres" database at all. Every psql,
// pg_dumpall, pg_isready and vacuumdb the plan runs therefore names the
// CLUSTER'S OWN user and maintenance database instead of the constant that
// worked while there was exactly one cluster.
//
// Measured, not deduced: with the constant in place the observer's migration
// hung in `start-source` until the 5-minute readiness deadline and rolled back,
// because `pg_isready -U postgres` against a cluster that has no such role
// never reports ready.
type pgCreds struct {
	User string
	DB   string
}

// defaultPgCreds is what the image does with neither variable set.
var defaultPgCreds = pgCreds{User: "postgres", DB: "postgres"}

// pgCredsFromDef reads them off the namespace's OWN generated def — the same
// def the temp containers are built from, so the plan cannot disagree with the
// container it just started. It mirrors the image's rule: POSTGRES_DB defaults
// to POSTGRES_USER, which itself defaults to "postgres".
func pgCredsFromDef(def appdef.ApplicationDef) pgCreds {
	c := defaultPgCreds
	if u, ok := def.Environments.Get("POSTGRES_USER"); ok && u != "" {
		c.User = u
		c.DB = u
	}
	if db, ok := def.Environments.Get("POSTGRES_DB"); ok && db != "" {
		c.DB = db
	}
	return c
}

// RestoreCommandPrefix is the psql invocation the restore step runs. It reads
// the dump over a pipe (gunzip's stdout), never a "-f <dump>" flag, so this is
// the WHOLE invocation rather than a prefix with a tail appended — the name is
// kept because it is EXPORTED and the real-Docker integration test
// (internal/daemon/deps_integration_test.go) has to pick the restore's own
// stderr out of every command the migration ran; since RestoreScript wraps it
// in a `bash -c` pipeline, the test now finds it as a SUBSTRING of that
// script rather than as a slice prefix of the command. A hand-copied literal
// there would stop matching the day a flag is added or reordered — and, since
// a missed match means "no stderr recorded", it would silently delete the
// assertion that the tolerated `role "postgres" already exists` error is what
// a real dump produces. One source, used by RestoreScript and by the test.
//
// A fresh slice per call: RestoreScript joins it into its own script rather
// than appending to it, but the fresh-slice contract is kept so a caller that
// still appends (as the step used to) cannot corrupt a shared backing array.
func RestoreCommandPrefix(c pgCreds) []string {
	return []string{"psql", "-h", "127.0.0.1", "-U", c.User, "-d", c.DB, "-q", "-o", "/dev/null"}
}

// DefaultRestoreCommandPrefix is RestoreCommandPrefix for a cluster with the
// image's own defaults. It exists for the real-Docker integration test, which
// has to identify the restore's stderr among every command the migration ran
// and migrates the stand's own database.
func DefaultRestoreCommandPrefix() []string { return RestoreCommandPrefix(defaultPgCreds) }

// gzipLevel is the compression the dump is written at.
//
// 1, not the default 6: the dump is a transient file inside a migration that
// has already stopped the namespace, so the seconds matter and the last few
// percent of ratio do not. SQL text compresses several-fold even at level 1,
// which is the whole of the saving; the same reasoning already picks level 1
// for the JVM heap dumps this launcher configures.
const gzipLevel = "1"

// DumpScript is the command the dump step runs: pg_dumpall piped into gzip.
//
// It is `bash -c` and not `sh -c`, and it sets pipefail, and those two are one
// decision. A pipe reports only its LAST command's status, so without pipefail
// a pg_dumpall that died halfway is followed by a gzip that exits 0 — and the
// migration restores a truncated cluster and verifies it against an inventory
// read from the same half-dumped source. /bin/sh in the postgres image is
// dash, which only grew pipefail in 0.5.12; bash is present (5.2) and has had
// it since forever, so bash is the one that cannot be wrong on an older base.
func DumpScript(c pgCreds, outPath string) []string {
	return []string{"bash", "-c", "set -o pipefail; " +
		"pg_dumpall -h 127.0.0.1 -U " + c.User + " | gzip -" + gzipLevel + " > " + shellQuote(outPath)}
}

// RestoreScript is the command the restore step runs: the archive decompressed
// into the SAME psql invocation RestoreCommandPrefix names — so the flags have
// one source and the integration test can still identify the restore's own
// stderr by that prefix, now as a substring of this script.
func RestoreScript(c pgCreds, dumpPath string) []string {
	return []string{"bash", "-c", "set -o pipefail; " +
		"gunzip -c " + shellQuote(dumpPath) + " | " + strings.Join(RestoreCommandPrefix(c), " ")}
}

// shellQuote wraps a path in single quotes for the two scripts above. The
// paths are built by the launcher (the dump directory plus a fixed file
// name), so this guards a path with a space in it rather than hostile input —
// but a dump directory under a user's home is exactly where a space appears.
//
// An embedded single quote is escaped using the standard POSIX idiom: close
// the open quote, emit a backslash-escaped literal quote outside any
// quoting, then reopen the quote. An earlier draft of this function tripled
// the quote character instead, which is wrong — the shell reads that as three
// adjacent quoted-and-unquoted words that concatenate back together with the
// quote character simply gone, not preserved — so a path containing a quote
// would have been silently mangled rather than passed through.
func shellQuote(s string) string {
	const escapedQuote = `'\''`
	return "'" + strings.ReplaceAll(s, "'", escapedQuote) + "'"
}

// restoreErrors extracts the ERROR lines psql printed on STDERR while
// replaying a pg_dumpall script into a fresh cluster.
//
// psql is run WITHOUT ON_ERROR_STOP on purpose: pg_dumpall emits
// `CREATE ROLE <bootstrap superuser>` and the PostgreSQL documentation calls
// that failure harmless ("it won't hurt for the CREATE to fail"), so it is
// tolerated and every other error fails the restore. Stopping at the first
// error instead would abort a restore that is in fact perfect, on the very
// first statement of every dump.
//
// TOLERATED IS EXACTLY WHAT THE IMAGE ITSELF CREATED, by name — the role
// POSTGRES_USER names and the database POSTGRES_DB names — and nothing else.
// The second one only became visible with a SECOND cluster: for the stand's own
// database POSTGRES_DB is unset, so pg_dumpall emits no `CREATE DATABASE
// postgres` and the case never arose, while the observer's cluster is created
// with POSTGRES_DB=observer and its dump says `CREATE DATABASE observer` about
// a database `initdb` has just made (measured on a real stand: the migration
// failed at `restore` with exactly these two lines). Restoring INTO that
// database is what the dump does next, so the data lands where it should; what
// is lost is only what CREATE DATABASE would have set, and tolerating the
// specific name cannot hide a target volume that was not empty, because a
// fresh cluster holds no other database.
//
// The match is case-sensitive: psql spells its severities upper-case, while a
// lower-case "error:" is text being replayed out of the dumped data.
func restoreErrors(output string, c pgCreds) []string {
	tolerated := []string{
		`role "` + c.User + `" already exists`,
		`database "` + c.DB + `" already exists`,
	}
	var out []string
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.Contains(line, "ERROR:") {
			continue
		}
		if slices.ContainsFunc(tolerated, func(t string) bool { return strings.Contains(line, t) }) {
			continue
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}
