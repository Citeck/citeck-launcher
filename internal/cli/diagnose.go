package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/output"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/spf13/cobra"
)

type diagnoseCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok, warning, error
	Message string `json:"message"`
	Fixable bool   `json:"fixable"`
	// FixHint is an optional human-readable pointer shown in text mode
	// when --fix is requested but the launcher cannot safely auto-fix
	// the problem (e.g., port conflict with a foreign process).
	FixHint string `json:"fixHint,omitempty"`
}

// troubleshootingRef is the canonical pointer we print for checks that
// cannot be auto-fixed. Kept as a constant so tests can assert against it.
const troubleshootingRef = "see docs: troubleshooting.rst \"Конфликт портов\""

// failedAppStatuses enumerates app statuses that indicate a failure the
// operator needs to act on. Kept in one place so diagnose and tests stay
// in sync with the runtime status machine.
var failedAppStatuses = map[string]struct{}{
	api.AppStatusFailed:         {},
	api.AppStatusStartFailed:    {},
	api.AppStatusPullFailed:     {},
	api.AppStatusStoppingFailed: {},
}

func newDiagnoseCmd() *cobra.Command {
	var fix bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Find and optionally fix problems",
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := runDiagnoseChecks(fix, dryRun)
			return renderDiagnose(checks, fix)
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Auto-fix fixable problems")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview fixes without applying")

	return cmd
}

// runDiagnoseChecks executes every probe in order and returns the check list.
// Each probe is factored into its own helper so gocyclo/nestif stay happy
// and individual probes remain covered by unit tests.
func runDiagnoseChecks(fix, dryRun bool) []diagnoseCheck {
	// B7-01: any app in a failed state becomes an ERROR so `diagnose` can
	// never print "No problems found" during a real outage. Collected first
	// so port-443 checks can be elevated when they co-occur with a proxy
	// failure.
	failedApps := collectFailedApps()
	proxyFailed := hasProxyFailure(failedApps)

	dirs := directoryChecks(fix, dryRun)
	checks := make([]diagnoseCheck, 0, 5+len(dirs)+len(failedApps)+2)
	checks = append(checks, socketCheck(fix, dryRun), configCheck(), dockerCheck())
	checks = append(checks, dirs...)
	for _, app := range failedApps {
		checks = append(checks, failedAppCheck(app))
	}
	launcherOwners := findLauncherPortOwners([]int{80, 443})
	checks = append(checks,
		portCheck(80, "HTTP", proxyFailed, launcherOwners[80]),
		portCheck(443, "HTTPS", proxyFailed, launcherOwners[443]),
	)
	return checks
}

// renderDiagnose prints checks to stdout (text/JSON) and returns an
// ExitCodeError when any problem was reported.
func renderDiagnose(checks []diagnoseCheck, fix bool) error {
	result := map[string]any{"checks": checks}
	errCount, warnCount := countSeverities(checks)

	output.PrintResult(result, func() {
		for _, c := range checks {
			msg := c.Message
			if c.Fixable && !fix {
				msg += " (fixable with --fix)"
			}
			output.PrintText("  %s  %s", formatCheckIcon(c.Status), msg)
			// B7-02: surface the troubleshooting pointer for any check
			// that can't be auto-fixed — shown inline so the user doesn't
			// have to re-run with --fix to learn about the manual path.
			if c.FixHint != "" && (c.Status == "error" || c.Status == "warning") {
				output.PrintText("       %s", output.Colorize(output.Dim, "→ "+c.FixHint))
			}
		}
		fmt.Println()
		printDiagnoseSummary(checks, errCount, warnCount)
	})

	if errCount > 0 {
		return exitWithCode(ExitError, "%d problem(s) found", errCount)
	}
	return nil
}

func countSeverities(checks []diagnoseCheck) (errCount, warnCount int) {
	for _, c := range checks {
		switch c.Status {
		case "error":
			errCount++
		case "warning":
			warnCount++
		}
	}
	return errCount, warnCount
}

