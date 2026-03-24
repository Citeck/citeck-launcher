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

### Detailed test cases per phase

**Phase 1 — Go unit tests** (`internal/*_test.go`):
| Test file | Cases |
|-----------|-------|
| `output/formatter_test.go` | Text renders table; JSON produces valid JSON; JSON has no ANSI; empty data → empty object |
| `client/client_test.go` | Unix socket detected when file exists; TCP used when `--host` set; TCP via `CITECK_HOST` env; error when neither available |
| `cli/exitcodes_test.go` | Each code has correct int; all codes unique |

**Phase 3 — Go unit tests:**
| Test file | Cases |
|-----------|-------|
| `namespace/config_test.go` | YAML parse BASIC; YAML parse KEYCLOAK+TLS; default values; builder round-trip |
| `namespace/context_test.go` | proxyBaseUrl HTTP:80; HTTPS:443; HTTP:8080; HTTPS:8443; HTTP:443; HTTPS:80; blank host → localhost |
| `namespace/generator_test.go` | BASIC auth env vars; KEYCLOAK proxy env vars; TLS exec probe uses curl; lua mount to /tmp |
| `docker/probes_test.go` | Container running → retry; container exited → immediate fail; OOMKilled → report OOM; restart loop detected |
| `namespace/runtime_test.go` | State transitions: STOPPED→STARTING→RUNNING; STARTING→STALLED on failure |

**Phase 4 — Go unit tests:**
| Test file | Cases |
|-----------|-------|
| `namespace/diff_test.go` | Same config → empty; changed port → port change; auth type change → full regenerate; new app → add; removed app → remove |
| `namespace/apply_test.go` | No changes → no-op; env change → restart affected; image change → pull+restart; `--force` → restart all |
| `cli/wait_test.go` | Parse `--status running`; parse `--app X`; parse `--healthy`; invalid status → error |
| `cli/diagnose_test.go` | Each check produces correct result; fixable vs non-fixable; `--dry-run` doesn't execute |
| `cli/install_test.go` | Flags override env; env override defaults; missing required → error; `--from-config` reads YAML |

**Phase 7 — Go unit tests:**
| Test file | Cases |
|-----------|-------|
| `daemon/middleware_test.go` | No token on TCP → 401; valid token → 200; invalid token → 401; Unix socket → skip auth |

### Failure injection matrix

| Scenario | How to inject | Expected behavior |
|----------|--------------|-------------------|
| Bad YAML | Write `{{{invalid` to namespace.yml | `apply` exit code 2, error shows line number |
| Missing cert | Set certPath to nonexistent file | `apply` exit code 2, suggests checking cert |
| Container crash | `docker kill citeck_proxy_*` | Liveness probe detects, auto-restart within 60s |
| OOM kill | Set memory limit to 10m for webapp | Probe reports OOM, suggests increasing memory |
| Docker down | `systemctl stop docker` | `health` exit code 6, diagnose reports unavailable |
| Disk full | Fill disk to 100% | `preflight` warns, `health` shows disk failed |
| Port conflict | Start service on port 80 | `preflight` detects, `diagnose` reports process |
| Stale lock | Leave `app.lock` from dead process | `diagnose --fix` deletes it |
| Orphaned container | `docker stop` without using citeck | Reconciler recreates from config |

### Playwright browser tests (Web UI)

**Smoke test (all configs):**
1. Navigate to dashboard URL
2. Verify JS/CSS load (HTTP 200)
3. Check console for errors (ignore chrome-extension)
4. Take screenshot, compare with baseline

**BASIC auth (configs 1, 2, 5):**
1. Set HTTP credentials via Playwright context
2. Navigate → dashboard renders with app list

**Keycloak auth (config 4):**
1. Navigate → redirect to Keycloak login
2. Fill username=admin, password=admin, submit
3. Handle password update if prompted
4. Verify redirect back to dashboard

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

## Complete CLI Command Reference

All commands the Go binary must support (final state after all phases):

