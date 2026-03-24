# Plan V3: Citeck Launcher — Full Rewrite in Go + React

## Vision

Single binary (`citeck`, ~30MB) that is both CLI and daemon. No JRE. Web UI embedded inside. Users download one file, run `citeck install`, done.

```
$ curl -fsSL https://get.citeck.ru | sh     # downloads single binary
$ citeck install --from-config namespace.yml  # configures + starts
$ citeck status                               # shows status
# open http://localhost:8088                  # web dashboard
```

## Why Rewrite

| Aspect | Kotlin (current) | Go (target) |
|--------|------------------|-------------|
| Binary size | ~44MB JAR + ~50MB JRE = ~94MB | ~30MB single binary |
| Install | Download archive, extract JRE, link script | Download binary, chmod +x |
| Startup time (CLI) | ~1.5s (JVM cold start) | ~50ms |
| Daemon memory | ~500MB (JVM heap) | ~50MB |
| Cross-compilation | jlink per platform | `GOOS=linux GOARCH=arm64 go build` |
| Agent familiarity | Good | Excellent (kubectl, docker, terraform — all Go) |
| UI testability | Compose Desktop — opaque | React + Playwright — full visibility |
| Dependencies | Gradle + JDK 25 + many JARs | Go modules (self-contained) |

## Architecture

### Three distributions, one codebase

```
1. citeck (Go binary)              — daemon + CLI for servers (Linux/macOS/Windows)
2. Citeck Desktop (Tauri app)      — Lens-like desktop client (Windows/macOS/Linux)
3. Web UI (browser)                — opens http://localhost:8088 or remote URL
```

### Lens-inspired model

Like Lens manages multiple Kubernetes clusters, Citeck Desktop manages multiple Citeck instances:

```
┌─ Citeck Desktop (Tauri) ──────────────────────────────────────┐
│                                                                │
│  Connections:                                                  │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐          │
│  │ Local         │ │ Production   │ │ Staging      │          │
│  │ localhost:8088│ │ prod.co:8088 │ │ stg.co:8088  │          │
│  │ ● RUNNING     │ │ ● RUNNING    │ │ ○ STOPPED    │          │
│  └──────────────┘ └──────────────┘ └──────────────┘          │
│                                                                │
│  ┌─ prod.co ──────────────────────────────────────────────┐   │
│  │ Dashboard    Apps    Logs    Config    Diagnose         │   │
│  │                                                         │   │
│  │ Status: RUNNING    Bundle: community:2025.12            │   │
│  │                                                         │   │
│  │ APP          STATUS    CPU    MEM     IMAGE             │   │
│  │ proxy        RUNNING   0.1%   32M     ecos-proxy:2.25  │   │
│  │ gateway      RUNNING   0.6%   533M    ecos-gateway:3.3 │   │
│  │ emodel       RUNNING   2.2%   946M    ecos-model:2.35  │   │
│  │ ...                                                     │   │
│  └─────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────┘
```

### Platform matrix

| Component | Linux x64 | Linux arm64 | macOS x64 | macOS arm64 | Windows x64 |
|-----------|-----------|-------------|-----------|-------------|-------------|
| **citeck** (daemon+CLI) | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Citeck Desktop** (Tauri) | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Web UI** (browser) | ✅ | ✅ | ✅ | ✅ | ✅ |

Go cross-compiles natively for all targets. Tauri builds via GitHub Actions for each platform.

**Local mode:** On any OS, `citeck start` launches the daemon locally. Docker Desktop (macOS/Windows) or Docker Engine (Linux) provides containers. The Web UI at `localhost:8088` manages the local instance. Desktop app connects to `localhost:8088`.

**Remote mode:** Desktop app connects to a remote daemon (`prod.example.com:8088`). Same UI, different target.

### Component architecture

