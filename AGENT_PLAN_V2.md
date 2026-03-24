# Plan V2: Citeck Launcher — Orchestration, UX & Agent Autonomy

## Context

V1 complete: bug fixes, 8 CLI commands, 5 E2E configs pass. This plan makes the launcher a proper orchestration tool for **both humans and agents** — inspired by Kubernetes/Docker Swarm patterns adapted for single-host deployment.

### Design Principles

1. **Human-first defaults, agent-friendly options** — text output by default, `--output json` for agents
2. **Every mutation has a preview** (`--dry-run`) — humans see a readable summary, agents get structured JSON
3. **Every operation is idempotent** (safe to retry by both humans and agents)
4. **Every failure is actionable** — humans get suggestions ("Run `citeck diagnose`"), agents get error codes
5. **The system self-heals** (liveness probes, reconciliation)
6. **State is declarative** (`citeck apply` — desired state → actual state)
7. **Interactive and non-interactive paths coexist** — wizard stays for humans, `--non-interactive` for agents
8. **Dangerous operations require confirmation** — humans get `[y/N]` prompt, agents pass `--yes`

### Human vs Agent UX Contract

| Aspect | Human (default) | Agent (`-o json` / `--yes`) |
|--------|----------------|---------------------------|
| Output format | Colored tables, readable messages | JSON to stdout |
| Progress | Progress bars in stderr | Suppressed (or events via API) |
| Errors | Message + suggestion + exit code | JSON `{error, code, suggestion}` + exit code |
| Confirmations | Interactive `[y/N]` prompt | `--yes` skips prompts |
| Mutations | `--dry-run` shows colored diff | `--dry-run -o json` shows structured changes |
| Install | Interactive wizard | `--non-interactive` / `--from-config` |
| Logs | Streamed to terminal | `--since 5m --errors-only -o json` |

**Rule:** stderr is for humans (progress, hints, warnings). stdout is for data (tables or JSON). Agents use `-o json` and only parse stdout.

---

## PRIORITY 1: Machine-Readable Interface

Without this, agents are blind. This unblocks ALL other agent work.

### 1.1 Global `--output` flag

Add `--output` / `-o` option to `CiteckCli` parent command: `text` (default), `json`.

**Implementation:** `OutputFormat` enum + `Formatter` interface. Commands populate a data object; formatter renders it. Text formatter renders tables/messages; JSON formatter serializes to stdout.

```bash
citeck status --apps -o json
citeck health -o json
citeck inspect proxy -o json
citeck logs proxy --tail 50 -o json   # { "lines": ["...", "..."] }
citeck config validate -o json        # { "checks": [...], "valid": true }
```

**Files:** `cli/commands/CiteckCli.kt`, all command classes, new `cli/output/OutputFormatter.kt`

### 1.2 Structured errors in DaemonClient

Replace all `T?` returns with sealed class:
```kotlin
sealed class DaemonResult<out T> {
    data class Success<T>(val data: T) : DaemonResult<T>()
    data class NotFound(val message: String) : DaemonResult<Nothing>()
    data class ServerError(val code: String, val message: String) : DaemonResult<Nothing>()
    data object ConnectionFailed : DaemonResult<Nothing>()
}
```

Commands translate this to exit codes + error JSON.

### 1.3 Machine-readable exit codes

| Code | Constant | Meaning |
|------|----------|---------|
| 0 | OK | Success |
| 1 | ERROR | General/unknown error |
| 2 | CONFIG_ERROR | Invalid YAML, missing cert, bad config |
| 3 | DAEMON_NOT_RUNNING | Daemon socket not found or not responding |
| 4 | NOT_CONFIGURED | Namespace not configured |
| 5 | NOT_FOUND | App/resource not found |
| 6 | DOCKER_UNAVAILABLE | Docker daemon unreachable |
| 7 | TIMEOUT | Operation timed out |
| 8 | UNHEALTHY | Health check failed |
| 9 | CONFLICT | Another operation in progress / lock held |