### Namespace lifecycle
| Command | Phase | Description |
|---------|-------|-------------|
| `citeck install` | 4 | Interactive wizard OR `--non-interactive` / `--from-config` |
| `citeck uninstall` | 4 | Remove config, optionally data (`--yes` for no prompt) |
| `citeck start` | 3 | Start daemon (background or `--foreground`) + namespace |
| `citeck stop` | 3 | Stop namespace, `--shutdown` also stops daemon |
| `citeck apply -f ns.yml` | 4 | Idempotent desired-state (core command). `--wait`, `--force`, `--dry-run`, `--rollback-on-failure` |
| `citeck reload` | 3 | Hot-reload config. `--dry-run` validates without applying |
| `citeck diff` | 4 | Show pending changes. `-f new.yml` to compare with file |

### App management
| Command | Phase | Description |
|---------|-------|-------------|
| `citeck status --apps` | 1 | Show namespace + app table |
| `citeck describe <app>` | 1 | Rich detail: events, timeline, conditions, env, ports |
| `citeck logs <app>` | 1 | Container logs. `--tail N`, `--follow`, `--errors-only`, `--search`, `--since` |
| `citeck exec <app> -- cmd` | 1 | Execute command in container |
| `citeck restart <app>` | 1 | Stop + recreate app |
| `citeck top` | 8 | Resource usage. `--sort memory`, `--watch` |

### Operations
| Command | Phase | Description |
|---------|-------|-------------|
| `citeck health` | 1 | System health check |
| `citeck diagnose` | 4 | Find problems. `--fix` auto-repair, `--fix --dry-run` preview, `--yes` skip confirmation |
| `citeck wait` | 4 | Block until condition. `--status running`, `--app X --status running`, `--healthy`, `--timeout` |
| `citeck update` | 8 | Update bundle/images. `--strategy rolling`, `--dry-run`, `--app X --image Y` |
| `citeck rollback` | 8 | Restore previous config. `--to <version>` |
| `citeck backup` | 8 | Backup config + volumes. `--include-volumes` |
| `citeck restore` | 8 | Restore from backup. `--dry-run`, `--yes` |
| `citeck preflight` | 8 | Pre-deploy resource check. `--config ns.yml` |
| `citeck clean` | 8 | Cleanup orphaned resources. `--execute`, `--volumes`, `--images`, `--yes` |
| `citeck cp <app>:/path ./local` | 8 | Copy files to/from container |

### Configuration
| Command | Phase | Description |
|---------|-------|-------------|
| `citeck config view` | 1 | Display current namespace.yml |
| `citeck config validate` | 1 | Validate YAML, certs, ports |
| `citeck version` | 1 | Version, build time, OS |

### Events & history
| Command | Phase | Description |
|---------|-------|-------------|
| `citeck events` | 8 | App state change events. `--since 1h` |
| `citeck history` | 8 | Operation log (start, stop, apply, restart). `--since 1d` |