```
citeck (single Go binary — daemon + CLI + embedded Web UI)
├── CLI mode:     citeck status, citeck apply, ...  (cobra commands)
├── Daemon mode:  citeck start --foreground         (HTTP/WS server)
│   ├── REST API      /api/v1/*
│   ├── WebSocket     /api/v1/events, /api/v1/apps/{name}/logs/stream
│   └── Web UI        /* (embedded React SPA)
└── Hybrid:       citeck start                      (fork daemon, then CLI)

Citeck Desktop (Tauri — thin native shell)
├── WebView → loads http://localhost:8088 or remote URL
├── Connection manager (add/edit/remove servers)
├── System tray icon (quick status, start/stop)
├── Native notifications (app failed, cert expiring)
└── Auto-start on login
```

### Project Structure

```
citeck-launcher/
├── cmd/citeck/main.go            # Entry point
├── internal/
│   ├── cli/                      # Cobra commands
│   │   ├── root.go               # Global flags: -o json, --host, --token
│   │   ├── start.go
│   │   ├── stop.go
│   │   ├── status.go
│   │   ├── apply.go
│   │   ├── describe.go
│   │   ├── logs.go
│   │   ├── exec.go
│   │   ├── wait.go
│   │   ├── health.go
│   │   ├── diagnose.go
│   │   ├── install.go
│   │   ├── version.go
│   │   └── config.go             # config view, config validate
│   ├── daemon/
│   │   ├── server.go             # HTTP server (chi router)
│   │   ├── routes_namespace.go   # Namespace API handlers
│   │   ├── routes_apps.go        # App API handlers (logs, restart, exec, describe)
│   │   ├── routes_health.go      # Health check
│   │   ├── routes_events.go      # WebSocket event stream
│   │   └── middleware.go         # Token auth, logging, CORS
│   ├── namespace/
│   │   ├── config.go             # NamespaceConfig (YAML parsing)
│   │   ├── generator.go          # Container definitions from config
│   │   ├── context.go            # NsGenContext (proxyBaseUrl, etc.)
│   │   ├── runtime.go            # Namespace state machine
│   │   ├── app_runtime.go        # Per-app state machine
│   │   ├── reconciler.go         # Reconciliation loop
│   │   └── diff.go               # Config diff computation
│   ├── docker/
│   │   ├── client.go             # Docker client wrapper
│   │   ├── containers.go         # Container lifecycle (create, start, stop, remove)
│   │   ├── images.go             # Image pull
│   │   ├── probes.go             # Startup + liveness probes
│   │   ├── exec.go               # Exec in container
│   │   ├── logs.go               # Container logs
│   │   └── stats.go              # Container stats
│   ├── bundle/
│   │   ├── bundle.go             # BundleDef, BundleRef
│   │   └── resolver.go           # Bundle resolution from git repos
│   ├── git/
│   │   └── repo.go               # Git clone/pull (go-git)
│   ├── config/
│   │   ├── paths.go              # /opt/citeck paths + system property overrides
│   │   ├── daemon_config.go      # daemon.yml (TCP, reconciliation, etc.)
│   │   └── workspace.go          # Workspace config loading
│   ├── output/
│   │   ├── formatter.go          # OutputFormat interface (text/json)
│   │   ├── table.go              # ASCII table renderer
│   │   ├── color.go              # ANSI color helpers
│   │   └── progress.go           # Progress bars (stderr)
│   ├── client/
│   │   ├── client.go             # DaemonClient (HTTP to daemon)
│   │   └── transport.go          # Unix socket + TCP transport
│   ├── history/
│   │   └── operations.go         # Operation history (JSONL file)
│   └── appfiles/                 # Embedded resource files
│       └── embed.go              # go:embed for proxy/keycloak/postgres configs
├── web/                          # React SPA (separate npm project)
│   ├── src/
│   │   ├── App.tsx
│   │   ├── pages/
│   │   │   ├── Dashboard.tsx     # Namespace status, app list
│   │   │   ├── AppDetail.tsx     # describe-like view
│   │   │   ├── Logs.tsx          # Real-time log viewer
│   │   │   └── Settings.tsx      # Config view/edit
│   │   ├── components/
│   │   │   ├── AppStatusCard.tsx
│   │   │   ├── StatusBadge.tsx
│   │   │   ├── LogViewer.tsx
│   │   │   └── ResourceChart.tsx
│   │   └── lib/
│   │       ├── api.ts            # API client (fetch wrapper)
│   │       └── websocket.ts      # WebSocket event stream
│   ├── package.json
│   ├── vite.config.ts
│   ├── playwright.config.ts      # E2E test config
│   └── tests/
│       ├── dashboard.spec.ts     # Playwright E2E
│       ├── logs.spec.ts
│       └── login.spec.ts
├── desktop/                      # Tauri desktop app
│   ├── src-tauri/
│   │   ├── Cargo.toml
│   │   ├── src/main.rs           # Tauri entry point
│   │   ├── tauri.conf.json       # Window config, tray, permissions
│   │   └── icons/                # App icons per platform
│   ├── src/                      # Desktop-specific UI (shares web/ components)
│   │   ├── App.tsx               # Wraps web UI + connection manager
│   │   ├── ConnectionManager.tsx # Add/edit/remove servers (like Lens cluster list)
│   │   └── TrayMenu.tsx          # System tray context
│   ├── package.json
│   └── vite.config.ts
├── go.mod
├── go.sum
├── Makefile                      # build, test, lint, dist
├── .goreleaser.yml               # Go binary multi-platform release
├── .github/workflows/
│   ├── release-cli.yml           # Go binary release (goreleaser)
│   └── release-desktop.yml       # Tauri app release (tauri-action)
└── AGENT_PLAN_V3.md
```

