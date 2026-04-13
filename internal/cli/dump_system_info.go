// dump-system-info command implementation.
//
// Collects diagnostics (namespace state, daemon logs, container logs, system
// info) into a single ZIP archive in the current working directory. Designed
// as a one-shot replacement for the manual checklist in troubleshooting.rst
// — a user running into problems can run this command and attach the
// resulting archive to a support ticket.

package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/cli/setup"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// Log-trim thresholds (lines) applied per container unless --full is set.
// Head+tail lets support engineers see the boot preamble (head) and the
// final failure cause (tail) in the common case without blowing up the
// archive size.
const (
	defaultLogHeadLines = 1000
	defaultLogTailLines = 2000
	// sizeWarnBytes triggers a stderr warning when the archive grows
	// unexpectedly large. Not a hard cap — support engineers may genuinely
	// need the full logs with --full on a 24-app enterprise install.
	sizeWarnBytes int64 = 50 * 1024 * 1024
	// daemonLogTailLines and journalTailLines keep system logs bounded
	// even without --full; these sources are inherently append-only and
	// rarely need the full history for a support ticket.
	daemonLogTailLines  = 5000
	journalTailLines    = 5000
	systemCmdTimeout    = 15 * time.Second
	dockerCmdTimeout    = 30 * time.Second
	containerLogTimeout = 60 * time.Second
)

// newDumpSystemInfoCmd registers `citeck dump-system-info`.
func newDumpSystemInfoCmd(info BuildInfo) *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "dump-system-info",
		Short: "Collect diagnostics into a ZIP archive in current directory",
		Long: "Produces ./citeck-dump-<timestamp>.zip with namespace state, " +
			"daemon/container logs, and host information. Intended for support " +
			"engineers — replaces the manual command checklist in " +
			"troubleshooting.rst.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDumpSystemInfo(cmd.Context(), info, full)
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "Include full container logs (no head/tail trimming)")
	return cmd
}

// dumpProgress prints `[i/n] msg` to stderr so stdout stays reserved for the
// final archive path (useful for scripting). Stderr is the natural home for
// progress updates — fits with Unix conventions and doesn't break
// `archive=$(citeck dump-system-info)`.
type dumpProgress struct {
	total int
	idx   int
}

func (p *dumpProgress) step(msg string) {
	p.idx++
	// Stderr output, intentional — progress channel kept separate from the
	// command's primary artifact (archive path on stdout).
	fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", p.idx, p.total, msg)
}

// stepError is recorded per-failed-step so the whole command never aborts
// on a missing container or a flaky `systemctl` — support engineers care
// about the partial picture more than a clean exit code.
type stepError struct {
	Step string `json:"step"`
	Err  string `json:"error"`
}

// dumpWriter wraps zip.Writer with a running byte counter (for the size
// warning) and an error log that ends up in info.txt.
type dumpWriter struct {
	zw     *zip.Writer
	errs   []stepError
	bytes  int64
	cancel bool
}

func newDumpWriter(w io.Writer) *dumpWriter {
	return &dumpWriter{zw: zip.NewWriter(w)}
}

// addFile writes bytes as a single file entry in the archive.
func (d *dumpWriter) addFile(name string, data []byte) {
	if d.cancel {
		return
	}
	f, err := d.zw.Create(name)
	if err != nil {
		d.recordErr(name, err)
		return
	}
	n, err := f.Write(data)
	if err != nil {
		d.recordErr(name, err)
		return
	}
	d.bytes += int64(n)
}

// addText is a convenience over addFile for string content.
func (d *dumpWriter) addText(name, content string) {
	d.addFile(name, []byte(content))
}

// addErr writes an accompanying `<entry>.err` file that records why the
// requested step failed. Kept as a sibling entry (not a single errors.json
// at the root) so support engineers can see the failure next to its
// intended output.
func (d *dumpWriter) addErr(entry string, err error) {
	if err == nil {
		return
	}
	d.addText(entry+".err", err.Error()+"\n")
	d.recordErr(entry, err)
}

func (d *dumpWriter) recordErr(step string, err error) {
	d.errs = append(d.errs, stepError{Step: step, Err: err.Error()})
}

// Close finalizes the archive. Safe to call multiple times — only the first
// call has an effect.
func (d *dumpWriter) Close() error {
	if d.cancel {
		return nil
	}
	d.cancel = true
	if err := d.zw.Close(); err != nil {
		return fmt.Errorf("close zip: %w", err)
	}
	return nil
}