### 1.4 Human-friendly error messages with suggestions

When an error occurs in text mode, show actionable guidance:
```
Error: Namespace is not configured (exit code 4)
  Run 'citeck install' to create a configuration, or
  copy a namespace.yml to /opt/citeck/conf/namespace.yml

Error: Docker daemon unreachable (exit code 6)
  Ensure Docker is running: systemctl start docker
  Check permissions: usermod -aG docker $USER
```

In JSON mode, the same info is structured:
```json
{"error": "not_configured", "code": 4, "message": "Namespace is not configured",
 "suggestions": ["Run 'citeck install'", "Copy namespace.yml to /opt/citeck/conf/"]}
```

### 1.5 `--dry-run` on all mutating commands

Every command that changes state gets `--dry-run`:
```bash
citeck deploy --config new.yml --dry-run      # shows what would change
citeck restart proxy --dry-run                 # shows what would happen
citeck update --bundle 2025.13 --dry-run       # shows which images change
citeck reload --dry-run                        # validates config, shows diff
citeck stop --dry-run                          # lists what would be stopped
```

**Human output** (colored, readable):
```
Dry run: deploy new.yml
  STOP    proxy         (running, uptime 5h)
  UPDATE  gateway       nexus.citeck.ru/ecos-gateway:3.3.0 → 3.4.0
  NO-OP   postgres      (unchanged)
  START   new-app       nexus.citeck.ru/ecos-new:1.0.0
  3 apps affected, 16 unchanged
```

**Agent output** (`-o json`):
```json
{"dryRun": true, "changes": [
  {"app": "proxy", "action": "stop", "reason": "removed from config"},
  {"app": "gateway", "action": "update", "oldImage": "...3.3.0", "newImage": "...3.4.0"}
]}
```

### 1.6 `--yes` flag for non-interactive confirmation

Dangerous commands (diagnose --fix, clean, restore) ask for confirmation in text mode:
```
Found 3 issues:
  1. Orphaned container citeck_old_proxy (remove)
  2. Stale socket file /run/citeck/daemon.sock (delete)
  3. Crashed app emodel (restart)

Apply fixes? [y/N]
```

With `--yes` (for agents and scripts): skip confirmation, apply immediately.
```bash
citeck diagnose --fix --yes          # agent: no prompt
citeck clean --execute --yes         # agent: no prompt
citeck restore --from backup.tar.gz --yes
```

---

## PRIORITY 2: Declarative State Management (inspired by `kubectl apply`)

### 2.1 `citeck apply` — Idempotent desired-state command

The single most important command for agents. Takes a desired state and makes it so.

```bash
citeck apply -f namespace.yml                  # make it so
citeck apply -f namespace.yml --dry-run        # show what would change
citeck apply -f namespace.yml --wait           # apply + wait for RUNNING
citeck apply -f namespace.yml --timeout 600    # with timeout
```

**Behavior:**
1. Parse desired config
2. Compare with current running state
3. Compute minimal change set:
   - Config unchanged → no-op (exit 0)
   - Only env/config changed → restart affected apps only
   - Image changed → pull + restart affected apps
   - New app added → start it
   - App removed → stop it
   - Auth type changed → full regenerate
4. Apply changes
5. If `--wait` → wait for all apps RUNNING

**Difference from `deploy`:** `deploy` always does full stop→start cycle. `apply` computes the diff and makes minimal changes. Safe to run in a cron loop.

**API:**
```
POST /api/v1/namespace/apply
Body: { config: <NamespaceConfig>, dryRun: false, wait: false, timeout: 300 }
Response: { changes: [...], status: "applied" }
```

### 2.2 Reconciliation loop (inspired by K8s controllers)

Daemon periodically compares desired state (namespace.yml) with actual state (Docker containers) and fixes drift:

- Container crashed → restart it
- Container removed externally → recreate it
- Config file changed on disk → trigger reload
- Image updated in bundle → pull + restart

Configurable interval (default: 60s). Can be disabled.

```yaml
# namespace.yml
reconciliation:
  enabled: true
  intervalSeconds: 60
```

### 2.3 `citeck diff` — Show pending changes

```bash
citeck diff                                    # running vs namespace.yml on disk
citeck diff -f new-config.yml                  # running vs provided file
citeck diff -f old.yml -f new.yml              # between two files
citeck diff -o json                            # structured output
```

---

## PRIORITY 3: Non-Interactive Install & Config-as-Code

### 3.1 `citeck install --non-interactive`

All config via flags + env vars:
```bash
citeck install \
  --auth basic --users admin \
  --host custom.launcher.ru --port 443 \
  --tls self-signed \
  --bundle community:LATEST \
  --non-interactive

# Or via env vars
CITECK_AUTH_TYPE=keycloak CITECK_PROXY_HOST=prod.example.com citeck install --non-interactive
```

**Env var mapping:**
```
CITECK_AUTH_TYPE, CITECK_AUTH_USERS, CITECK_PROXY_HOST,
CITECK_PROXY_PORT, CITECK_TLS_MODE, CITECK_TLS_CERT,
CITECK_TLS_KEY, CITECK_BUNDLE, CITECK_SNAPSHOT
```

### 3.2 `citeck install --from-config <path>`

Skip wizard, use pre-built config:
```bash
citeck install --from-config /path/to/namespace.yml
citeck install --from-config /path/to/namespace.yml --start  # install + start
```

### 3.3 Idempotent install

```bash
citeck install --from-config ns.yml --force    # overwrite existing
citeck install --from-config ns.yml            # skip if same config exists (exit 0)
```

### 3.4 Config templating

Support environment variable expansion in namespace.yml:
```yaml
proxy:
  host: "${CITECK_HOST:-localhost}"
  port: ${CITECK_PORT:-80}
  tls:
    enabled: ${CITECK_TLS_ENABLED:-false}
```

Resolved at `apply`/`install` time. Original template preserved.

---

## PRIORITY 4: Reliability & Self-Healing

### 4.1 Liveness probes (inspired by K8s)

**Currently:** Only startup probes exist. If an app hangs AFTER reaching RUNNING, nobody notices.

**Add:** Liveness probes that run periodically on RUNNING apps. If probe fails N times → auto-restart.

```kotlin
// In ApplicationDef
val livenessProbe: AppProbeDef? = null  // optional, same format as startup probe
val livenessFailureThreshold: Int = 3
val livenessPeriodSeconds: Int = 30
```

Default liveness probes:
- Webapps: `GET /management/health` every 30s, fail after 3
- Gateway: `GET /management/health` every 30s
- Proxy: `curl http://localhost:80/eis.json` every 30s
- Postgres: `SELECT 1` every 30s

### 4.2 Startup probe failure categorization

**Problem:** Probe retries 10,000 times (28h) even if container crashed.

**Fix:** After each probe failure, check container state:
- Container running → continue retrying (app still starting)
- Container exited → report exit code + last 20 log lines, mark `START_FAILED` immediately
- Container OOMKilled → report OOM, suggest increasing memory limit
- Container restarting → detect restart loop, report

**File:** `core/namespace/runtime/actions/AppStartAction.kt:351-385`

### 4.3 Graceful shutdown ordering (inspired by K8s pod termination)

Define shutdown order — reverse of startup dependencies:
1. Stop proxy first (stop accepting traffic)
2. Stop webapps (drain in-flight requests)
3. Stop infrastructure (postgres, rabbitmq, zookeeper last)

Add configurable `terminationGracePeriodSeconds` per app (default: 30s).

### 4.4 Config validation before reload

```bash
citeck reload --dry-run                        # validate only
citeck reload                                  # validate + apply
```