### Key Go Libraries

| Purpose | Library | Why |
|---------|---------|-----|
| CLI | `github.com/spf13/cobra` | Standard (kubectl, docker, helm use it) |
| Config | `github.com/spf13/viper` | YAML/env/flags unified config |
| HTTP server | `net/http` + `github.com/go-chi/chi` | Lightweight, stdlib-compatible |
| WebSocket | `github.com/gorilla/websocket` | Standard, battle-tested |
| Docker | `github.com/docker/docker/client` | Official Docker SDK |
| Git | `github.com/go-git/go-git/v5` | Pure Go, no libgit2 |
| YAML | `gopkg.in/yaml.v3` | Standard YAML parser |
| JSON | `encoding/json` (stdlib) | No external deps |
| Testing | `testing` + `github.com/stretchr/testify` | Standard + assertions |
| Logging | `log/slog` (stdlib) | Structured logging, Go 1.21+ |
| Embed | `embed` (stdlib) | Embed web UI + appfiles into binary |
| Color output | `github.com/fatih/color` | ANSI colors for CLI |
| Table output | `github.com/olekukonenko/tablewriter` | ASCII tables |
| Unix socket | `net` (stdlib) | Unix domain socket listener |

### Web UI Stack

| Purpose | Library | Why |
|---------|---------|-----|
| Framework | React 19 + TypeScript | Maximum agent training data |
| Build | Vite | Fast, simple |
| Components | shadcn/ui + Radix | Agent-friendly, accessible |
| Styles | Tailwind CSS 4 | Utility-first, agents generate well |
| State | Zustand | Simple, TypeScript-first |
| API client | ky or native fetch | Lightweight |
| WebSocket | Native WebSocket API | No deps needed |
| Testing | Vitest + Testing Library | Component tests |
| E2E | Playwright | Browser automation, agent MCP |
| Storybook | Storybook 8 | Component isolation |

---

## Build & Development

```makefile
# Makefile
build:                          # Build Go binary + embed web UI
	cd web && npm run build
	go build -o citeck ./cmd/citeck

build-fast:                     # Go only (skip web rebuild)
	go build -o citeck ./cmd/citeck

test:                           # All tests
	go test ./...
	cd web && npm test

test-unit:                      # Go unit tests only
	go test ./internal/...

test-integration:               # Start daemon + Docker tests
	go test -tags=integration ./tests/...

test-e2e:                       # Playwright browser tests
	cd web && npx playwright test

lint:
	golangci-lint run
	cd web && npm run lint

dev-daemon:                     # Run daemon with web hot-reload
	go run ./cmd/citeck start --foreground &
	cd web && npm run dev -- --proxy http://localhost:8088

dist:                           # Cross-platform build
	goreleaser release --snapshot --clean
```