// runDumpSystemInfo is the command body — orchestrates all collection steps
// inside a single zip.Writer. Errors from individual steps never abort the
// whole command (they're captured via addErr instead).
func runDumpSystemInfo(ctx context.Context, info BuildInfo, full bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	ts := time.Now().Format("20060102-150405")
	archiveName := fmt.Sprintf("citeck-dump-%s.zip", ts)
	archivePath := filepath.Join(cwd, archiveName)

	out, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // archive goes into user cwd by design
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()

	dw := newDumpWriter(out)

	prog := &dumpProgress{total: 20}

	// Section 1: basic info.txt + citeck namespace state
	prog.step("Collecting info.txt")
	dw.addText("info.txt", buildInfoTxt(info))

	prog.step("Collecting citeck/version.txt")
	dw.addText("citeck/version.txt", buildVersionJSON(info))

	prog.step("Collecting citeck/status.json")
	collectStatusJSON(dw, "citeck/status.json")

	prog.step("Collecting citeck/health.txt")
	collectHealth(dw, "citeck/health.txt")

	prog.step("Collecting citeck/diagnose.txt")
	collectDiagnose(dw, "citeck/diagnose.txt")

	prog.step("Collecting citeck/config-view.yaml")
	collectConfigView(dw, "citeck/config-view.yaml")

	prog.step("Collecting citeck/setup-history.txt")
	collectSetupHistory(dw, "citeck/setup-history.txt")

	// Section 2: daemon files
	prog.step("Collecting daemon/daemon.log")
	collectTailFile(dw, "daemon/daemon.log", config.DaemonLogPath(), daemonLogTailLines, full)

	prog.step("Collecting daemon/daemon.yml")
	collectFile(dw, "daemon/daemon.yml", config.DaemonConfigPath())

	prog.step("Collecting daemon/namespace.yml")
	collectFile(dw, "daemon/namespace.yml", config.NamespaceConfigPath())

	// Section 3: system commands
	prog.step("Collecting system/uname.txt")
	collectCmd(ctx, dw, "system/uname.txt", systemCmdTimeout, "uname", "-a")

	prog.step("Collecting system/os-release.txt")
	collectFile(dw, "system/os-release.txt", "/etc/os-release")

	prog.step("Collecting system/free.txt")
	collectCmd(ctx, dw, "system/free.txt", systemCmdTimeout, "free", "-h")

	prog.step("Collecting system/df.txt")
	collectCmd(ctx, dw, "system/df.txt", systemCmdTimeout, "df", "-h")

	prog.step("Collecting system/systemctl-citeck.txt")
	collectCmd(ctx, dw, "system/systemctl-citeck.txt", systemCmdTimeout, "systemctl", "status", "citeck", "--no-pager")

	prog.step("Collecting system/journalctl-citeck.txt")
	collectJournal(ctx, dw, "system/journalctl-citeck.txt", full)

	prog.step("Collecting system/docker-version.txt")
	collectCmd(ctx, dw, "system/docker-version.txt", systemCmdTimeout, "docker", "version")

	// Section 4: docker state + container inspects
	prog.step("Collecting docker/ps,networks,volumes")
	collectDockerList(ctx, dw)

	prog.step("Collecting docker/inspect/*")
	containers := collectDockerInspects(ctx, dw)

	// Section 5: per-container logs (single step at the end; slow)
	prog.step(fmt.Sprintf("Collecting logs/ for %d container(s)", len(containers)))
	collectContainerLogs(ctx, dw, containers, full)

	// Flush error summary into info.txt-adjacent file BEFORE closing the
	// zip — otherwise the archive would be missing its own error log.
	if len(dw.errs) > 0 {
		buf, _ := json.MarshalIndent(dw.errs, "", "  ")
		dw.addFile("errors.json", buf)
	}

	if err := dw.Close(); err != nil {
		return err
	}

	// Post-close: stat the archive to report size and warn on huge dumps.
	// Using Lstat here would still work for a regular file but Stat matches
	// intent (we wrote a real file, not a symlink).
	stat, _ := os.Stat(archivePath)
	var sizeMB float64
	if stat != nil {
		sizeMB = float64(stat.Size()) / (1024 * 1024)
		if stat.Size() > sizeWarnBytes {
			fmt.Fprintf(os.Stderr,
				"Warning: archive is %.1f MB (> %d MB). Consider removing old logs or running without --full.\n",
				sizeMB, sizeWarnBytes/(1024*1024))
		}
	}

	// Archive path goes to stdout so `archive=$(citeck dump-system-info)`
	// works in shell scripts even when progress is on stderr.
	output.PrintText("%s", archivePath)
	if stat != nil {
		fmt.Fprintf(os.Stderr, "Archive size: %.2f MB (%d bytes)\n", sizeMB, stat.Size())
	}
	return nil
}

