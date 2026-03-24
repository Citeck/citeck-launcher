# Agent Plan V2: Citeck Launcher — Improvements & Agent Autonomy

## Context

V1 plan completed: all bug fixes, 8 CLI commands, 5 E2E configs verified. This plan focuses on reliability, automation, and making the launcher agent-friendly for autonomous operation.

---

## PRIORITY 1: Agent-Friendly CLI (Machine-Readable Output)

Agents cannot reliably parse human-readable tables and status messages. Every command must support structured output.

### 1.1 Global `--output` flag

Add a global `--output` / `-o` option to `CiteckCli` (Clikt parent command) with values: `text` (default), `json`.

**Files:**
- `cli/commands/CiteckCli.kt` — add global option
- All command classes — respect output format

**Implementation:** Create an `OutputFormat` enum and a `Formatter` abstraction. Commands call `formatter.print(data)` instead of `echo()`. Text formatter renders tables/human text; JSON formatter serializes the same data objects.

### 1.2 Structured error responses in DaemonClient

**Problem:** All client methods return `null` on any failure — can't distinguish "not found" from "server error" from "connection refused".

**Fix:** Return a sealed class `Result<T>` with `Success(data)`, `NotFound(message)`, `Error(code, message)`, `ConnectionFailed`. Client callers get actionable error information.

**Files:**
- `cli/client/DaemonClient.kt` — replace `T?` returns with `DaemonResult<T>`

### 1.3 `citeck status --json` structured output

Return complete namespace state as JSON:
```json
{
  "name": "...", "id": "...", "status": "RUNNING",
  "bundle": "community:2025.12",
  "apps": [
    {"name": "proxy", "status": "RUNNING", "image": "...", "cpu": "0.1%", "memory": "32M/128M", "uptime": 3600}
  ]
}
```

### 1.4 `citeck health --json` structured output

Already returns HealthDto — just add JSON output formatting.

### 1.5 Machine-readable exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Config error (invalid YAML, missing cert) |
| 3 | Daemon not running |
| 4 | Namespace not configured |
| 5 | App not found |
| 6 | Docker unavailable |
| 7 | Timeout |

---

## PRIORITY 2: Non-Interactive Install & Config-as-Code

### 2.1 `citeck install --non-interactive`

Accept all configuration via flags and environment variables:

```bash
citeck install \
  --auth basic --users admin \
  --host custom.launcher.ru --port 443 \
  --tls self-signed \
  --bundle community:LATEST \
  --non-interactive
```

Environment variables as fallback:
```
CITECK_AUTH_TYPE, CITECK_AUTH_USERS, CITECK_PROXY_HOST,
CITECK_PROXY_PORT, CITECK_TLS_MODE, CITECK_BUNDLE
```

**Files:** `cli/commands/InstallCmd.kt` — add Clikt options, add `--non-interactive` flag

### 2.2 `citeck install --from-config <path>`

Read a pre-built `namespace.yml` and install it directly (copy to conf dir + setup systemd). Skip the wizard entirely.

### 2.3 Idempotent install

If config already exists, compare with requested config:
- Same → skip (exit 0)
- Different → show diff, apply if `--force`

---

## PRIORITY 3: Reliability & Error Handling

### 3.1 Fix daemon startup detection

**Problem:** `StartCmd.waitForDaemon()` uses `process.isAlive` which is unreliable. Daemon process may exit after fork.

**Fix:** Poll for socket file existence + `/api/v1/daemon/status` HTTP probe. If process exits during wait, read stderr/log and report error immediately.

**File:** `cli/commands/StartCmd.kt:42-53`

### 3.2 Startup probe failure categorization

**Problem:** Probe retries 10,000 times even if the container has crashed. Wastes ~28 hours before failing.

**Fix:** After each probe failure, check if container is still running (`docker inspect`). If container exited, report the exit code and last logs immediately instead of continuing to retry.

**File:** `core/namespace/runtime/actions/AppStartAction.kt:351-385`

### 3.3 Config validation before reload

**Problem:** `NamespaceConfigManager.reload()` applies new config without validation. Bad YAML causes runtime crash.

**Fix:** Parse and validate config before applying. Return validation errors. Add `--dry-run` flag to `citeck reload`.

**File:** `cli/daemon/services/NamespaceConfigManager.kt:244-249`

### 3.4 Graceful shutdown with timeouts

**Problem:** `DaemonLifecycle.shutdown()` calls `dispose()` on services without timeout. Hung disposable blocks shutdown.

**Fix:** Wrap each `dispose()` in a timeout (e.g., 10s). If timeout expires, log warning and continue.

**File:** `cli/daemon/DaemonLifecycle.kt:105-137`

### 3.5 Install script rollback

**Problem:** `citeck-install.sh` has no rollback. If upgrade fails, system is left in broken state.

**Fix:** Backup current JAR/JRE before upgrade. If post-upgrade health check fails, restore backup.

**File:** `cli/src/dist/citeck-install.sh`

---

## PRIORITY 4: Agent Workflow Commands

New commands designed specifically for autonomous agent operation.

### 4.1 `citeck wait` — Wait for condition

