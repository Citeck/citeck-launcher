# Agent Instructions: Citeck Launcher — Reference & Development Guide

## Mandate

You are a fully autonomous developer. **Do not ask questions** — investigate, decide, implement, test, fix, repeat. Commit at every key milestone (do NOT push).

### Work loop

```
while (tasks remain in current plan):
    1. Identify the highest-priority task from the current plan
    2. Investigate (read Kotlin reference code, check existing Go patterns)
    3. Implement in Go (or React for web UI)
    4. Build: make build (or make build-fast for Go only)
    5. Run unit tests: go test ./... && cd web && npx vitest run
    6. Integration test on real system (start daemon, check status, verify)
    7. If broken → go to step 2
    8. If working → commit, update PROGRESS.md, move to next task
```

### Plans

- **AGENT_PLAN_V3.md** — original rewrite plan (phases 1-10, all done except phase 9)
- **snoopy-herding-gosling.md** (in `~/.claude/plans/`) — current plan: Web UI feature parity
- **PROGRESS.md** — tracks completed work and next steps

---

## 1. Environment

| Item | Value |
|------|-------|
| Repo | `/home/spk/IdeaProjects/citeck-launcher2` |
| Branch | `release/1.4.0` |
| Build | `make build` → single `citeck` binary with embedded web UI |
| Build (Go only) | `make build-fast` → skip web rebuild |
| Run tests | `make test` (Go + Vitest) |
| Run Go tests | `go test ./...` |
| Run web tests | `cd web && npx vitest run` |
| Run E2E tests | `cd web && npx playwright test` |
| Lint Go | `golangci-lint run` |
| Lint web | `cd web && npm run lint` |
| Go version | 1.26.1 |
| Node | see `web/package.json` |
| Test host | `custom.launcher.ru` → `127.0.0.1` (in `/etc/hosts`) |
| Platform sources | `/home/spk/IdeaProjects/ecos-*` and `/home/spk/IdeaProjects/citeck-*` |

### How to run and test

```bash
# Build everything (Go + web UI)
make build

# Build Go only (faster iteration)
make build-fast

# Run daemon in foreground
./citeck start --foreground

# Open Web UI
# http://localhost:8088

# Run with custom dirs (no sudo)
CITECK_HOME=/tmp/citeck-test CITECK_RUN=/tmp/citeck-run ./citeck start --foreground

# Setup test dirs (first time)
mkdir -p /tmp/citeck-test/conf /tmp/citeck-test/data /tmp/citeck-test/log /tmp/citeck-run
```

### Key environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `CITECK_HOME` | `/opt/citeck` | Base directory for config, data, logs |
| `CITECK_RUN` | `/run/citeck` | Socket directory |
| `CITECK_HOST` | — | Remote daemon address (host:port) |
| `CITECK_TOKEN` | — | Auth token for remote connections |

---

## 2. Architecture

### Platform Architecture

```
┌─────────────────────────────────────────────────────────┐
│              citeck (single Go binary ~14MB)             │
│  net/http server                                         │
│  ├─ Unix socket  /run/citeck/daemon.sock  (local fast)   │
│  ├─ TCP          0.0.0.0:8088             (remote)       │
│  └─ Embedded React Web UI on /*                          │
│  ↕                                                       │
│  internal/ — namespace runtime, Docker API, config       │
└─────────────────────────────────────────────────────────┘
        ↑                    ↑
   Unix socket            TCP + token
        ↑                    ↑
┌──────────────┐  ┌──────────────────┐
│  CLI (local) │  │  Browser / Remote│
│  citeck cmd  │  │  Web UI at :8088 │
└──────────────┘  └──────────────────┘
```

### Project Structure

```
citeck-launcher/
├── cmd/citeck/main.go              # Entry point
├── internal/
│   ├── cli/                        # Cobra commands
│   ├── daemon/                     # HTTP server, routes, middleware
│   ├── namespace/                  # Config, generator, runtime, reconciler
│   ├── docker/                     # Docker SDK wrapper
│   ├── bundle/                     # Bundle resolution from git repos
│   ├── git/                        # Git clone/pull (os/exec)
│   ├── config/                     # Paths, daemon config, workspace
│   ├── output/                     # Text/JSON formatter, tables, colors
│   ├── client/                     # DaemonClient (Unix socket + TCP)
│   ├── api/                        # API types (DTOs)
│   ├── appdef/                     # Application definition models
│   ├── appfiles/                   # Embedded resource files (go:embed)
│   └── history/                    # Operation history (JSONL)
├── web/                            # React SPA (Vite + TypeScript + Tailwind)
│   ├── src/
│   │   ├── pages/                  # Dashboard, AppDetail, Logs, Config
│   │   ├── components/             # AppTable, StatusBadge
│   │   └── lib/                    # API client, WebSocket
│   ├── package.json
│   └── vite.config.ts
├── go.mod
├── go.sum
└── Makefile
```