// buildInfoTxt produces the top-level info.txt — what the user ran plus
// the minimum host fingerprint needed to reproduce the environment.
func buildInfoTxt(info BuildInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Citeck dump-system-info\n")
	fmt.Fprintf(&b, "Generated: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Version:   %s\n", info.Version)
	if info.Commit != "" {
		fmt.Fprintf(&b, "Commit:    %s\n", info.Commit)
	}
	if info.BuildDate != "" {
		fmt.Fprintf(&b, "BuildDate: %s\n", info.BuildDate)
	}
	fmt.Fprintf(&b, "OS/Arch:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "Go:        %s\n", runtime.Version())
	host, _ := os.Hostname()
	fmt.Fprintf(&b, "Hostname:  %s\n", host)
	fmt.Fprintf(&b, "HomeDir:   %s\n", config.HomeDir())
	fmt.Fprintf(&b, "Desktop:   %v\n", config.IsDesktopMode())
	return b.String()
}

// buildVersionJSON mirrors what `citeck version --format json` emits so the
// archive is diffable across invocations without calling the command.
func buildVersionJSON(info BuildInfo) string {
	m := map[string]string{
		"version": info.Version,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"go":      runtime.Version(),
	}
	if info.Commit != "" {
		m["commit"] = info.Commit
	}
	if info.BuildDate != "" {
		m["buildDate"] = info.BuildDate
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b) + "\n"
}

// collectStatusJSON pulls the namespace state via the daemon's HTTP API
// (Unix socket). A missing daemon is recorded — not an error — because
// "daemon is down" is itself useful information for support.
func collectStatusJSON(dw *dumpWriter, name string) {
	c := client.TryNew(clientOpts())
	if c == nil {
		dw.addErr(name, fmt.Errorf("daemon not reachable (no client)"))
		return
	}
	defer c.Close()
	if !c.IsRunning() {
		dw.addText(name, `{"running": false}`+"\n")
		return
	}
	ns, err := c.GetNamespace()
	if err != nil {
		dw.addErr(name, fmt.Errorf("get namespace: %w", err))
		return
	}
	b, err := json.MarshalIndent(ns, "", "  ")
	if err != nil {
		dw.addErr(name, fmt.Errorf("marshal namespace: %w", err))
		return
	}
	dw.addFile(name, append(b, '\n'))
}

// collectHealth renders the same text output as `citeck health`, including
// the banner that encodes the exit code. Also records the would-be exit
// code in the first line so support engineers can reconstruct it.
func collectHealth(dw *dumpWriter, name string) {
	c, err := client.New(clientOpts())
	if err != nil {
		dw.addErr(name, fmt.Errorf("connect to daemon: %w", err))
		return
	}
	defer c.Close()

	health, err := c.GetHealth()
	if err != nil {
		dw.addErr(name, fmt.Errorf("get health: %w", err))
		return
	}
	exitCode := ExitOK
	if !health.Healthy {
		exitCode = ExitUnhealthy
	}
	// Render without colors/JSON into a buffer so the archive gets stable,
	// plain text — not interleaved with whatever stdout format happens to
	// be active for the parent command.
	buf := renderHealthToString(health, exitCode)
	dw.addText(name, buf)
}

// renderHealthToString produces the same layout as renderHealth but into a
// string (no global stdout writes, no ANSI). Kept separate so unit tests can
// exercise it without rewiring os.Stdout.
func renderHealthToString(health *api.HealthDto, exitCode int) string {
	label, _ := healthBanner(exitCode)
	var b strings.Builder
	fmt.Fprintf(&b, "Status: %s  (exit=%d)\n\n", label, exitCode)
	if health == nil {
		return b.String()
	}
	for _, check := range health.Checks {
		icon := plainCheckIcon(check.Status)
		msg := check.Name
		if check.Message != "" {
			msg += " — " + check.Message
		}
		fmt.Fprintf(&b, "  %s  %s\n", icon, msg)
	}
	return b.String()
}

// plainCheckIcon is the ANSI-free counterpart to formatCheckIcon, used by
// the archive renderer.
func plainCheckIcon(status string) string {
	switch status {
	case "ok":
		return "[OK]   "
	case "warning":
		return "[WARN] "
	case "error":
		return "[ERROR]"
	default:
		return "[?]    "
	}
}

// collectDiagnose walks the same probe list as `citeck diagnose` (read-only,
// no --fix) and writes the text summary to the archive.
func collectDiagnose(dw *dumpWriter, name string) {
	checks := runDiagnoseChecks(false, false)
	var b strings.Builder
	for _, c := range checks {
		b.WriteString("  ")
		b.WriteString(plainCheckIcon(c.Status))
		b.WriteString("  ")
		b.WriteString(c.Message)
		if c.FixHint != "" {
			b.WriteString("\n       → ")
			b.WriteString(c.FixHint)
		}
		b.WriteString("\n")
	}
	errCount, warnCount := countSeverities(checks)
	fmt.Fprintf(&b, "\nSummary: %d error(s), %d warning(s)\n", errCount, warnCount)
	dw.addText(name, b.String())
}

// collectConfigView mirrors `citeck config view` — the raw contents of
// namespace.yml. Secret references stay as `secret:*` refs; we never expand
// them.
func collectConfigView(dw *dumpWriter, name string) {
	path := config.NamespaceConfigPath()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from internal config
	if err != nil {
		dw.addErr(name, fmt.Errorf("read %s: %w", path, err))
		return
	}
	dw.addFile(name, data)
}

// collectSetupHistory invokes the exported setup.HistoryText helper so we
// don't shell out to `citeck setup history` — the binary would re-enter
// itself and re-init i18n, which is both slow and fragile.
func collectSetupHistory(dw *dumpWriter, name string) {
	var buf bytes.Buffer
	if err := setup.HistoryText(&buf); err != nil {
		dw.addErr(name, fmt.Errorf("history: %w", err))
		return
	}
	dw.addFile(name, buf.Bytes())
}

// collectFile copies a file from disk into the archive verbatim.
func collectFile(dw *dumpWriter, entry, path string) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a compile-time constant or internal config path
	if err != nil {
		dw.addErr(entry, fmt.Errorf("read %s: %w", path, err))
		return
	}
	dw.addFile(entry, data)
}

