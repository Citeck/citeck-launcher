# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Citeck Launcher manages Citeck namespaces and Docker containers. It is a single Go binary (~24 MB) that serves as both CLI and daemon. The embedded React Web UI is served over TCP by the server-mode daemon (port configurable via `daemon.yml`; disabled with `--no-ui`); desktop mode renders the same Web UI through a Wails webview that talks to the daemon over the Unix socket.

The repo also keeps the legacy Kotlin/JVM launcher under git tags `v1.0.0`–`v1.3.9` for reference and rollback (`git show v1.3.8:path/to/file`). The desktop migrator opens its H2 `storage.db` read-only on first start.

## Build & Development Commands

### Go + Web UI (primary)

```bash
make build                    # Build Go binary + embed React web UI → build/bin/citeck-server
make build-fast               # Build Go only (skip web rebuild) → build/bin/citeck-server
make build-desktop            # Build desktop (Wails) binary → build/bin/citeck-launcher
make test                     # Run all tests (Go + Vitest)
make test-unit                # Go unit tests only (./internal/...)
make test-race                # Go tests with race detector + 120s timeout
make test-coverage            # Go coverage report → coverage.html
make lint                     # Run Go (golangci-lint) + Web (eslint) linters
make fmt                      # Format Go code
make tidy                     # go mod tidy
make tools                    # Install golangci-lint v2.11.4
make clean                    # Remove build artifacts
build/bin/citeck-server start --foreground   # Run daemon in foreground
./build/bin/citeck-launcher            # Run desktop app (Wails webview)
```

### Desktop installers

Packaging configs live in `packaging/` (nfpm → deb/rpm, WiX → msi, macOS scripts → dmg).
A `v*.*.*` tag builds all installers via `.github/workflows/release-go.yml` and attaches
them to the GitHub Release alongside the server tarballs. Release artifacts are named
`citeck-desktop_<version>_<os>_<arch>.<ext>` (the package/app *identity* stays `citeck-launcher`
for the upgrade contract). Clean upgrade over the legacy
1.* installer: same deb/rpm package name `citeck-launcher`, shared Windows `UpgradeCode`,
and a `citeck-launcher.app` bundle name matching 1.*'s path. Run the local clean-upgrade
e2e with `./scripts/test-deb-upgrade.sh` (needs Docker + GTK3 dev libs).

## Architecture

### Go Daemon + CLI (`internal/`)

