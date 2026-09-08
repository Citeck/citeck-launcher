package migrate

import "strings"

// RestoreCommandPrefix is the psql invocation the restore step runs, minus its
// "-f <dump>" tail. It is EXPORTED because the real-Docker integration test
// (internal/daemon/deps_integration_test.go) has to pick the restore's own
// stderr out of every command the migration ran, and it identifies it by this
// prefix. A hand-copied literal there would stop matching the day a flag is
// added or reordered — and, since a missed match means "no stderr recorded",
// it would silently delete the assertion that the tolerated
// `role "postgres" already exists` error is what a real dump produces. One
// source, used by the step and by the test.
//
// A fresh slice per call: the caller appends its own tail to it.
func RestoreCommandPrefix() []string {
	return []string{"psql", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-q", "-o", "/dev/null"}
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