// collectTailFile reads `path` and keeps only the trailing `tailLines` lines
// unless `full` is set. Useful for daemon.log which can grow to hundreds of
// MB on busy servers.
func collectTailFile(dw *dumpWriter, entry, path string, tailLines int, full bool) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: internal config path
	if err != nil {
		dw.addErr(entry, fmt.Errorf("read %s: %w", path, err))
		return
	}
	if full {
		dw.addFile(entry, data)
		return
	}
	dw.addText(entry, trimTail(string(data), tailLines))
}

// collectCmd runs a host command with timeout and captures stdout+stderr as
// the archive entry. A non-zero exit status is recorded in the `.err` file
// but doesn't abort the dump — a missing `systemctl` on a desktop machine
// is expected, not fatal.
func collectCmd(ctx context.Context, dw *dumpWriter, entry string, timeout time.Duration, name string, args ...string) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...) //nolint:gosec // G204: args are compile-time constants
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var combined bytes.Buffer
	combined.Write(stdout.Bytes())
	if stderr.Len() > 0 {
		if combined.Len() > 0 && !strings.HasSuffix(combined.String(), "\n") {
			combined.WriteByte('\n')
		}
		combined.WriteString("--- stderr ---\n")
		combined.Write(stderr.Bytes())
	}
	if err != nil {
		// Still write the partial output so support can see whatever the
		// command managed to emit before failing.
		combined.WriteString("\n--- error ---\n")
		combined.WriteString(err.Error())
		combined.WriteByte('\n')
		dw.addFile(entry, combined.Bytes())
		dw.recordErr(entry, err)
		return
	}
	dw.addFile(entry, combined.Bytes())
}

// collectJournal runs `journalctl -u citeck --since '7 days ago'` and trims
// to `journalTailLines` unless `full` is set. Journal absence on non-systemd
// hosts is an expected failure, not an error.
func collectJournal(ctx context.Context, dw *dumpWriter, entry string, full bool) {
	cctx, cancel := context.WithTimeout(ctx, systemCmdTimeout)
	defer cancel()
	args := []string{"-u", "citeck", "--since", "7 days ago", "--no-pager"}
	cmd := exec.CommandContext(cctx, "journalctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		dw.addFile(entry, out)
		dw.recordErr(entry, err)
		return
	}
	if full {
		dw.addFile(entry, out)
		return
	}
	dw.addText(entry, trimTail(string(out), journalTailLines))
}