| Package | Purpose |
|---|---|
| `internal/cli/` | Cobra CLI commands (start, stop, status, setup, install, upgrade, etc.) |
| `internal/daemon/` | HTTP server, API routes (SSE events, config, volumes), middleware |
| `internal/namespace/` | Config parsing, container generator, runtime state machine, reconciler |
| `internal/docker/` | Docker SDK wrapper (containers, images, exec, logs via stdcopy, probes) |
| `internal/bundle/` | Bundle definitions and resolution from git repos |
| `internal/git/` | Git clone/pull via go-git (pure Go, with token auth, hard-reset, reclone) |
| `internal/config/` | Filesystem paths, daemon config (daemon.yml), workspace dir scanner |
| `internal/storage/` | Store interface + FileStore (server) + SQLiteStore (desktop) + SecretService (AES-256-GCM encryption) |
| `internal/h2migrate/` | Pure-Go H2 MVStore reader (chunk → layout → meta → maps), LZF decompressor, TransactionStore `VersionedValue` wrapper stripping, AES-GCM secrets decrypt, Kotlin→Go translators for `ApplicationDef` / `BundleDef`, one-shot `storage.db → storage.db.kotlin-bak` backup, drives H2→SQLite migration (no JAR / JRE) |
| `internal/client/` | DaemonClient over Unix socket + mTLS TCP transport |
| `internal/output/` | Text/JSON output formatter, tables, colors |
| `internal/api/` | Shared API types (DTOs), path constants |
| `internal/appdef/` | Application definition models (ApplicationDef, ApplicationKind) |
| `internal/appfiles/` | Embedded resource files (go:embed) |
| `internal/form/` | Form field specs, built-in field definitions, validation |
| `internal/snapshot/` | Volume snapshot export/import (ZIP + tar.xz) |
| `internal/tlsutil/` | TLS cert utilities (self-signed, client cert, CA pool loader) |
| `internal/fsutil/` | Atomic file write (temp+fsync+rename), RotatingWriter (log rotation), CleanLogHandler (human-readable slog) |
| `internal/acme/` | ACME/Let's Encrypt client + auto-renewal service |
| `internal/i18n/` | Embedded JSON locale files (shared source-of-truth between CLI and web UI) |
| `internal/desktop/` | Wails desktop runner (browser launcher, single-instance guard) |
| `internal/namespace/nsactions/` | Pull/start retry constants and auth-error helpers for `runtime_workers.go` |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`. Darcula/Lens dark theme.

**Pages:**
- `Dashboard.tsx` — sidebar + app table + right drawer overlay + bottom panel
- `AppDetail.tsx` — full-page fallback (composes AppDrawerContent + AppConfigEditor)
- `Logs.tsx` — thin wrapper for LogViewer
- `Config.tsx` — health checks + ConfigEditor
- `Volumes.tsx` — Docker volume management (namespace-scoped, list/delete)
- `DaemonLogs.tsx` — thin wrapper for DaemonLogsViewer
- `Welcome.tsx` — namespace list, quick start buttons, create/delete
- `Wizard.tsx` — multi-step namespace creation (8 steps, language-aware)
- `Secrets.tsx` — secret CRUD with type selector, test button, encryption status, inline unlock
- `Diagnostics.tsx` — system health checks with fix actions

**Components:**
- `AppTable.tsx` — grouped table with panel actions (openDrawer, openBottomTab)
- `BottomPanel.tsx` — IDE-style bottom panel (lazy mount, drag-resize, collapse)
- `RightDrawer.tsx` — overlay drawer with slide animation
- `LogViewer.tsx` — log viewer (virtual list, regex search, level filter, streaming, active prop)
- `ConfigEditor.tsx` — namespace.yml viewer/editor with YAML highlighting
- `DaemonLogsViewer.tsx` — daemon logs streaming (fetch-based, replaces polling)
- `AppDrawerContent.tsx` — app inspect details + action buttons (logs, config, restart)
- `AppConfigEditor.tsx` — per-app YAML config + mounted files editor
- `YamlViewer.tsx` — shared YAML syntax highlighter
- `TabBar.tsx` — IDE-style tab navigation + language selector + theme toggle
- `StatusBadge.tsx` — color-coded status with dot indicator and i18n display names
- `NamespaceControls.tsx` — Start/Stop/Reload with confirm
- `ConfirmModal.tsx` — reusable confirm dialog (always mounted, showModal/close)
- `Toast.tsx` — toast notifications (theme-aware colors)
- `ErrorBoundary.tsx` — React error boundary with reload button
- `ContextMenu.tsx` — right-click context menu with items/dividers
- `FormDialog.tsx` — spec-driven form dialog (text/number/password/select/checkbox)
- `JournalDialog.tsx` — data table dialog with search, selection, custom buttons
- `RestartEvents.tsx` — restart-event log rendered in a bottom-panel tab

**Lib:**
- `api.ts` — REST API client (fetchWithTimeout, CSRF, exported API_BASE)
- `store.ts` — Zustand dashboard store (SSE events, exponential backoff reconnect)
- `panels.ts` — Zustand panel store (drawer, bottom tabs, height persistence)
- `i18n.ts` — i18n store (8 locales bundled synchronously, t() + useTranslation())
- `websocket.ts` — SSE EventSource wrapper (not WebSocket despite filename)
- `tabs.ts` — Tab state management (zustand)
- `toast.ts` — Toast notification store (zustand, auto-dismiss)
- `types.ts` — TypeScript interfaces matching Go DTOs

**Hooks:**
- `useResizeHandle.ts` — pointer-capture drag hook for bottom panel resize
- `useContextMenu.ts` — context menu state management

### Entry Point

`cmd/citeck/main.go` — CLI entry point (cobra root command).

## Code Style

### Go
- Standard `gofmt` formatting
- `golangci-lint` v2.11.4 with 21 linters (`.golangci.yml`): dupl, errorlint, gochecknoinits, gocritic, gocyclo, gosec, govet (shadow), ineffassign, misspell (US), modernize, nakedret, nestif, prealloc, revive, staticcheck, testifylint, unconvert, unparam, unused, wrapcheck. **Always run `make tools` before linting to get the pinned CI version** — newer gosec taint analysis catches more G703/G706 false positives than 2.7.2.
- Tabs for indentation (Go standard)
- Custom slog handler (`fsutil.CleanLogHandler`): `2026-04-01T02:58:51Z INFO  Message key=value`

### Web (React/TypeScript)
- Tailwind CSS 4 for styling
- ESLint for linting
- lucide-react for icons

## Key Dependencies

### Go
- **CLI**: spf13/cobra
- **Docker**: docker/docker/client (official SDK) + docker/docker/pkg/stdcopy
- **YAML**: gopkg.in/yaml.v3
- **CLI output**: charmbracelet/lipgloss
- **Testing**: stretchr/testify
- **HTTP**: net/http (stdlib, Go 1.22+ routing)
- **Logging**: log/slog (stdlib)
- **Embed**: embed (stdlib, for web UI + appfiles)
- **SQLite**: modernc.org/sqlite (pure Go, no CGO — desktop mode storage)

### Web UI
- **Framework**: React 19 + TypeScript
- **Build**: Vite
- **Styles**: Tailwind CSS 4
- **Icons**: lucide-react
- **State**: Zustand
- **Testing**: Vitest + Testing Library
- **E2E**: Playwright

## Key Technical Decisions

- **SSE** (not WebSocket) for real-time events
- **Unix socket only** for daemon communication in server mode; mTLS TCP reserved for future Web UI
- **Smart regenerate** via deployment hash comparison (like `docker-compose up`) — unchanged containers keep running
- **Namespace runtime is a single-threaded state machine with signal-queue wake-ups.** One `runtimeLoop` goroutine owns every mutation to `r.apps`, per-app status, namespace status, and persistence. External commands enqueue via `cmdQueue` (typed, coalescing, 500ms back-pressure); workers (pull / start / stop / probe / stats / reconcile / liveness) run off-loop on the dispatcher, post typed Results back on `resultCh`; `applyWorkerResult` applies state-machine transitions under lock. Per-iteration `stepAllApps` walks all non-detached apps driving transitions T1–T33. See the `internal/namespace/runtime.go` package header for the concurrency rules and architecture diagram.
- **3-phase doStart**: I/O outside lock → prepare plans → atomic state commit under lock. Detached apps get STOPPED status in the commit phase (not pulled/started). Stale containers (existing && !reuse) enter as STOPPING+initialSweep+desiredNext=READY_TO_PULL so the state machine drives the recreate; no parallel inline Docker stops.
- **`:snapshot` pre-pull on reload/start**: images whose tag contains "snapshot" are pulled from the registry before the hash diff. Without this, a dev push under the same tag would be silently missed (hash computed from stale local digest matches running container → adoption).
- **Graceful shutdown**: phased stop groups (proxy → webapps → keycloak → infra)
- **Detach mode** (binary upgrade): `Runtime.Detach()` exits without stopping containers for zero-downtime binary upgrades; Docker owns containers, not the launcher (same principle as kubelet restarts)
- **Per-app detach**: `citeck stop <app>` marks app as `manualStoppedApps` (desired-state-first, like k8s) and stops container. Detached apps are excluded from start/reload/regenerate, skipped by reconciler and liveness probes, and treated as satisfied in `waitForDeps`. `citeck start <app>` re-attaches. State persisted in `state-{nsID}.json`.
- **Template detachedApps**: workspace `namespaceTemplates[].detachedApps` applied on first start (no persisted state). Install wizard sets `template: "default"` in namespace.yml.
- **`GetHashInput` stability** is a hard compatibility contract across versions — changes require migration
- **Secrets**: AES-256-GCM per-secret encryption via `SecretService`; system secrets (JWT, OIDC, admin password, citeck SA) via `resolveOneSystemSecret` pattern
- **citeck service account**: single shared SA named `citeck` used in two systems: (1) Keycloak master realm (admin role) for kcadm ops, (2) RabbitMQ (monitoring tag, vhost `/` full perms) for webapp AMQP auth and observer management-API monitoring. One 32-char random password stored as `_citeck_sa` system secret. Used by init script (kcadm.sh), admin password change handler, and webapp→Keycloak integration (`${KK_ADMIN_USER}/${KK_ADMIN_PASSWORD}` template vars + `ECOS_WEBAPP_RABBITMQ_USERNAME/_PASSWORD`). Survives snapshot import because Keycloak and RabbitMQ init actions create/sync the SA on every container start.
- **Admin password**: generated on first server-mode start; seeded into both Keycloak realms (`master` via `KC_BOOTSTRAP_ADMIN_PASSWORD` on empty DB + `ecos-app` realm via init script) and shared with RabbitMQ / PgAdmin admin-UI users. Webapps do NOT use the admin user to connect to RabbitMQ — they use the stable `citeck` SA, so admin-password rotation never requires webapp recreation. Desktop mode always uses "admin". The Keycloak init script never touches the master realm `admin` password on re-run, so rotations done via `citeck setup admin-password` are preserved across container restarts.
- **Admin password change** via `citeck setup admin-password`: rotates Keycloak `master` + `ecos-app` realms, RabbitMQ, and PgAdmin. The SA `citeck` password is stable (launcher uses it for internal Keycloak/RabbitMQ auth and must not lose access). Authenticates as the citeck SA → kcadm.sh set-password for `ecos-app` (fatal on failure), then `master` (best-effort — logged but non-fatal since the SA can still manage Keycloak), then rabbitmqctl change_password for the RabbitMQ admin user (UI only), then setup.py for PgAdmin. All runtime, no container restart; **no webapp reload** — webapps use the citeck SA for RabbitMQ and are unaffected by the admin-password change.
- **Email config**: via env vars (`SPRING_MAIL_HOST/PORT/PROTOCOL`, `ECOS_NOTIFICATIONS_EMAIL_FROM_DEFAULT/FIXED`), NOT CloudConfig (disabled in server mode). When email configured, mailhog container is not generated and proxy skips `MAILHOG_TARGET` env.
- **Two storage backends**: flat files (server) / SQLite (desktop); desktop mode via explicit `--desktop` flag (hidden from server binary help)
- **Kotlin 1.x → Go 2.x migration**: pure-Go H2 MVStore reader (no JAR, no JRE download). `internal/h2migrate/mvstore.go` walks chunk headers → layout map → meta map → user-data maps, strips H2 `TransactionStore` `VersionedValue` wrapper (`varLong(operationId) || value`) on user maps, decompresses LZF pages (with extended back-ref byte-order fix). `internal/h2migrate/applicationdef_compat.go` + `bundledef_compat.go` translate Kotlin entity JSON into Go shapes. A one-shot atomic `storage.db → storage.db.kotlin-bak` backup runs before the first migration; `storage.db` itself is opened read-only so Kotlin 1.x can be reinstalled at any time. When the H2 reader cannot open the file at all, a filesystem-fallback safety net emits Kotlin-parity default authentication (BASIC + admin/fet). SQLite schema versions v3 (`Secret.Username`), v4 (per-workspace `SelectedNs` map), v5 (`git_repo_state` table) are applied after import.
- **go-git** (pure Go) for git operations — no external git binary required
- **ACME** profiles via custom JWS; LE works with IPs via shortlived profile (~6 day certs)
- **Reconciler**: exponential backoff retry for failed apps (1m → 10m max); liveness probes with 3-failure threshold. Does NOT touch detached (STOPPED + manualStoppedApps) apps.
- **Snapshot import**: CLI normalizes .zip suffix, validates existence via list endpoint BEFORE stopping namespace. Server validates name/file before lock acquisition. Event types: `snapshot_complete`/`snapshot_error` for both export and import.
- **install.sh** is a thin bootstrap (~260 lines): fetch + SHA256 verify + exec. All lifecycle logic (install/upgrade/rollback/SIGKILL preserve/systemd drop-in) lives in Go (`internal/cli/installer_lifecycle.go`)
- **Install wizard**: prefers `systemctl start citeck` when systemd service is installed; falls back to `forkDaemon()` when systemd unavailable
- **CloudConfigServer** skipped in server mode (webapps disable it via env)
- **Memory**: Community needs 16GB RAM minimum; Enterprise (24 apps) needs 24–32GB. On 16GB, detach non-essential apps (`citeck stop onlyoffice attorneys ai edi`) to free 4–5GB.

## CLI Conventions

- `stop <app>` detaches (persists across restart); `stop` (no args) stops all but doesn't detach
- `start <app>` re-attaches; `start` (no args) starts all non-detached
- `--detach`/`-d` for async on all commands (start, stop, reload) — don't wait, return immediately
- `--force` for destructive ops (clean); dry-run by default
- `--format json` for scripting on any command
- `--yes` to skip confirmations
- Hidden flags: `--desktop`, `--no-ui`, `_daemon` (internal)

## Agent Testing Guide (server-side)

Constraints and tactics for automated server-side testing on a 16GB box. Read before running lifecycle tests.

### Memory management (critical)

Enterprise bundle needs ~15GB for 24 Java apps. On 16GB server any extra container (S3Mock, MailPit, Playwright browser) triggers OOM → SSH hangs 10-20 min → recovery.

**Always detach non-essential apps BEFORE adding test infrastructure:**

```bash
# Frees ~4-5GB on enterprise
for app in onlyoffice attorneys ecom service-desk ecos-project-tracker ai edi; do
  citeck stop $app 2>/dev/null