API validates before applying. Returns structured validation errors.

### 4.5 Graceful daemon shutdown with timeouts

Wrap each `dispose()` in 10s timeout. Log + continue if timeout expires.

### 4.6 Install script rollback

Backup current installation before upgrade. If post-upgrade health check fails → restore.

### 4.7 Fix daemon startup detection

Poll for socket file + HTTP status instead of `process.isAlive`.

---

## PRIORITY 5: Agent Workflow Commands

### 5.1 `citeck wait` — Atomic condition waiting

```bash
citeck wait --status running --timeout 300
citeck wait --app proxy --status running --timeout 60
citeck wait --healthy --timeout 300
citeck wait --status stopped --timeout 60         # wait for stop
citeck wait --app eapps --status running -o json   # JSON result
```

Exit code 0 = condition met, 7 = timeout. Returns final status in JSON.

**Implementation:** WebSocket event subscription with condition matching + timeout.

### 5.2 `citeck deploy` — Full lifecycle deploy

```bash
citeck deploy --config namespace.yml --wait --timeout 600
citeck deploy --config namespace.yml --dry-run
citeck deploy --config namespace.yml --rollback-on-failure
```

Steps: validate → stop current → write config → start → wait → verify.
On `--rollback-on-failure`: backup old config before, restore if new deployment fails.

### 5.3 `citeck update` — Rolling update (inspired by K8s rolling update)

```bash
citeck update                                  # update to latest bundle
citeck update --bundle community:2025.13
citeck update --app proxy --image ecos-proxy:2.26.0
citeck update --strategy rolling               # one app at a time (default)
citeck update --strategy all-at-once           # restart all at once
citeck update --dry-run                        # show what would change
```

Rolling update strategy:
1. Pull all new images
2. For each app with changed image:
   a. Stop app
   b. Start with new image
   c. Wait for RUNNING
   d. If failed → rollback this app to old image, abort remaining
3. Report result

### 5.4 `citeck backup` / `citeck restore`

```bash
citeck backup --output /path/to/backup.tar.gz
citeck backup --output /path/to/backup.tar.gz --include-volumes  # include data
citeck restore --from /path/to/backup.tar.gz
citeck restore --from /path/to/backup.tar.gz --dry-run
```

### 5.5 `citeck rollback`

```bash
citeck rollback                                # rollback to previous config
citeck rollback --to <config-version>          # rollback to specific version
```

Keep last N configs (default: 5) in `/opt/citeck/conf/history/`.

---

## PRIORITY 6: Observability & Diagnostics

### 6.1 `citeck diagnose` — with auto-fix

```bash
citeck diagnose                                # find problems
citeck diagnose -o json                        # structured output
citeck diagnose --fix                          # find + fix automatically
citeck diagnose --fix --dry-run                # show what would be fixed
```

**Checks and auto-fixes:**
| Check | Auto-fix |
|-------|----------|
| Docker unreachable | Report instructions |
| Container crashed | Restart it |
| Container missing (expected running) | Recreate from config |
| Orphaned containers (no config) | Remove them |
| Stale lock file | Delete it |
| Stale socket file | Delete it |
| Orphaned volumes | List them (manual cleanup) |
| Port conflict | Report which process holds port |
| TLS cert expiring (<30 days) | Renew if Let's Encrypt |
| Disk space low (<5GB) | Report + suggest cleanup |
| DNS resolution failure | Report |
| Container network unreachable | Recreate Docker network |
| App in START_FAILED | Show exit code + last logs |

### 6.2 `citeck logs` improvements

```bash
citeck logs proxy --errors-only                # filter ERROR/Exception lines
citeck logs proxy --search "connection refused" # search pattern
citeck logs proxy --since 5m                   # time-based
citeck logs proxy --since 5m --until 2m        # time range
citeck logs --all --errors-only                # errors from ALL apps
citeck logs --all --errors-only -o json        # structured
```