### CLI Commands

| Command | What it does |
|---------|-------------|
| `citeck start` | Start daemon (background or `--foreground`) + namespace |
| `citeck stop` | Stop namespace, optionally daemon (`--shutdown`) |
| `citeck status` | Print status, `--watch` for live events, `--apps` for app table |
| `citeck reload` | Hot-reload namespace config |
| `citeck logs <app>` | Show container logs (`--tail N`, `--follow`) |
| `citeck restart <app>` | Restart a single app |
| `citeck describe <app>` | Show container details |
| `citeck exec <app> <cmd>` | Execute command in container |
| `citeck version` | Show version info |
| `citeck health` | System health check |
| `citeck config view` | Display namespace.yml |
| `citeck config validate` | Validate config (YAML, certs, ports) |
| `citeck apply -f ns.yml` | Idempotent desired-state apply (`--wait`, `--force`, `--dry-run`) |
| `citeck diff -f new.yml` | Show pending config changes |
| `citeck wait` | Block until condition (`--status`, `--app`, `--healthy`, `--timeout`) |
| `citeck diagnose` | Find problems (`--fix`, `--dry-run`) |
| `citeck cert status` | Show cert expiry, issuer, SANs |
| `citeck cert generate` | Generate self-signed ECDSA P256 cert |
| `citeck clean` | Orphaned resource cleanup (`--execute`, `--volumes`) |
| `citeck token generate` | Generate new daemon API token |
| `citeck token show` | Show current token |

All commands support `-o json` for machine-readable output.

### API Endpoints

| Method | Path | Response |
|--------|------|----------|
| GET | `/api/v1/daemon/status` | `DaemonStatusDto` |
| POST | `/api/v1/daemon/shutdown` | `ActionResultDto` |
| GET | `/api/v1/namespace` | `NamespaceDto` (apps inside) |
| POST | `/api/v1/namespace/start` | `ActionResultDto` |
| POST | `/api/v1/namespace/stop` | `ActionResultDto` |
| POST | `/api/v1/namespace/reload` | `ActionResultDto` |
| WS | `/api/v1/events` | Stream of `EventDto` |
| GET | `/api/v1/apps/{name}/logs?tail=N` | Plain text |
| POST | `/api/v1/apps/{name}/restart` | `ActionResultDto` |
| GET | `/api/v1/apps/{name}/inspect` | `AppInspectDto` |
| POST | `/api/v1/apps/{name}/exec` | `ExecResultDto` |
| GET | `/api/v1/health` | `HealthDto` |

### App Lifecycle State Machine

```
READY_TO_PULL → PULLING → READY_TO_START → DEPS_WAITING → STARTING → RUNNING
                PULL_FAILED             START_FAILED
                                        STOPPING_FAILED
```

Namespace status: `STOPPED → STARTING → RUNNING` (or `STALLED` if any app fails).

---

## 3. Critical Knowledge

### Keycloak + Proxy Integration

- Keycloak has NO context path — serves at `/realms/...` not `/auth/realms/...`
- Init action runs `sed` on nginx config to rewrite proxy_pass and add rewrite rule
- Then `nginx -s reload`
- `proxyBaseUrl` must include port for non-standard ports (affects Keycloak `--hostname` and redirect URIs)

### Proxy Container

- Based on OpenResty (nginx + Lua)
- Has `curl` but NOT `wget` — exec probes must use `curl`
- TLS mode: port 80 internal only, port 443 published → exec probe for startup
- Bind-mounted files: use init action `cp` from `/tmp` to avoid sed inode lock

### Kotlin Reference Code

The Kotlin code (in `core/`, `cli/`, `app/` directories) is the **reference implementation**. Read it to understand logic when porting or extending Go code. Key Kotlin paths:

| Kotlin source | Purpose |
|--------------|---------|
| `core/namespace/NamespaceConfig.kt` | Config model |
| `core/namespace/gen/NamespaceGenerator.kt` | Container definitions |
| `core/namespace/gen/NsGenContext.kt` | Generation context |
| `core/namespace/runtime/NamespaceRuntime.kt` | Namespace state machine |
| `core/namespace/runtime/AppRuntime.kt` | Per-app state machine |
| `core/namespace/runtime/docker/DockerApi.kt` | Docker client wrapper |
| `core/bundle/BundlesService.kt` | Bundle resolution |

---

## 4. Debugging Cheat Sheet

```bash
# Containers
docker ps --filter "label=citeck.launcher.namespace"
docker ps -a --filter "label=citeck.launcher.workspace=daemon"
docker logs citeck_proxy_default_daemon 2>&1 | tail -50

# Proxy internals
docker exec citeck_proxy_default_daemon cat /etc/nginx/conf.d/default.conf
docker exec citeck_proxy_default_daemon cat /usr/local/openresty/nginx/logs/error.log

# Network
ss -tlnp | grep -E ':80|:443|:8443'
curl -sk --noproxy '*' https://custom.launcher.ru/eis.json

# Daemon
ls -la /tmp/citeck-run/daemon.sock
cat /tmp/citeck-test/log/daemon.log | tail -50

# Clean slate
docker ps -a --filter "label=citeck.launcher.workspace=daemon" -q | xargs -r docker rm -f
rm -f /tmp/citeck-run/daemon.sock
```

---

## 5. Code Quality Rules

- Follow Go conventions (`gofmt`, `golangci-lint`)
- API routes follow REST conventions. Use proper HTTP status codes
- Error responses use `ErrorDto`. Success responses use specific DTOs
- All commands support `-o json` for machine-readable output
- Dangerous commands ask confirmation in text mode, `--yes` skips
- Never swallow errors silently. Log or return them
- Exit codes: 0 success, non-zero for specific errors (see AGENT_PLAN_V3.md)

---

## 6. Commit Discipline

- Commit at every meaningful milestone
- **NEVER push. NEVER amend. NEVER add Co-Authored-By.**
- Stage specific files, not `git add -A`
- Update PROGRESS.md after each phase completion

---

## 7. Context Management

### Preventive measures
- Commit early and often (save points for context overflow)
- Update PROGRESS.md after each milestone
- Use subagents for investigation (keeps main context clean)
- Don't read huge files fully (use offset/limit, Grep)

### If context overflows
1. `git log --oneline -20` — see all commits
2. `cat PROGRESS.md` — completed work and next steps
3. `cat AGENT_PLAN_V3.md` — the original plan
4. `git diff HEAD` — uncommitted work

---

## 8. Test Configurations (regression suite)

These 5 configs must pass after every major change:

| # | Auth | Host | TLS | Port | Key test |
|---|------|------|-----|------|----------|
| 1 | BASIC | localhost | no | 80 | Baseline |
| 2 | BASIC | localhost | self-signed | 443 | TLS exec probe |
| 3 | KEYCLOAK | custom.launcher.ru | self-signed | 443 | OIDC + custom host |
| 4 | KEYCLOAK | localhost | no | 80 | OIDC without TLS |
| 5 | BASIC | custom.launcher.ru | self-signed | 8443 | Non-standard port |

Self-signed cert:
```bash
openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt -days 365 -nodes \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,DNS:custom.launcher.ru,IP:127.0.0.1"
```

---

## 9. Web UI Development

### Development workflow

```bash
# Start daemon
./citeck start --foreground &

# Start Vite dev server with proxy to daemon
cd web && npm run dev -- --proxy http://localhost:8088

# Run component tests
cd web && npx vitest run

# Run E2E tests
cd web && npx playwright test
```

### Web UI stack

| Purpose | Library |
|---------|---------|
| Framework | React 19 + TypeScript |
| Build | Vite |
| Styles | Tailwind CSS 4 |
| State | Zustand |
| Testing | Vitest + Testing Library |
| E2E | Playwright |

### Development cycle (per feature)

```
1. IMPLEMENT → Write code (Go API + React UI)
2. BUILD     → make build
3. TEST      → go test + npx vitest run + npx playwright test
4. VERIFY    → Start daemon, open browser, test with Playwright MCP
5. REVIEW    → Code review: bugs, duplication, hardcoded values, security
6. FIX       → Fix ALL issues found in review
7. DEDUP     → Extract shared components/hooks/utils
8. COMMIT    → Only after steps 1-7 are clean
```