```bash
citeck wait --status running --timeout 300
citeck wait --app proxy --status running --timeout 60
citeck wait --healthy --timeout 300
```

Blocks until condition is met or timeout expires. Returns JSON status on completion. Exit code 0 on success, 7 on timeout.

**Why agents need this:** Currently agents must poll `status --apps` in a loop and parse text output. `wait` makes it atomic.

### 4.2 `citeck deploy` — Atomic deploy from config

```bash
citeck deploy --config namespace.yml --wait --timeout 600
```

Combines: stop → write config → start → wait for RUNNING. Atomic operation with rollback on failure.

**Why agents need this:** Currently requires multiple commands with manual status checking between each.

### 4.3 `citeck diff` — Show config changes

```bash
citeck diff                          # running config vs namespace.yml
citeck diff --from file1.yml --to file2.yml
```

Shows what would change if config is reloaded. Returns structured diff.

### 4.4 `citeck backup` / `citeck restore`

```bash
citeck backup --output /path/to/backup.tar.gz
citeck restore --from /path/to/backup.tar.gz
```

Backup: namespace.yml + volumes + runtime state.
Restore: stop → restore → start.

### 4.5 `citeck update` — Update bundle/images

```bash
citeck update                    # update to latest bundle
citeck update --bundle community:2025.13
citeck update --app proxy --image nexus.citeck.ru/ecos-proxy-oidc:2.26.0
```

Update bundle or individual app images without full reinstall.

---

## PRIORITY 5: Observability & Diagnostics

### 5.1 `citeck diagnose`

Comprehensive diagnostic command that collects:
- System info (OS, Docker version, disk space, memory)
- Namespace status
- All container states + last 20 log lines per failed container
- Network connectivity between containers
- Port conflicts
- DNS resolution
- TLS certificate validity

Output as structured JSON or human-readable report. Designed for agent-driven troubleshooting.

### 5.2 Request logging in daemon

Log all incoming API requests to daemon with:
- Timestamp, method, path, status code, duration
- Structured format (JSON lines)
- Separate log file: `/opt/citeck/log/access.log`

### 5.3 Event history buffer

Store last 1000 events in memory. New API:
```
GET /api/v1/events/history?since=<timestamp>&limit=100
```

Agents can poll event history instead of maintaining a persistent WebSocket.

### 5.4 Startup timeline

Track and expose timing for each startup phase:
```json
{
  "pullStart": "...", "pullEnd": "...",
  "startStart": "...", "probeStart": "...", "runningAt": "...",
  "totalMs": 45000
}
```

Add to `AppInspectDto` or new endpoint.

---

## PRIORITY 6: Performance

### 6.1 Logs follow via WebSocket

**Problem:** `citeck logs --follow` polls every 2s via HTTP.

**Fix:** Add WebSocket endpoint `/api/v1/apps/{name}/logs/stream` that pushes log lines in real-time using `DockerApi.watchLogs()`.

### 6.2 Reuse DaemonClient HTTP connection

**Problem:** Each DaemonClient request creates a new Ktor HttpClient.

**Fix:** Reuse single HttpClient instance per DaemonClient (already partially done — the client is a field, but `getText` etc. recreate it).

### 6.3 Async snapshot import

**Problem:** Snapshot download blocks daemon startup synchronously.

**Fix:** Download in background thread, report progress via events. Daemon is usable during download.

### 6.4 Parallel container pulls

**Problem:** Image pulls happen sequentially per action thread.

**Fix:** Pull all images in parallel before starting any container. Show pull progress.

---

## PRIORITY 7: Security

### 7.1 Daemon API authentication

Currently the Unix socket has no authentication. Anyone with filesystem access can control the daemon.

**Fix:** Optional token-based auth. Token generated at install time, stored in config file readable only by the daemon user.

### 7.2 Secret management

BASIC auth password = username is insecure. Add:
- `--password` flag to install
- Password hashing (bcrypt) in proxy config
- Secret rotation command

### 7.3 Audit log

Log all administrative actions (start, stop, restart, reload, exec) with user/source info.

---

## Implementation Order

For maximum agent impact, implement in this order:

1. **1.1 + 1.2 + 1.3** — `--output json` + structured errors (agents can parse output)
2. **4.1** — `citeck wait` (agents can wait for conditions atomically)
3. **2.1** — `--non-interactive` install (agents can deploy without interaction)
4. **3.2** — Probe failure categorization (agents don't wait 28h for crashed containers)
5. **4.2** — `citeck deploy` (atomic config deployment)
6. **5.1** — `citeck diagnose` (agents can self-diagnose issues)
7. **1.5** — Exit codes (agents can branch on error type)
8. **3.1 + 3.3 + 3.4** — Reliability fixes
9. **5.3** — Event history (agents poll history instead of maintaining WebSocket)
10. **4.3 + 4.4 + 4.5** — Config management commands
11. **6.x** — Performance
12. **7.x** — Security

---

## Testing Strategy

Each priority should include:
1. Unit tests for new code
2. Integration test with real Docker (configs 1-5 from V1)
3. Agent simulation test: run full deploy workflow using only `--json` output and exit codes
4. Failure injection: test error paths (bad config, crashed container, network failure)