### 6.3 `citeck describe <app>` (inspired by `kubectl describe pod`)

Rich app description with events, conditions, and history:
```
Name:         proxy
Image:        nexus.citeck.ru/ecos-proxy-oidc:2.25.6
Status:       RUNNING
Container ID: 2eabd77c6225
Started:      2026-03-24T12:18:15Z
Uptime:       2h 30m
Restarts:     0
Ports:        443:443/TCP
Memory:       32M / 128M (25%)
CPU:          0.1%
Network:      citeck_network_default_daemon

Conditions:
  Ready       True    since 2026-03-24T12:18:45Z
  Probing     False

Startup Timeline:
  Pull:       2026-03-24T12:18:00Z (2.1s)
  Create:     2026-03-24T12:18:02Z (0.3s)
  Init:       2026-03-24T12:18:03Z (5.2s)  [sed nginx config, nginx -s reload]
  Probe:      2026-03-24T12:18:08Z → 2026-03-24T12:18:45Z (37s, 4 attempts)
  Running:    2026-03-24T12:18:45Z

Recent Events:
  12:18:00  Pulling    image nexus.citeck.ru/ecos-proxy-oidc:2.25.6
  12:18:02  Pulled     image found locally
  12:18:02  Creating   container citeck_proxy_default_daemon
  12:18:03  Created    container created
  12:18:03  InitAction sed -i ... /etc/nginx/conf.d/default.conf
  12:18:08  InitAction nginx -s reload
  12:18:08  Probing    startup probe started
  12:18:45  Running    startup probe passed (HTTP 200)
```

### 6.4 `citeck top` (inspired by `kubectl top`)

```bash
citeck top                                     # all apps, sorted by CPU
citeck top --sort memory                       # sort by memory
citeck top --watch                             # live refresh
citeck top -o json                             # structured
```

### 6.5 Event history + operation history

```bash
citeck events --since 1h                       # app state change events
citeck events --since 1h -o json
citeck history                                 # operation log (start, stop, restart, deploy)
citeck history --since 1d -o json
```

**Operation history** is persisted to `/opt/citeck/log/operations.jsonl`:
```json
{"ts":"2026-03-24T12:00:00Z","op":"start","result":"ok","duration":180000,"apps":19}
{"ts":"2026-03-24T14:30:00Z","op":"restart","app":"proxy","result":"ok","duration":5000}
{"ts":"2026-03-24T15:00:00Z","op":"reload","result":"error","error":"invalid YAML at line 12"}
```

### 6.6 Startup timeline in inspect/describe

Track per-app: pullStart/pullEnd, createStart/createEnd, initStart/initEnd, probeStart/probeEnd, runningAt, totalMs.

---

## PRIORITY 7: Operational Commands

### 7.1 `citeck preflight` — Pre-deploy resource check

```bash
citeck preflight                               # check current config
citeck preflight --config new.yml              # check new config
citeck preflight -o json
```

Checks:
- RAM: sum of all app memory limits vs available
- Disk: estimated data size vs free space
- Ports: check for conflicts with running services
- Docker: version, storage driver, available disk
- Images: which need pulling, estimated download size
- Network: DNS resolution, internet access (for pulls)

### 7.2 `citeck cert` — Certificate lifecycle

```bash
citeck cert status                             # expiration, issuer, SANs
citeck cert status -o json
citeck cert check --warn-days 30               # exit code 1 if expiring soon
citeck cert renew                              # renew (LE or regenerate self-signed)
citeck cert generate --host example.com        # generate new self-signed
```

### 7.3 `citeck cp` — Copy files to/from container (inspired by `kubectl cp`)

```bash
citeck cp proxy:/etc/nginx/conf.d/default.conf ./nginx.conf
citeck cp ./custom.conf proxy:/etc/nginx/conf.d/custom.conf
```

Uses `docker cp` under the hood.