func printDiagnoseSummary(checks []diagnoseCheck, errCount, warnCount int) {
	switch {
	case errCount > 0:
		output.PrintText(output.Colorize(output.Red, fmt.Sprintf("%d problem(s) found", errCount)))
		if hasHint(checks) {
			output.PrintText(output.Colorize(output.Dim, "For manual remediation steps "+troubleshootingRef))
		}
	case warnCount > 0:
		output.PrintText(output.Colorize(output.Yellow, fmt.Sprintf("%d warning(s)", warnCount)))
	default:
		output.PrintText(output.Colorize(output.Green, "No problems found"))
	}
}

// socketCheck probes the Unix socket. Not-responding yields an ERROR
// (fixable by removing); absent socket is a WARN (daemon not running).
func socketCheck(fix, dryRun bool) diagnoseCheck {
	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		return diagnoseCheck{
			Name: "socket", Status: "warning",
			Message: "No daemon socket found (daemon not running)",
		}
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err == nil {
		_ = conn.Close()
		return diagnoseCheck{
			Name: "socket", Status: "ok", Message: "Daemon socket is responsive",
		}
	}
	c := diagnoseCheck{
		Name:    "stale_socket",
		Status:  "error",
		Message: fmt.Sprintf("Stale socket file at %s (daemon not responding)", socketPath),
		Fixable: true,
	}
	if fix && !dryRun {
		_ = os.Remove(socketPath)
		c.Message += " — FIXED (removed)"
		c.Status = "ok"
	}
	return c
}

func configCheck() diagnoseCheck {
	cfgPath := config.NamespaceConfigPath()
	if _, err := os.Stat(cfgPath); err != nil {
		return diagnoseCheck{
			Name: "config", Status: "warning",
			Message: fmt.Sprintf("No config file at %s", cfgPath),
		}
	}
	return diagnoseCheck{Name: "config", Status: "ok", Message: "Config file exists"}
}

func dockerCheck() diagnoseCheck {
	dockerClient, dockerErr := docker.NewClient("", "diagnose")
	if dockerErr != nil {
		return diagnoseCheck{
			Name: "docker", Status: "error",
			Message: "Docker client error: " + dockerErr.Error(),
		}
	}
	defer dockerClient.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dockerClient.Ping(ctx); err != nil {
		return diagnoseCheck{
			Name: "docker", Status: "error",
			Message: "Docker is not reachable: " + err.Error(),
		}
	}
	return diagnoseCheck{Name: "docker", Status: "ok", Message: "Docker is reachable"}
}

func directoryChecks(fix, dryRun bool) []diagnoseCheck {
	dirs := []string{config.ConfDir(), config.DataDir(), config.LogDir()}
	var out []diagnoseCheck
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		c := diagnoseCheck{
			Name:    "directory",
			Status:  "error",
			Message: fmt.Sprintf("Missing directory: %s", dir),
			Fixable: true,
		}
		if fix && !dryRun {
			_ = os.MkdirAll(dir, 0o755) //nolint:gosec // G301: data dir needs 0o755
			c.Message += " — FIXED (created)"
			c.Status = "ok"
		}
		out = append(out, c)
	}
	return out
}

// portCheck opens a listener on port to detect a conflict. Returns a status
// record in either case. launcherOwner is the name of the citeck container
// holding the port (if any), used to avoid WARNing on our own proxy.
func portCheck(port int, name string, proxyFailed bool, launcherOwner string) diagnoseCheck {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return portConflictCheck(port, name, proxyFailed, launcherOwner)
	}
	_ = ln.Close()
	return diagnoseCheck{
		Name: "port", Status: "ok",
		Message: fmt.Sprintf("Port %d (%s) is available", port, name),
	}
}

// collectFailedApps queries the daemon (if reachable) and returns the list
// of apps in a failed state. A missing or unresponsive daemon yields an
// empty slice — socketCheck surfaces that separately.
func collectFailedApps() []api.AppDto {
	c := client.TryNew(clientOpts())
	if c == nil {
		return nil
	}
	defer c.Close()
	if !c.IsRunning() {
		return nil
	}
	ns, err := c.GetNamespace()
	if err != nil {
		return nil
	}
	var failed []api.AppDto
	for _, app := range ns.Apps {
		if isFailedAppStatus(app.Status) {
			failed = append(failed, app)
		}
	}
	return failed
}

