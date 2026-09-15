package migrate

import "strings"

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
func RestoreCommandPrefix() []string {
	return []string{"psql", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-q", "-o", "/dev/null"}
}

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
func DumpScript(outPath string) []string {
	return []string{"bash", "-c", "set -o pipefail; " +
		"pg_dumpall -h 127.0.0.1 -U postgres | gzip -" + gzipLevel + " > " + shellQuote(outPath)}
}

// RestoreScript is the command the restore step runs: the archive decompressed
// into the SAME psql invocation RestoreCommandPrefix names — so the flags have
// one source and the integration test can still identify the restore's own
// stderr by that prefix, now as a substring of this script.
func RestoreScript(dumpPath string) []string {
	return []string{"bash", "-c", "set -o pipefail; " +
		"gunzip -c " + shellQuote(dumpPath) + " | " + strings.Join(RestoreCommandPrefix(), " ")}
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
// `CREATE ROLE postgres` even for the bootstrap superuser and the PostgreSQL
// documentation calls that failure harmless ("it won't hurt for the CREATE to
// fail"), so that one error is tolerated and every other one fails the
// restore. Stopping at the first error instead would abort a restore that is
// in fact perfect, on the very first statement of every dump.
//
// The match is case-sensitive: psql spells its severities upper-case, while a
// lower-case "error:" is text being replayed out of the dumped data.
func restoreErrors(output string) []string {
	var out []string
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.Contains(line, "ERROR:") {
			continue
		}
		if strings.Contains(line, `role "postgres" already exists`) {
			continue
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}