done
# Verify: free -h → 5GB+ available
```

If SSH becomes unresponsive: wait 5 minutes (OOM killer finishes), don't retry aggressively. `systemctl stop citeck` + kill orphan containers to recover.

### Startup timing

- **Infrastructure** (postgres, mongo, rabbitmq, zookeeper): 30s–1m
- **Keycloak**: 1–2 min (5–10 min on fresh DB — realm import)
- **Webapps**: 5–15 min (Java startup + startup probes 90×10s)
- **Enterprise full startup on 16GB**: 10–15 min
- **`citeck reload`** on enterprise: 2–5 min

Don't poll more frequently than 30s. Use `sleep 120 && check` in background tasks.

### Docker network gotchas

- **Server mode**: only proxy publishes ports. `docker port <container>` returns empty for webapps.
- **Internal access**: use `docker inspect <name> -f "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"` to get container IP, then curl on its `SERVER_PORT` env (17022, 17026, 17027, etc. — varies per app).
- **Port numbers are not stable** — read from `docker inspect <name> --format "{{json .Config.Env}}" | grep SERVER_PORT` each time.

### Keycloak and auth

- **citeck SA** is the service account used for all launcher→Keycloak ops and webapp→RabbitMQ AMQP auth. Password in `/opt/citeck/conf/secrets/_citeck_sa.json` (encrypted). Survives snapshot import. In Keycloak it has the master `admin` role; in RabbitMQ it has `monitoring` tag + vhost `/` full permissions.
- **Admin bootstrap password**: `docker exec citeck_keycloak_default printenv KC_BOOTSTRAP_ADMIN_PASSWORD` — one-time bootstrap for master admin, not the current password after snapshot restore.
- **OIDC client secret**: `docker exec citeck_keycloak_default /opt/keycloak/bin/kcadm.sh ...` to fetch (see `internal/namespace/generator.go` init script).
- **Gateway access**: OIDC token via `/realms/ecos-app/protocol/openid-connect/token` + `Authorization: Bearer <token>`.
- **Webapp direct access**: JWT HMAC-SHA256 with `ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET`. Not straightforward — prefer gateway API when possible.

### S3 testing

- **Fake-S3** (`lphoward/fake-s3`): NOT compatible with MinIO SDK used by ecos-content. Fails on `?location=` request. Avoid.
- **S3Mock** (`adobe/s3mock`): works, but `initialBuckets` env var doesn't create buckets automatically. Create manually: `curl -X PUT http://s3mock:9090/ecos-content`.
- **MinIO**: works but ~300MB RAM (too heavy for 16GB enterprise).
- **Content upload via ECOS**: `POST /gateway/emodel/api/ecos/webapp/content` with `multipart/form-data` (file, name, dir). Returns `{"entityRef":"emodel/temp-file@..."}`.
- **Set default S3 storage**: mutate `eapps/config@app/emodel$default-content-storage` with `_value?json:{"ref":"content/storage@content-storage-s3"}`.