### Agent development workflow

```bash
# Go changes
vim internal/cli/status.go       # edit
go build ./cmd/citeck            # compile (~2s)
go test ./internal/cli/...       # unit test (~1s)
./citeck status -o json          # verify

# Web UI changes
vim web/src/pages/Dashboard.tsx  # edit
# Vite HMR updates browser instantly
cd web && npx vitest run         # component tests (~2s)
cd web && npx playwright test    # E2E tests (~10s)

# Full cycle
make test                        # all tests (~30s)
```

---

## Implementation Phases

### Phase 1: Go Project Scaffold + CLI Skeleton

**Goal:** Buildable Go binary with basic commands that talk to the existing Kotlin daemon.

**Tasks:**
1. Initialize Go module (`go mod init github.com/niceteck/citeck-launcher`)
2. Set up cobra CLI with global flags (`-o json`, `--host`, `--token`, `--yes`)
3. Implement `DaemonClient` (Unix socket + TCP transport)
4. Port commands: `version`, `status`, `health`, `config view`, `config validate`
5. Implement output formatter (text/json dual output)
6. Implement exit codes

**Tests:**
- Unit: OutputFormatter (text/json), ExitCodes, DaemonClient transport detection
- Integration: Go CLI → Kotlin daemon (verify API compatibility)

**Verification:** Go `citeck status -o json` returns same data as Kotlin version.

### Phase 2: Web UI Scaffold

**Goal:** React app served by Go daemon, testable with Playwright.

**Tasks:**
1. Initialize React + Vite + TypeScript project in `web/`
2. Set up shadcn/ui + Tailwind
3. Create API client (`web/src/lib/api.ts`)
4. Create WebSocket client for events
5. Build minimal Dashboard page (namespace status + app list table)
6. Set up Playwright + Storybook
7. Embed built web UI into Go binary via `go:embed`
8. Add static file serving route to daemon

**Tests:**
- Component: Vitest + Testing Library for Dashboard, AppStatusCard
- Storybook: Visual stories for each component
- E2E: Playwright navigates to dashboard, verifies app list renders
- Visual: Playwright screenshot → baseline

**Verification:** `citeck start --foreground` → open `http://localhost:8088` → dashboard shows apps.

### Phase 3: Port Daemon Core

**Goal:** Go daemon replaces Kotlin daemon. Full namespace lifecycle.

**Tasks:**
1. Port `NamespaceConfig` (YAML parsing with viper/yaml.v3)
2. Port `NsGenContext` + `NamespaceGenerator` (container definitions)
3. Port `DockerApi` (using official Docker SDK)
4. Port `AppRuntime` state machine + probes
5. Port `NamespaceRuntime` (background thread → goroutine, command queue → channel)
6. Port `BundlesService` + `GitRepoService`
7. Port `NamespaceConfigManager` (config loading, bundle resolution)
8. Implement daemon lifecycle (lock file, shutdown hooks, signal handling)
9. Port all API routes (namespace, apps, events, health)
10. Port appfiles embedding (keycloak, postgres, proxy configs)

**Tests:**
- Unit: NamespaceConfig parsing, NsGenContext.proxyBaseUrl, container name generation
- Unit: State machine transitions (AppRuntimeStatus)
- Integration: Start namespace with Config 1 (BASIC+HTTP), verify all apps RUNNING
- Integration: All 5 test configs pass (regression suite)
- E2E: Playwright dashboard shows RUNNING apps

**Verification:** Delete Kotlin daemon. Go binary handles everything. Same 5 configs pass.

### Phase 4: Full CLI + Apply + Diff

**Goal:** All CLI commands ported + new K8s-style commands.

**Tasks:**
1. Port remaining commands: `logs`, `exec`, `restart`, `describe`, `install`, `uninstall`, `reload`, `stop`
2. Implement `citeck apply -f namespace.yml` (idempotent, diff-based)
3. Implement `citeck diff` (show pending changes)
4. Implement `citeck wait` (atomic condition waiting)
5. Implement `--dry-run` on all mutating commands
6. Implement `--yes` for confirmation skipping
7. Implement `citeck diagnose` (with `--fix`)
8. Implement non-interactive install (`--from-config`, `--non-interactive`)

