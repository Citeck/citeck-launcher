package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// A dependency migration is the one operation whose failure an operator cannot
// investigate afterwards: it builds temp containers, and the rollback removes
// them — with their logs — seconds later. What is left in daemon.log is a
// timeout, which reads the same for a slow disk, a bad image and a database
// that refused the role it was addressed as (a real case: see
// tempContainerDiagnostics).
//
// So every attempt writes ONE file under config.ReportsDir(): what was
// attempted, every step it reached, the verdict, and — on a failure — the
// evidence the plan collected before the rollback. The system dump ships the
// whole directory, so "it would not update" arrives with its own explanation
// attached instead of a request for a screenshot.
const (
	// reportsKept bounds the directory: reports are small (a few KiB plus at
	// most two container log tails) and only a migration writes one, so this is
	// many months of history on any real stand.
	reportsKept = 50
	// reportFilePerm keeps a report readable only by the launcher's own user:
	// container logs can carry connection strings and hostnames.
	reportFilePerm = 0o600
	reportDirPerm  = 0o700
)

// reportStep is one step's outcome, in the order the engine ran them.
type reportStep struct {
	ID     string
	Index  int
	Failed bool
}

// migrationReport is one attempt's whole story.
type migrationReport struct {
	Kind        string // "upgrade" or "rollback"
	Namespace   string
	Dependency  string
	From        string
	To          string
	Launcher    string
	StartedAt   time.Time
	FinishedAt  time.Time
	Steps       []reportStep
	StepCount   int
	Outcome     string
	Err         string
	Preflight   []string
	Diagnostics []migrate.Diagnostic
}

// writeMigrationReport renders the report and returns the path it was written
// to. A failure to write is returned, never fatal: the migration's own verdict
// does not depend on the record of it.
func writeMigrationReport(rep migrationReport) (string, error) {
	dir := config.ReportsDir()
	if err := os.MkdirAll(dir, reportDirPerm); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	name := fmt.Sprintf("deps-%s-%s-%s.log",
		sanitizeReportPart(rep.Kind), sanitizeReportPart(rep.Dependency),
		rep.StartedAt.UTC().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if err := fsutil.AtomicWriteFile(path, []byte(rep.render()), reportFilePerm); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	pruneReports(dir, reportsKept)
	return path, nil
}

// render is deliberately plain text in ONE language: a report is read by
// whoever is being asked "why did it not update", usually pasted into a ticket,
// and a localized artifact would arrive in a language the reader does not have.
// The daemon's localized sentences are for the operator's screen; this is
// evidence.
func (r migrationReport) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Citeck launcher dependency %s report\n", r.Kind)
	fmt.Fprintf(&b, "===============================================\n")
	fmt.Fprintf(&b, "dependency : %s\n", r.Dependency)
	fmt.Fprintf(&b, "from -> to : %s -> %s\n", r.From, r.To)
	fmt.Fprintf(&b, "namespace  : %s\n", r.Namespace)
	fmt.Fprintf(&b, "launcher   : %s\n", r.Launcher)
	fmt.Fprintf(&b, "started    : %s\n", r.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "finished   : %s (%s)\n", r.FinishedAt.UTC().Format(time.RFC3339),
		r.FinishedAt.Sub(r.StartedAt).Round(time.Second))
	fmt.Fprintf(&b, "outcome    : %s\n", r.Outcome)
	if r.Err != "" {
		fmt.Fprintf(&b, "error      : %s\n", r.Err)
	}

	if len(r.Preflight) > 0 {
		b.WriteString("\npreflight\n---------\n")
		for _, line := range r.Preflight {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	b.WriteString("\nsteps\n-----\n")
	if len(r.Steps) == 0 {
		b.WriteString("  (none reached)\n")
	}
	for _, st := range r.Steps {
		status := "ok"
		if st.Failed {
			status = "FAILED"
		}
		fmt.Fprintf(&b, "  [%d/%d] %-16s %s\n", st.Index, r.StepCount, st.ID, status)
	}

	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "\n%s\n%s\n", d.Name, strings.Repeat("-", len(d.Name)))
		text := strings.TrimRight(d.Text, "\n")
		if text == "" {
			text = "(empty)"
		}
		b.WriteString(text)
		b.WriteString("\n")
	}
	return b.String()
}

// sanitizeReportPart keeps a file name a file name: ids come from the
// dependency registry, but a workspace-declared cluster names itself, and a
// name is not a path.
func sanitizeReportPart(s string) string {
	if s == "" {
		return "unknown"
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
	return out
}

// pruneReports keeps the newest keep files and removes the rest. A directory it
// cannot read is left alone: pruning is tidiness, and tidiness may not cost a
// report.
func pruneReports(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "deps-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	// The name carries a UTC timestamp, so lexical order IS chronological.
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// reportStepsFrom turns the steps the engine announced into the report's list,
// marking the last one as failed when the run ended badly. The engine reports a
// step when it STARTS, so the last announced step is the one that failed.
func reportStepsFrom(seen []reportStep, failed bool) []reportStep {
	out := slices.Clone(seen)
	if failed && len(out) > 0 {
		out[len(out)-1].Failed = true
	}
	return out
}