### 7.4 `citeck port-forward` — Temporary port exposure (inspired by `kubectl port-forward`)

```bash
citeck port-forward postgres 5432:5432         # expose postgres on localhost:5432
citeck port-forward emodel 8080:17020          # expose emodel debug port
```

Creates temporary `socat`/`docker exec` forwarding. Useful for debugging.

### 7.5 `citeck clean` — Cleanup orphaned resources

```bash
citeck clean                                   # show what would be cleaned
citeck clean --execute                         # actually clean
citeck clean --volumes                         # include orphaned volumes
citeck clean --images                          # remove unused images
```

---

## PRIORITY 8: Performance

### 8.1 Logs follow via WebSocket

Replace polling with `WS /api/v1/apps/{name}/logs/stream`.

### 8.2 Parallel image pulls

Pull all images concurrently before starting containers. Show progress:
```
Pulling images...
  [=====>    ] proxy       (2/3 layers)
  [=========>] gateway     (done)
  [=>        ] emodel      (1/5 layers)
```

### 8.3 Async snapshot import with progress

Background download, report via events. Daemon functional during download.

### 8.4 Incremental config reload

Only restart apps that actually changed (not full stop→start).
Compute diff between old and new generated app definitions.

---

## PRIORITY 9: Security

### 9.1 Daemon API token auth

Optional bearer token. Generated at install, stored in `/opt/citeck/conf/daemon-token`.

### 9.2 Secret management

- Support external secret references: `password: ${secret:vault/citeck/admin-password}`
- `citeck secret set <key> <value>` — store encrypted secrets
- `citeck secret rotate` — regenerate all secrets

### 9.3 Audit log

All mutating operations logged to `/opt/citeck/log/audit.jsonl` with: timestamp, command, user, source IP, result.

### 9.4 Image signature verification

Optional: verify image signatures before pulling (cosign/notary).

---

## Implementation Order

Optimized for both human and agent value:

| Phase | Items | Human value | Agent value |
|-------|-------|-------------|-------------|
| **Phase 1** | 1.1-1.6 output/errors/exit codes/dry-run/--yes | Readable errors with suggestions, dry-run preview | JSON output, structured errors, exit codes |
| **Phase 2** | 5.1 `citeck wait` + 6.3 `citeck describe` | Rich app details, readable event timeline | Atomic waiting, structured app state |
| **Phase 3** | 2.1 `citeck apply` + 2.3 `citeck diff` | Preview changes before applying, safe config updates | Declarative idempotent state management |
| **Phase 4** | 3.1-3.4 non-interactive install | Wizard stays, `--from-config` shortcut | Full automation via flags/env vars |
| **Phase 5** | 4.1-4.2 liveness probes + probe categorization | Automatic restart of hung apps, fast failure feedback | Self-healing, no 28h probe waits |
| **Phase 6** | 6.1-6.2 diagnose --fix + log filtering | Interactive fix confirmation, error log search | Auto-remediation with `--yes` |
| **Phase 7** | 6.4-6.5 top + history | Live resource dashboard, operation audit trail | Resource monitoring, context recovery |
| **Phase 8** | 5.3 rolling update + 5.5 rollback | Safe updates with per-app progress | Automated rollback on failure |
| **Phase 9** | 7.1-7.2 preflight + cert lifecycle | Pre-deploy warnings, cert expiry alerts | Fail-fast, proactive cert renewal |
| **Phase 10** | 2.2 reconciliation loop | Zero-maintenance drift correction | Continuous self-healing |
| **Phase 11** | 5.4 backup + 7.3-7.5 cp/port-forward/clean | Debugging tools, data protection | Full lifecycle automation |
| **Phase 12** | 8.x performance + 9.x security | Progress bars for pulls, faster startup | Parallel pulls, audit log |

---

## Human UX Guidelines (apply across ALL phases)

These apply to every new command and feature:

### Output conventions
- **Text mode** (default): colored output, tables with padding, progress bars in stderr
- **JSON mode** (`-o json`): clean JSON to stdout, no progress/color, no extra text
- Progress (pull, download, startup) goes to **stderr** — visible to humans, invisible to `jq`
- Use ANSI colors for status: green=RUNNING, red=FAILED, yellow=STARTING/WARNING

### Status display
```
$ citeck status --apps

Name:      Production (default)                 ← bold
Status:    RUNNING                               ← green
Bundle:    community:2025.12

APP              STATUS     IMAGE                         CPU    MEMORY
proxy            RUNNING    ecos-proxy-oidc:2.25.6        0.1%   32M/128M    ← green
gateway          RUNNING    ecos-gateway:3.3.0            0.6%   533M/1.0G   ← green
emodel           STARTING   ecos-model:2.35.7             --     --          ← yellow
postgres         FAILED     postgres:17.5                 --     --          ← red
  └─ Exit code 1: configuration file contains errors                         ← hint
```

### Error messages
Always include:
1. **What happened** (one line)
2. **Why** (if known)
3. **What to do** (suggestion)

```
Error: App 'proxy' failed to start
  Container exited with code 1 after 3.2s
  Last log: nginx: [emerg] cannot load certificate "/app/tls/server.crt"
  Suggestion: Check TLS certificate path in namespace.yml
              Run 'citeck config validate' to verify configuration
```

### Confirmation prompts
For destructive operations in text mode:
```
$ citeck clean --execute
Found 3 orphaned resources:
  Container  citeck_old_proxy_default_daemon   (stopped 3 days ago)
  Volume     citeck_volume_old_data            (unused)
  Network    citeck_network_old                (no containers)

Remove these resources? [y/N] y
Removed 3 resources.
```

Skipped with `--yes` for agents/scripts.

### Progress display (stderr)
```
$ citeck apply -f namespace.yml --wait
Applying configuration...
  Pulling images    [████████░░] 4/5
  Starting apps     [██████░░░░] 12/19
  Waiting for proxy [probe 3/10, 30s elapsed]
All 19 apps running. Took 2m 15s.
```

In JSON mode, none of this appears. Final result only:
```json
{"status": "applied", "apps": 19, "running": 19, "duration": 135000}
```

---

## Testing Strategy

### Per-phase testing
1. **Unit tests** — new code, formatters, diff logic, validation
2. **Integration tests** — real Docker, configs 1-5 from V1
3. **Human UX review** — run each command in text mode, verify: colors, table alignment, error messages have suggestions, progress bars work, confirmations prompt correctly
4. **Agent simulation** — script that deploys using ONLY `--json` output and exit codes:
   ```bash
   citeck install --from-config ns.yml --non-interactive
   citeck apply -f ns.yml --wait --timeout 600 -o json
   STATUS=$(citeck status -o json | jq -r '.status')
   [ "$STATUS" = "RUNNING" ] || citeck diagnose --fix -o json
   ```
4. **Failure injection** — bad config, crashed container, network down, disk full, OOM
5. **Idempotency test** — run `citeck apply` 3 times, verify no unnecessary restarts

### Agent E2E scenario

Full autonomous workflow test:
```bash
# 1. Pre-flight
citeck preflight --config ns.yml -o json || exit 1

# 2. Deploy
citeck apply -f ns.yml --wait --timeout 600 -o json
[ $? -eq 0 ] || { citeck diagnose --fix -o json; exit 1; }

# 3. Verify
citeck health -o json | jq -e '.healthy' || exit 1

# 4. Update
citeck update --dry-run -o json
citeck update --strategy rolling --wait --timeout 600

# 5. Monitor (periodic)
citeck health -o json
citeck cert check --warn-days 30
citeck top -o json

# 6. Troubleshoot (on alert)
citeck diagnose -o json
citeck logs --all --errors-only --since 5m -o json
citeck describe <failing-app> -o json
```
