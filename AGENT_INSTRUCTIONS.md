# Agent Instructions: Citeck Launcher — Reference & Development Guide

## Mandate

You are a fully autonomous developer. **Do not ask questions** — investigate, decide, implement, test, fix, repeat. Commit at every key milestone (do NOT push).

### Work loop

```
while (tasks remain in current phase):
    1. Identify the highest-priority task from AGENT_PLAN_V3.md
    2. Investigate (read Kotlin reference code, check existing patterns)
    3. Implement in Go (or React for web UI)
    4. Build: make build (or make build-fast for Go only)
    5. Run unit tests: go test ./... && cd web && npm test
    6. Integration test on real system (start namespace, check status, verify)
    7. If broken → go to step 2
    8. If working → commit, update PROGRESS.md, move to next task
```

### Plans

- **AGENT_PLAN_V3.md** — current plan: full rewrite in Go + React + Tauri (phases 1-10)
- **PROGRESS.md** — tracks completed work and next steps

---

## 1. Environment

| Item | Value |
|------|-------|
| Repo | `/home/spk/IdeaProjects/citeck-launcher2` |
| Branch | `release/1.4.0` |
| Build CLI | `./gradlew :cli:shadowJar` → `cli/build/libs/citeck-cli-1.4.0.jar` |
| Quick check | `./gradlew :core:classes` or `./gradlew :cli:classes` |
| Run tests | `./gradlew test` |
| Lint | `./gradlew ktlintFormat` (auto-fix) / `./gradlew ktlintCheck` |
| JDK | Java 25 at `~/.jdks/temurin-25.0.1+12/` |
| Test host | `custom.launcher.ru` → `127.0.0.1` (in `/etc/hosts`) |
| Platform sources | `/home/spk/IdeaProjects/ecos-*` and `/home/spk/IdeaProjects/citeck-*` |

### How to run and test the CLI

```bash
# Build the fat JAR
./gradlew :cli:shadowJar

# Run without sudo (custom home/run dirs)
java --enable-native-access=ALL-UNNAMED \
  -Dciteck.home=/tmp/citeck-test \
  -Dciteck.run=/tmp/citeck-run \
  -jar cli/build/libs/citeck-cli-1.4.0.jar <command>

# Setup test dirs (first time)
mkdir -p /tmp/citeck-test/conf /tmp/citeck-test/data /tmp/citeck-test/log /tmp/citeck-run
```

### Key system properties

| Property | Default | Purpose |
|----------|---------|---------|
| `citeck.home` | `/opt/citeck` | Base directory for config, data, logs |
| `citeck.run` | `/run/citeck` | Socket directory |

---

## 2. Architecture

### Platform Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Daemon (Linux server)                  │
│  Ktor CIO engine                                         │
│  ├─ Unix socket  /run/citeck/daemon.sock  (local fast)   │
│  └─ TCP/TLS      0.0.0.0:8088            (remote, P8)   │
│  ↕                                                       │
│  core/ — namespace runtime, Docker API, config, bundles  │
└─────────────────────────────────────────────────────────┘
        ↑                    ↑                    ↑
   Unix socket            TCP/TLS              TCP/TLS
        ↑                    ↑                    ↑