### Background task polling

Don't poll background SSH commands with `cat output.file` every second — wastes context. Instead:
- Start the command with `run_in_background: true` (notification on completion)
- Include `sleep N && check` in the command itself
- Use short timeouts (5–15s) for direct queries when checking state

### Common test scripts (server-side)

```bash
# Quick status
./scripts/ssh.sh 'citeck status 2>/dev/null | grep -c "RUNNING"'
# Use grep -c "RUNNING" NOT -cw RUNNING — ANSI colors break word boundary

# Memory check
./scripts/ssh.sh 'free -h | grep Mem'

# Emergency stop all containers (recover from OOM)
./scripts/ssh.sh 'docker kill $(docker ps -q); citeck stop --shutdown'
```

## TUI Testing

TUI commands (`install`, `setup`, `migrate`, etc.) are tested via tmux — it provides
screen capture as plain text and accepts key injection without modifying the binary.

```bash
# Start a session with fixed dimensions
tmux new-session -d -s tui-test -x 120 -y 40

# Launch a TUI command
tmux send-keys -t tui-test "./build/bin/citeck install" Enter
sleep 1

# Read current screen as plain text (no ANSI codes)
tmux capture-pane -t tui-test -p

# Send keys
tmux send-keys -t tui-test Down        # arrow down
tmux send-keys -t tui-test Up          # arrow up
tmux send-keys -t tui-test "" Enter   # confirm (C-m, not literal Enter)
tmux send-keys -t tui-test "sometext" # type text

# Tear down
tmux kill-session -t tui-test
```