// collectDockerList populates docker/ps.txt, networks.txt, volumes.txt.
// Each shells out individually so a single-command failure (rare) doesn't
// drop the other two.
func collectDockerList(ctx context.Context, dw *dumpWriter) {
	collectCmd(ctx, dw, "docker/ps.txt", dockerCmdTimeout, "docker", "ps", "-a")
	collectCmd(ctx, dw, "docker/networks.txt", dockerCmdTimeout, "docker", "network", "ls")
	collectCmd(ctx, dw, "docker/volumes.txt", dockerCmdTimeout, "docker", "volume", "ls")
}

// collectDockerInspects writes `docker inspect` output for every launcher
// container (label citeck.launcher=true) into docker/inspect/<name>.json
// and returns the list of container names for the subsequent log step.
func collectDockerInspects(ctx context.Context, dw *dumpWriter) []string {
	names := listLauncherContainerNames(ctx, dw)
	for _, name := range names {
		entry := "docker/inspect/" + name + ".json"
		collectCmd(ctx, dw, entry, dockerCmdTimeout, "docker", "inspect", name)
	}
	return names
}

// listLauncherContainerNames uses the Docker SDK directly (not `docker ps`)
// because we need label filtering and the SDK is already initialized
// elsewhere — avoids a JSON parse of the CLI output.
func listLauncherContainerNames(ctx context.Context, dw *dumpWriter) []string {
	// namespace="" is fine here — ListAllLauncherContainers filters by
	// the launcher-wide label, not by per-namespace labels.
	cli, err := docker.NewClient("", "dump")
	if err != nil {
		dw.recordErr("docker/inspect", fmt.Errorf("docker client: %w", err))
		return nil
	}
	defer cli.Close()

	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()
	list, err := cli.ListAllLauncherContainers(cctx)
	if err != nil {
		dw.recordErr("docker/inspect", fmt.Errorf("list containers: %w", err))
		return nil
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		n := strings.TrimPrefix(c.Names[0], "/")
		if n != "" {
			names = append(names, n)
		}
	}
	return names
}

// collectContainerLogs writes one file per container. When !full, we trim
// to defaultLogHeadLines + defaultLogTailLines (and insert a "[skipped N
// lines]" marker) so a 20 GB log doesn't turn into a 2 GB archive.
func collectContainerLogs(ctx context.Context, dw *dumpWriter, names []string, full bool) {
	for _, name := range names {
		entry := "logs/" + name + ".log"
		collectOneContainerLog(ctx, dw, entry, name, full)
	}
}

// collectOneContainerLog shells out to `docker logs` rather than using the
// SDK's stdcopy path because we want the raw, human-friendly output
// (timestamps + prefixes) that the CLI produces — it's what a support
// engineer sees when they run the command manually.
func collectOneContainerLog(ctx context.Context, dw *dumpWriter, entry, container string, full bool) {
	cctx, cancel := context.WithTimeout(ctx, containerLogTimeout)
	defer cancel()
	// --details for log driver metadata; --timestamps to correlate across
	// containers. Intentionally no `--tail` — we do the trim ourselves so
	// head+tail (not just tail) is available.
	cmd := exec.CommandContext(cctx, "docker", "logs", "--details", "--timestamps", container) //nolint:gosec // G204: container name comes from Docker SDK ListAllLauncherContainers, not user input
	out, err := cmd.CombinedOutput()
	if err != nil {
		dw.addFile(entry, out)
		dw.recordErr(entry, err)
		return
	}
	if full {
		dw.addFile(entry, out)
		return
	}
	dw.addText(entry, trimHeadTail(string(out), defaultLogHeadLines, defaultLogTailLines))
}

// trimTail keeps the last `n` lines of s, returning s unchanged if it has
// fewer lines.
func trimTail(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	// Split keeps a trailing empty element when s ends with "\n" — leave it
	// so round-tripping doesn't silently add/remove a newline.
	if len(lines) <= n {
		return s
	}
	kept := lines[len(lines)-n:]
	return fmt.Sprintf("[trimmed %d earlier lines]\n%s", len(lines)-n, strings.Join(kept, "\n"))
}

// trimHeadTail keeps the first `head` and last `tail` lines with a marker
// noting how many lines were skipped in between. Used for container logs
// where the interesting bits are usually at startup (head) and crash (tail).
func trimHeadTail(s string, head, tail int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= head+tail {
		return s
	}
	// Preserve the trailing empty line from Split so round-tripping is
	// stable.
	var b strings.Builder
	b.WriteString(strings.Join(lines[:head], "\n"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "... [skipped %d lines — rerun with --full for complete logs] ...\n", len(lines)-head-tail)
	b.WriteString(strings.Join(lines[len(lines)-tail:], "\n"))
	return b.String()
}