// hasHint reports whether any check carries a manual fix pointer.
func hasHint(checks []diagnoseCheck) bool {
	for _, c := range checks {
		if c.FixHint != "" && strings.TrimSpace(c.FixHint) != "" {
			return true
		}
	}
	return false
}

// isFailedAppStatus reports whether an app status represents an actionable
// failure surfaced by `diagnose`.
func isFailedAppStatus(status string) bool {
	_, ok := failedAppStatuses[status]
	return ok
}

// hasProxyFailure reports whether the proxy app is in any of the failed
// statuses we track. Used to decide whether a port-443 conflict is an
// outage (ERROR) or just informational (WARN).
func hasProxyFailure(apps []api.AppDto) bool {
	for _, app := range apps {
		if app.Name == "proxy" && isFailedAppStatus(app.Status) {
			return true
		}
	}
	return false
}

// failedAppCheck returns the diagnose entry for a failed app.
func failedAppCheck(app api.AppDto) diagnoseCheck {
	return diagnoseCheck{
		Name:    "app:" + app.Name,
		Status:  "error",
		Message: fmt.Sprintf("App %q is in %s state", app.Name, app.Status),
		FixHint: troubleshootingRef,
	}
}

// portConflictCheck builds the diagnose entry for a port listen failure.
// Severity is ERROR when proxy is failing and the conflict is on 443
// (proxy's primary port) — otherwise WARN with a hint. See B7-01/B7-02.
// When launcherOwner is non-empty, the port is held by one of our own
// containers — that's not a conflict, so it's reported as OK.
func portConflictCheck(port int, name string, proxyFailed bool, launcherOwner string) diagnoseCheck {
	// Port held by our own proxy container is the expected state when the
	// namespace is running — don't report it as a conflict. A residual
	// container left behind after the daemon died is still surfaced by the
	// socket/docker checks; this path just makes sure `diagnose` doesn't
	// spook users when everything is fine.
	if launcherOwner != "" {
		return diagnoseCheck{
			Name:   "port",
			Status: "ok",
			Message: fmt.Sprintf("Port %d (%s) is held by %s",
				port, name, launcherOwner),
		}
	}

	status := "warning"
	hint := ""
	if port == 443 && proxyFailed {
		status = "error"
		hint = troubleshootingRef
	} else if port == 443 || port == 80 {
		hint = troubleshootingRef
	}
	return diagnoseCheck{
		Name:    "port",
		Status:  status,
		Message: fmt.Sprintf("Port %d (%s) is in use", port, name),
		FixHint: hint,
	}
}

// findLauncherPortOwners asks Docker which citeck containers publish any of
// the given ports. Returns a map {port -> container name} — missing entries
// mean the port is not held by us (either free or held by a foreign process).
// A Docker failure yields an empty map: the caller falls back to the
// foreign-conflict branch, which is the safe default.
func findLauncherPortOwners(ports []int) map[int]string {
	owners := make(map[int]string, len(ports))
	dc, err := docker.NewClient("", "diagnose")
	if err != nil {
		return owners
	}
	defer dc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	containers, err := dc.ListAllLauncherContainers(ctx)
	if err != nil {
		return owners
	}
	return matchPortOwners(containers, ports)
}

// matchPortOwners scans a container list and picks the first citeck
// container that publishes each requested port. Extracted so tests can
// feed canned container summaries without touching Docker.
func matchPortOwners(containers []dockercontainer.Summary, ports []int) map[int]string {
	owners := make(map[int]string, len(ports))
	for _, c := range containers {
		name := containerDisplayName(c)
		for _, p := range c.Ports {
			// PublicPort is the host-side port (0 when not published).
			if p.PublicPort == 0 {
				continue
			}
			for _, want := range ports {
				if int(p.PublicPort) == want {
					if _, already := owners[want]; !already {
						owners[want] = name
					}
				}
			}
		}
	}
	return owners
}

// containerDisplayName returns a stable, human-readable name for a
// container summary. Docker reports names with a leading slash.
func containerDisplayName(c dockercontainer.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	if c.ID != "" && len(c.ID) >= 12 {
		return c.ID[:12]
	}
	return "citeck container"
}
