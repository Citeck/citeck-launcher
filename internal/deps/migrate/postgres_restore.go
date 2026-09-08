package migrate

import "strings"

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