**Autonomous test loop:** `make build-fast` → start tmux session → capture screen →
analyze text → send keys → capture → analyze → fix code if broken → rebuild → repeat.

Cover all interaction branches: happy path, validation errors, back navigation,
cancellation, edge inputs (empty, too long, invalid chars). Fix any regressions before
moving on.

### Visual screenshots via Playwright

To verify colors, layout, and styling — not just text content:

```bash
# 1. Capture screen with ANSI escape codes and convert to HTML
tmux capture-pane -t tui-test -p -e | aha --no-header > /tmp/tui-screen.html

# 2. Wrap in a styled page (dark terminal background, monospace font)
cat > /tmp/tui-preview.html << 'EOF'
<!DOCTYPE html><html><head><meta charset="utf-8"><style>
body { margin: 0; background: #1e1e2e; padding: 24px; }
pre { font-family: 'JetBrains Mono', monospace; font-size: 14px;
      line-height: 1.5; color: #cdd6f4; background: #1e1e2e;
      margin: 0; white-space: pre-wrap; }
</style></head><body><pre>CONTENT_HERE</pre></body></html>
EOF
# (replace CONTENT_HERE with the inner content of /tmp/tui-screen.html)

# 3. Serve locally and screenshot with Playwright
python3 -m http.server 18888 &
# then: browser_navigate http://localhost:18888/tui-preview.html → browser_take_screenshot
```

**When to use visual screenshots:** checking active-item highlight color, border/glyph
rendering, truncation, layout regressions. Plain text capture is sufficient for logic
and navigation tests.

**Why not vhs:** vhs requires `ttyd` + `ffmpeg` (~200 MB) and only records fixed
scenarios — no mid-session inspection. The tmux approach allows reading screen state
at each step and reacting programmatically.

## CI/CD

GitHub Actions:
- **Release workflow** (`.github/workflows/release-go.yml`): triggered by `v*.*.*` tags, builds `linux/{amd64,arm64}` server binaries (matrix build), publishes the GitHub release directly (`draft: false`). Uses `go-version-file: go.mod`.
- **CI workflow** (`.github/workflows/ci.yml`): triggered on push/PR to master and `release/**` branches. Runs `go vet`, `golangci-lint v2.11.4`, `go test -race`, `pnpm vitest run`, full server build.
- **Linting**: `.golangci.yml` v2 format, 21 linters, G104 excluded (cleanup errors), test files relaxed for dupl/gosec/unparam.