**Tests:**
- Unit: ConfigDiff, ApplyPlanner, WaitCondition, DiagnoseChecks
- Integration: `apply` idempotency (run 3 times, no unnecessary restarts)
- Integration: `apply --dry-run` shows changes without applying
- Integration: `diagnose --fix` fixes stale socket/container
- E2E: Full agent workflow (install → apply → wait → health → describe)

### Phase 5: Web UI — Full Dashboard

**Goal:** Production-ready web dashboard with real-time updates.

**Tasks:**
1. Dashboard page: namespace status, app cards with CPU/memory, health indicator
2. App detail page: describe-like view (events, timeline, env, ports, volumes)
3. Logs page: real-time log streaming via WebSocket, search, filter
4. Config page: view current config, validate, edit + apply
5. Install/setup wizard: web-based alternative to CLI install
6. Real-time updates: WebSocket pushes status changes to UI
7. Dark/light theme
8. Responsive layout (mobile-friendly)

**Tests:**
- Component: Vitest for every component
- Storybook: Stories for every component + page
- E2E Playwright: Navigate dashboard, view app detail, stream logs, change config
- Visual regression: Screenshot baselines for all pages
- Accessibility: Playwright accessibility snapshot for all pages

### Phase 6: Liveness Probes + Self-Healing

**Goal:** Daemon detects and fixes problems automatically.

**Tasks:**
1. Implement liveness probes (periodic health checks on RUNNING apps)
2. Auto-restart on liveness failure
3. Startup probe categorization (container exited → immediate failure, OOM → report)
4. Reconciliation loop (desired vs actual state)
5. Graceful shutdown ordering (proxy → apps → infra)
6. Operation history logging (JSONL)

**Tests:**
- Unit: Probe categorization logic, reconciliation diff
- Integration: Kill container → liveness detects → auto-restart
- Integration: `docker rm` container → reconciler recreates
- E2E: Web UI shows restart event in real-time

### Phase 7: Remote Daemon + Auth

**Goal:** Daemon accessible over network with TLS + token auth.

**Tasks:**
1. TCP/TLS listener alongside Unix socket
2. Token auth middleware (required for TCP, skip for Unix socket)
3. `daemon.yml` configuration for TCP/TLS/auth
4. CLI `--host` + `--token` flags, `CITECK_HOST`/`CITECK_TOKEN` env vars
5. Web UI login page (token entry for remote connections)
6. `citeck token generate/show` commands

**Tests:**
- Unit: Auth middleware, transport detection
- Integration: Connect via TCP with token, verify all APIs work
- Integration: Connect without token → 401
- E2E: Open remote URL in Playwright → login → dashboard

### Phase 8: Advanced Features

**Goal:** Rolling updates, backup, cert management, cleanup.

**Tasks:**
1. `citeck update --strategy rolling` (per-app update with rollback)
2. `citeck rollback` (restore previous config from history)
3. `citeck backup` / `citeck restore`
4. `citeck cert status/renew/generate`
5. `citeck preflight` (resource check before deploy)
6. `citeck clean` (orphaned containers/volumes)
7. `citeck cp` (copy files to/from container)
8. `citeck top` (resource usage)
9. `citeck events` / `citeck history`
10. Log filtering (`--errors-only`, `--search`, `--since`)

### Phase 9: Citeck Desktop (Tauri — Lens-like client)

**Goal:** Cross-platform desktop app for managing local and remote instances.

**Tasks:**
1. Initialize Tauri project in `desktop/`
2. Connection manager: add/edit/remove servers (local + remote)
3. Connection list UI (like Lens cluster sidebar)
4. Auto-detect local daemon (localhost:8088)
5. System tray icon with quick status + start/stop
6. Native notifications (app failed, cert expiring, update available)
7. Auto-start on login (optional)
8. Embed same React components from `web/` (shared component library)
9. Package: DMG (macOS), MSI (Windows), AppImage/deb (Linux)
10. GitHub Actions: tauri-action for all 5 targets (linux x64/arm64, macos x64/arm64, windows x64)