### Security & certificates
| Command | Phase | Description |
|---------|-------|-------------|
| `citeck cert status` | 8 | Show cert expiration, issuer, SANs |
| `citeck cert check --warn-days 30` | 8 | Exit code 1 if expiring soon |
| `citeck cert renew` | 8 | Renew (Let's Encrypt or regenerate self-signed) |
| `citeck cert generate --host X` | 8 | Generate new self-signed cert |
| `citeck token generate` | 7 | Generate new daemon API token |
| `citeck token show` | 7 | Show current token (for copying to desktop app) |
| `citeck secret set <key> <val>` | 10 | Store encrypted secret locally |
| `citeck secret list` | 10 | List secret keys (not values) |

### Global flags (all commands)
| Flag | Description |
|------|-------------|
| `-o json` | Machine-readable JSON output to stdout |
| `--yes` | Skip confirmation prompts |
| `--dry-run` | Preview changes without applying |
| `--host <host:port>` | Connect to remote daemon |
| `--token <token>` | Auth token for remote connections |
| `--token-file <path>` | Read token from file |

Env var alternatives: `CITECK_HOST`, `CITECK_TOKEN`, `CITECK_HOME`, `CITECK_RUN`

### Exit codes
| Code | Constant | Meaning |
|------|----------|---------|
| 0 | OK | Success |
| 1 | ERROR | General error |
| 2 | CONFIG_ERROR | Invalid YAML, missing cert |
| 3 | DAEMON_NOT_RUNNING | Daemon not running |
| 4 | NOT_CONFIGURED | Namespace not configured |
| 5 | NOT_FOUND | App/resource not found |
| 6 | DOCKER_UNAVAILABLE | Docker unreachable |
| 7 | TIMEOUT | Operation timed out |
| 8 | UNHEALTHY | Health check failed |
| 9 | CONFLICT | Lock held / operation in progress |

---

## Human + Agent UX Guidelines

### Design principles

1. **Human-first defaults, agent-friendly options** — text by default, `-o json` for agents
2. **Every mutation has a preview** (`--dry-run`)
3. **Every operation is idempotent** (safe to retry)
4. **Every failure is actionable** — humans get suggestions, agents get error codes
5. **The system self-heals** (liveness probes, reconciliation)
6. **State is declarative** (`citeck apply`)
7. **Interactive and non-interactive paths coexist** — wizard for humans, flags for agents
8. **Dangerous operations require confirmation** — `[y/N]` for humans, `--yes` for agents
9. **API-first** — every feature is an API endpoint, CLI/GUI/Desktop are just clients

### Human vs Agent UX contract

| Aspect | Human (default) | Agent (`-o json` / `--yes`) |
|--------|----------------|---------------------------|
| Output format | Colored tables, readable messages | JSON to stdout |
| Progress | Progress bars in stderr | Suppressed |
| Errors | Message + suggestion + exit code | JSON `{error, code, suggestion}` + exit code |
| Confirmations | Interactive `[y/N]` prompt | `--yes` skips prompts |
| Mutations | `--dry-run` shows colored diff | `--dry-run -o json` shows structured changes |
| Install | Interactive wizard | `--non-interactive` / `--from-config` |
| Logs | Streamed to terminal | `--since 5m --errors-only -o json` |

**Rule:** stderr is for humans (progress, hints). stdout is for data (tables or JSON). Agents parse only stdout.

### Output conventions
- ANSI colors: green=RUNNING, red=FAILED, yellow=STARTING/WARNING
- Progress bars go to **stderr** (invisible to `jq`)
- JSON mode (`-o json`): no colors, no progress, clean JSON to stdout

### Status display (human)
```
Name:      Production (default)
Status:    RUNNING                                ← green
Bundle:    community:2025.12

APP          STATUS     IMAGE                     CPU    MEMORY
proxy        RUNNING    ecos-proxy:2.25.6         0.1%   32M/128M     ← green
gateway      RUNNING    ecos-gateway:3.3.0        0.6%   533M/1.0G    ← green
emodel       STARTING   ecos-model:2.35.7         --     --           ← yellow
postgres     FAILED     postgres:17.5             --     --           ← red
  └─ Exit code 1: configuration file contains errors                  ← hint
```

### Error messages (always include what/why/what-to-do)
```
Error: App 'proxy' failed to start
  Container exited with code 1 after 3.2s
  Last log: nginx: [emerg] cannot load certificate "/app/tls/server.crt"
  Suggestion: Check TLS certificate path in namespace.yml
              Run 'citeck config validate' to verify configuration
```

JSON equivalent:
```json
{"error": "start_failed", "code": 1, "app": "proxy",
 "message": "Container exited with code 1 after 3.2s",
 "lastLog": "nginx: [emerg] cannot load certificate ...",
 "suggestions": ["Check TLS certificate path", "Run citeck config validate"]}
```

### Confirmation prompts (destructive operations)
```
$ citeck clean --execute
Found 3 orphaned resources:
  Container  citeck_old_proxy   (stopped 3 days ago)
  Volume     citeck_old_data    (unused)
  Network    citeck_old_net     (no containers)

Remove these resources? [y/N]
```

With `--yes`: skip prompt, apply immediately.

### Progress display (stderr, human mode)
```
$ citeck apply -f ns.yml --wait
Applying configuration...
  Pulling images    [████████░░] 4/5
  Starting apps     [██████░░░░] 12/19
  Waiting for proxy [probe 3/10, 30s elapsed]
All 19 apps running. Took 2m 15s.
```

In JSON mode: only final result to stdout:
```json
{"status": "applied", "apps": 19, "running": 19, "duration": 135000}
```

---

## Implementation Details

### File paths
| Path | Purpose |
|------|---------|
| `/opt/citeck/conf/namespace.yml` | Namespace config |
| `/opt/citeck/conf/daemon.yml` | Daemon operational config (TCP, reconciliation) |
| `/opt/citeck/conf/daemon-token` | API token for TCP connections |
| `/opt/citeck/conf/tls/` | TLS certificates |
| `/opt/citeck/conf/history/` | Last N configs for rollback (default: 5) |
| `/opt/citeck/data/` | Persistent data (bundles, workspace, volumes, snapshots) |
| `/opt/citeck/log/daemon.log` | Daemon log |
| `/opt/citeck/log/operations.jsonl` | Operation history |
| `/opt/citeck/log/audit.jsonl` | Audit log (timestamp, command, source, result) |
| `/run/citeck/daemon.sock` | Unix socket (local connections) |
| `~/.citeck/launcher/connections.yml` | Saved remote connections (Desktop app) |

### daemon.yml structure
```yaml
server:
  tcp:
    enabled: false
    port: 8088
    host: "0.0.0.0"
    tls:
      certPath: "/opt/citeck/conf/tls/daemon.crt"
      keyPath: "/opt/citeck/conf/tls/daemon.key"
  auth:
    token: "generated-at-install-time"
reconciliation:
  enabled: true
  intervalSeconds: 60
```

### Operation history format (`operations.jsonl`)
```json
{"ts":"2026-03-24T12:00:00Z","op":"start","result":"ok","duration":180000,"apps":19}
{"ts":"2026-03-24T14:30:00Z","op":"restart","app":"proxy","result":"ok","duration":5000}
{"ts":"2026-03-24T15:00:00Z","op":"apply","result":"error","error":"invalid YAML at line 12"}
```

### Liveness probe defaults
| App type | Probe | Period | Failure threshold |
|----------|-------|--------|-------------------|
| Webapps (Spring) | `GET /management/health` | 30s | 3 |
| Gateway | `GET /management/health` | 30s | 3 |
| Proxy | `curl -sf http://localhost:80/eis.json` | 30s | 3 |
| Postgres | `psql -U postgres -c 'SELECT 1'` | 30s | 3 |
| Keycloak | `bash /healthcheck.sh` | 30s | 3 |

### Graceful shutdown ordering
1. Stop proxy (stop accepting traffic)
2. Stop webapps (drain in-flight requests, `terminationGracePeriodSeconds` default: 30s)
3. Stop Keycloak
4. Stop infrastructure (postgres, rabbitmq, zookeeper — last)

### Startup timeline tracking (per-app)
```json
{
  "pullStart": "2026-03-24T12:18:00Z", "pullEnd": "2026-03-24T12:18:02Z",
  "createStart": "2026-03-24T12:18:02Z", "createEnd": "2026-03-24T12:18:03Z",
  "initStart": "2026-03-24T12:18:03Z", "initEnd": "2026-03-24T12:18:08Z",
  "probeStart": "2026-03-24T12:18:08Z", "probeEnd": "2026-03-24T12:18:45Z",
  "runningAt": "2026-03-24T12:18:45Z",
  "totalMs": 45000, "probeAttempts": 4
}
```

### K8s intentional differences
- No namespaces within a launcher instance (one namespace per installation)
- `citeck install` instead of `helm install` (includes system setup)
- `citeck health` — simpler all-in-one check (no separate component statuses)
- `citeck diagnose --fix` — auto-remediation (no K8s equivalent)
- `citeck preflight` — pre-deploy resource validation (no K8s equivalent)