┌──────────────┐  ┌──────────────────┐  ┌─────────────────┐
│  CLI (Linux) │  │  GUI app (any OS)│  │  Agent (any)    │
│  citeck cmd  │  │  Compose Desktop │  │  -o json + API  │
└──────────────┘  └──────────────────┘  └─────────────────┘
```

### Modules

| Module | Scope | Cross-platform |
|--------|-------|---------------|
| **`core/`** | Business logic: Docker API, namespace gen, bundle resolution, config | Yes — all OS |
| **`app/`** | Compose Desktop GUI | Yes — Windows, macOS, Linux |
| **`cli/`** | CLI daemon + client, Linux deployment | Linux only |

**Key rule:** All business logic in `core/`. No OS-specific code in core. CLI is a thin Linux-specific wrapper. GUI and CLI are both API clients.

### CLI Commands (current)

| Command | What it does |
|---------|-------------|
| `citeck install` | Interactive wizard → namespace.yml + systemd service |
| `citeck uninstall` | Remove service, optionally data |
| `citeck start` | Start daemon (background or `--foreground`) + namespace |
| `citeck stop` | Stop namespace, optionally daemon (`--shutdown`) |
| `citeck status` | Print status, `--watch` for live events, `--apps` for app table |
| `citeck reload` | Hot-reload namespace config |
| `citeck logs <app>` | Show container logs (`--tail N`, `--follow`) |
| `citeck restart <app>` | Restart a single app |
| `citeck inspect <app>` | Show container details (rename to `describe` in V2 Phase 0) |
| `citeck exec <app> <cmd>` | Execute command in container |
| `citeck version` | Show version info |
| `citeck health` | System health check |
| `citeck config show` | Display namespace.yml (rename to `config view` in V2 Phase 0) |
| `citeck config validate` | Validate config (YAML, certs, ports) |

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

## 3. Key Files

All paths relative to repo root. Package: `ru.citeck.launcher`.

### CLI module (`cli/src/main/kotlin/ru/citeck/launcher/`)

| Path | Purpose |
|------|---------|
| `cli/CliMain.kt` | Entry point, Clikt command tree |
| `cli/commands/` | All CLI commands (StartCmd, StopCmd, StatusCmd, etc.) |
| `cli/client/DaemonClient.kt` | HTTP+WS client to daemon via Unix socket |
| `cli/daemon/DaemonLifecycle.kt` | Daemon startup/shutdown, background spawn |
| `cli/daemon/server/DaemonServer.kt` | Ktor Unix socket server |
| `cli/daemon/server/routes/` | API route handlers (Namespace, Daemon, Event, App, Health) |
| `cli/daemon/server/converters/NamespaceConverter.kt` | Runtime → DTO conversion |
| `cli/daemon/services/DaemonServices.kt` | Service container (Docker, Git, Bundles) |
| `cli/daemon/services/NamespaceConfigManager.kt` | Config loading, bundle resolution |
| `cli/daemon/storage/ConfigPaths.kt` | Filesystem path constants |
| `cli/output/` | TableFormatter, EventPrinter, (future: OutputFormatter) |

### API types (`cli/src/main/kotlin/ru/citeck/launcher/api/`)

| Path | Purpose |
|------|---------|
| `ApiPaths.kt` | Route path constants |
| `DaemonFiles.kt` | Socket/log file resolution |
| `dto/*.kt` | All DTOs (ActionResult, App, AppInspect, DaemonStatus, Error, Event, ExecRequest, ExecResult, Health, Namespace) |

### Core module (`core/src/main/kotlin/ru/citeck/launcher/core/`)

| Path | Purpose |
|------|---------|
| `namespace/NamespaceConfig.kt` | Config model (ProxyProps, TlsConfig, AuthenticationProps) |
| `namespace/gen/NamespaceGenerator.kt` | Container definitions from config |
| `namespace/gen/NsGenContext.kt` | Generation context (proxyBaseUrl, ports) |
| `namespace/runtime/NamespaceRuntime.kt` | Namespace state machine + background thread |
| `namespace/runtime/AppRuntime.kt` | Per-app state machine |
| `namespace/runtime/actions/AppStartAction.kt` | Container create + start + probe |
| `namespace/runtime/docker/DockerApi.kt` | Docker client wrapper |
| `bundle/BundleDef.kt` / `BundleRef.kt` / `BundlesService.kt` | Bundle resolution |
| `appdef/ApplicationDef.kt` / `AppProbeDef.kt` | Container definition models |
| `config/AppDir.kt` | OS-specific app directory (cross-platform) |
| `utils/file/CiteckJarFile.kt` | JAR resource reading (shadow JAR compatible) |

---

## 4. Critical Knowledge (from V1 development)

### setActive(true) Required

`NamespaceRuntime.updateAndStart()` only queues a command. The runtime thread is started by `setActive(true)`. Without it, nothing happens. GUI calls setActive via NamespacesService; CLI must call it explicitly.

### Shadow JAR Resource Loading

- `CiteckJarFile.getAllFiles()` must use `jarFile.getInputStream(entry)` — not construct nested URLs
- Shadow JAR returns file keys with `appfiles/` prefix — must be stripped in NamespaceGenerator

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

### AppLock

- File-based lock at `~/.citeck/launcher/app.lock`
- Shared between GUI and CLI — only one can run at a time
- Kill desktop launcher before starting CLI daemon

---

## 5. Debugging Cheat Sheet

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
rm -rf ~/.citeck/launcher/ws/daemon/ns/default/rtfiles/
rm -f ~/.citeck/launcher/app.lock /tmp/citeck-run/daemon.sock
rm -f /tmp/citeck-test/data/runtime.yml
```

---

## 6. Code Quality Rules

- Follow existing Kotlin style. Run `ktlintFormat` before every commit
- API routes follow REST conventions. Use proper HTTP status codes
- Error responses use `ErrorDto`. Success responses use specific DTOs
- CLI commands support `-o json` for machine-readable output (V2 Phase 1)
- Dangerous commands ask confirmation in text mode, `--yes` skips (V2 Phase 1)
- Never swallow exceptions silently. Log or return them
- Exit codes: 0 success, non-zero for specific errors (see V2 P1.3)
- No OS-specific code in `core/` module

---

## 7. Commit Discipline

- Commit at every meaningful milestone
- **NEVER push. NEVER amend. NEVER add Co-Authored-By.**
- Always run `./gradlew ktlintFormat` before committing
- Stage specific files, not `git add -A`
- Update PROGRESS.md after each phase completion

---

## 8. Context Management

### Preventive measures
- Commit early and often (save points for context overflow)
- Update PROGRESS.md after each milestone
- Use subagents for investigation (keeps main context clean)
- Don't read huge files fully (use offset/limit, Grep)

### If context overflows
1. `git log --oneline -20` — see all commits
2. `cat PROGRESS.md` — completed work and next steps
3. `cat AGENT_PLAN_V2.md` — the full plan
4. `git diff HEAD` — uncommitted work

---

## 9. Test Configurations (regression suite)

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