**Tests:**
- Component: Shared components tested via web/ Vitest
- E2E: Playwright connects to Tauri WebView (tauri-driver)
- Manual: Install on each platform, verify tray icon + connection

### Phase 10: Distribution + Polish

**Goal:** Production-ready releases for all platforms.

**Tasks:**
1. goreleaser config (Linux/macOS/Windows, amd64/arm64)
2. Install script (`curl | sh` for Linux/macOS, PowerShell for Windows)
3. Systemd service template (Linux)
4. launchd plist template (macOS)
5. Windows Service support (via `citeck service install`)
6. Shell completion (bash, zsh, fish, PowerShell)
7. `--help` improvements, man pages
8. Audit logging
9. Secret management (`citeck secret set/list`)

---

## Porting Guide: Kotlin → Go

Reference implementation: `/home/spk/IdeaProjects/citeck-launcher2/`

### Pattern Mapping

| Kotlin | Go |
|--------|-----|
| `data class Dto(val x: String = "")` | `type Dto struct { X string \`json:"x"\` }` |
| `sealed class Result` | `type Result struct { Data T; Err error }` or multiple return |
| `MutProp<T>` (reactive) | Channel + goroutine, or `sync.Mutex` + callback |
| `companion object { val DEFAULT }` | Package-level `var Default = Dto{}` |
| `Clikt command` | `cobra.Command` |
| `Ktor route` | `chi.Router.Get/Post` |
| `Jackson @JsonDeserialize` | `yaml.v3` / `encoding/json` struct tags |
| `kotlin.test + assertj` | `testing.T` + `testify/assert` |
| `coroutines` | goroutines + channels |
| `AutoCloseable` | `io.Closer` or `defer` |
| `lazy { }` | `sync.Once` |
| `Thread.sleep` | `time.Sleep` or `time.After` |

### Key Files to Port (in order)

| Kotlin source | Go target | Priority |
|--------------|-----------|----------|
| `core/namespace/NamespaceConfig.kt` | `internal/namespace/config.go` | Phase 3 |
| `core/namespace/gen/NsGenContext.kt` | `internal/namespace/context.go` | Phase 3 |
| `core/namespace/gen/NamespaceGenerator.kt` | `internal/namespace/generator.go` | Phase 3 |
| `core/namespace/runtime/NamespaceRuntime.kt` | `internal/namespace/runtime.go` | Phase 3 |
| `core/namespace/runtime/AppRuntime.kt` | `internal/namespace/app_runtime.go` | Phase 3 |
| `core/namespace/runtime/docker/DockerApi.kt` | `internal/docker/client.go` | Phase 3 |
| `core/namespace/runtime/actions/AppStartAction.kt` | `internal/docker/containers.go` + `probes.go` | Phase 3 |
| `core/bundle/BundlesService.kt` | `internal/bundle/resolver.go` | Phase 3 |
| `core/git/GitRepoService.kt` | `internal/git/repo.go` | Phase 3 |
| `cli/client/DaemonClient.kt` | `internal/client/client.go` | Phase 1 |
| `cli/daemon/server/DaemonServer.kt` | `internal/daemon/server.go` | Phase 3 |
| `cli/daemon/server/routes/*.kt` | `internal/daemon/routes_*.go` | Phase 3 |
| `cli/commands/*.kt` | `internal/cli/*.go` | Phase 1 + 4 |
| `cli/output/TableFormatter.kt` | `internal/output/table.go` | Phase 1 |

---

## Testing Strategy

### Per-phase

| Phase | Unit tests | Integration tests | E2E (Playwright) |
|-------|-----------|------------------|-----------------|
| 1 | OutputFormatter, ExitCodes, Client | Go CLI → Kotlin daemon | — |
| 2 | React components (Vitest) | — | Dashboard renders |
| 3 | Config parsing, proxyBaseUrl, state machine | 5 test configs pass | Dashboard shows RUNNING |
| 4 | ConfigDiff, ApplyPlanner, WaitCondition | apply idempotency, dry-run, diagnose | Full agent E2E |
| 5 | All React components | — | All pages, visual regression |
| 6 | Probe categorization, reconciliation | Kill container → auto-restart | UI shows events |
| 7 | Auth middleware, transport detection | TCP+token, Unix socket | Remote dashboard |
| 8 | Rolling update, backup format | Update + rollback cycle | — |

### Regression suite (after every phase from Phase 3)

5 test configs must pass:

| # | Auth | Host | TLS | Port |
|---|------|------|-----|------|
| 1 | BASIC | localhost | no | 80 |
| 2 | BASIC | localhost | self-signed | 443 |
| 3 | KEYCLOAK | custom.launcher.ru | self-signed | 443 |
| 4 | KEYCLOAK | localhost | no | 80 |
| 5 | BASIC | custom.launcher.ru | self-signed | 8443 |

### Agent full E2E test

```bash
#!/bin/bash
set -euo pipefail

# 1. Build
make build

# 2. Preflight
./citeck preflight --config testdata/config1.yml -o json | jq -e '.ok'

# 3. Apply
./citeck apply -f testdata/config1.yml --wait --timeout 600 -o json
[ $? -eq 0 ] || { ./citeck diagnose --fix --yes -o json; exit 1; }

# 4. Verify CLI
./citeck health -o json | jq -e '.healthy'
./citeck status -o json | jq -e '.status == "RUNNING"'

# 5. Verify idempotency
./citeck apply -f testdata/config1.yml -o json | jq -e '.changes | length == 0'

# 6. Verify Web UI (Playwright)
cd web && npx playwright test dashboard.spec.ts

# 7. Verify describe
for APP in $(./citeck status -o json | jq -r '.apps[].name'); do
  ./citeck describe "$APP" -o json | jq -e '.status == "RUNNING"'
done

# 8. Stop
./citeck stop --yes
./citeck wait --status stopped --timeout 60

echo "PASS"
```

### Visual regression test (agent runs after UI changes)

```bash
cd web
npx playwright test --update-snapshots   # generate baselines (first time)
npx playwright test                       # compare with baselines
# If diff detected → agent examines screenshot diff → decides fix or update baseline
```

---

## Migration Plan

Since CLI is not in production, this is a clean start — not a migration.

1. **Create new Go repo** (or new branch in current repo)
2. **Phase 1-2:** Go CLI + Web UI scaffold. Kotlin daemon still runs.
3. **Phase 3:** Port daemon. Kotlin daemon removed.
4. **Phase 4+:** Pure Go + React. Kotlin code is reference-only.
5. **Final:** Remove Kotlin `core/`, `cli/`, `app/` modules from repo.

The Kotlin code at `/home/spk/IdeaProjects/citeck-launcher2/` stays available as reference. Agent reads it to understand logic, writes Go equivalent.

---

## K8s Familiarity (same as V2)

| kubectl | citeck | Notes |
|---------|--------|-------|
| `kubectl apply -f` | `citeck apply -f` | Declarative, idempotent |
| `kubectl get pods` | `citeck status --apps` | List resources |
| `kubectl describe pod` | `citeck describe <app>` | Rich detail |
| `kubectl logs` | `citeck logs <app>` | Container logs |
| `kubectl exec -- cmd` | `citeck exec <app> -- cmd` | Exec in container |
| `kubectl top pods` | `citeck top` | Resource usage |
| `kubectl diff -f` | `citeck diff -f` | Preview changes |
| `kubectl rollout undo` | `citeck rollback` | Undo change |
| `kubectl cp` | `citeck cp` | Copy files |
| `kubectl get events` | `citeck events` | Event stream |
| `-o json` | `-o json` | Machine output |
| `--dry-run=client` | `--dry-run` | Preview |

---

## Human + Agent UX (same as V2)

- Text output by default (colored tables, progress bars in stderr)
- `-o json` for machine parsing (clean JSON to stdout)
- `--yes` skips confirmations
- `--dry-run` previews changes
- Errors include suggestions: "Run `citeck diagnose` to investigate"
- Web UI for visual management (humans + Playwright for agents)
