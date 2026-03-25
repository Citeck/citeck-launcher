# Progress Log

## V1 — COMPLETE (2026-03-24)

Kotlin implementation done. 11 commits on `release/1.4.0`. All 5 test configs pass E2E.
Serves as **reference implementation** for Go rewrite.

## V3 — Plan: `AGENT_PLAN_V3.md`

Full rewrite: Go + React Web UI + Tauri Desktop.

### Phase 1: Go scaffold + CLI skeleton — COMPLETE (2026-03-25)
- [x] Go module init, cobra CLI, global flags (`-o json`, `--host`, `--token`, `--yes`)
- [x] DaemonClient (Unix socket + TCP transport)
- [x] Commands: version, status, health, config view, config validate, describe, logs, exec, restart
- [x] Output formatter (text/json), exit codes
- [x] Unit tests: 20 tests (formatter, exit codes, transport detection, uptime formatting)
- [x] Integration: Go CLI verified against live Kotlin daemon (status, health, describe, config)

### Phase 2: Web UI Scaffold — COMPLETE (2026-03-25)
- [x] React 19 + Vite + TypeScript + Tailwind CSS 4
- [x] API client (fetch) + WebSocket client + Zustand store
- [x] Dashboard page with StatusBadge + AppTable components
- [x] Vitest: 8 component tests pass
- [x] go:embed + SPA fallback handler for web UI serving
- [x] Build: 9.5MB binary with embedded web UI

### Phase 3: Port Daemon Core — COMPLETE (2026-03-25)
- [x] NamespaceConfig (YAML parsing with auth, proxy, TLS, bundle)
- [x] NsGenContext + NamespaceGenerator (all infrastructure + webapps)
- [x] Docker client (official SDK: containers, images, exec, logs, stats, probes)
- [x] AppRuntime state machine + NamespaceRuntime (goroutine + channels)
- [x] BundlesService (git clone/pull) + bundle YAML resolver
- [x] Daemon HTTP server on Unix socket with all API routes
- [x] CLI: start (foreground) + stop (with --shutdown)
- [x] Unit tests: 36 total (config, proxyBaseUrl, state machine, memory, formatBytes)
- [x] Binary: 14MB with Docker SDK + web UI embedded

### Phase 4: Full CLI + Apply + Diff — COMPLETE (2026-03-25)
- [x] All commands ported: start, stop, status, health, config, describe, logs, exec, restart
- [x] `citeck apply -f namespace.yml` (--wait, --timeout, --force, --dry-run)
- [x] `citeck diff -f new.yml` (configuration comparison)
- [x] `citeck wait --status RUNNING --app X --healthy --timeout`
- [x] `citeck diagnose` (--fix, --dry-run) — socket, config, Docker, ports
- [x] `citeck reload` — hot-reload configuration
- [x] 17 CLI commands total, all support -o json

### Phase 5: Full Web Dashboard — COMPLETE (2026-03-25)
- [x] React Router: dashboard, app detail, logs, config pages
- [x] AppDetail: container info, ports, volumes, env, logs preview, restart
- [x] Logs page: real-time viewer with search, tail, auto-refresh
- [x] Config page: system health display
- [x] 9 Vitest component tests

### Phase 6: Liveness + Self-Healing — COMPLETE (2026-03-25)
- [x] Reconciler: desired vs actual state, auto-recreate missing containers
- [x] Liveness probes: periodic health checks, auto-restart on failure
- [x] Graceful shutdown ordering (proxy → webapps → keycloak → infra)
- [x] Operation history JSONL logging

### Phase 7: Remote Daemon + Auth — COMPLETE (2026-03-25)
- [x] Token auth middleware (required on TCP, skip on Unix socket)
- [x] CORS middleware for web UI dev mode
- [x] `citeck token generate/show`
- [x] 5 middleware tests

### Phase 8: Advanced Features — COMPLETE (2026-03-25)
- [x] `citeck cert status`: show cert expiry, issuer, SANs
- [x] `citeck cert generate`: self-signed ECDSA P256 (pure Go crypto)
- [x] `citeck clean`: orphaned resource cleanup (--execute, --volumes)

### Phase 9: Citeck Desktop (Wails v3)
- [ ] Requires Wails v3 SDK installation
- [ ] Connection manager UI, system tray, native notifications

### Phase 10: Distribution — COMPLETE (2026-03-25)
- [x] .goreleaser.yml: multi-platform (linux/darwin/windows, amd64/arm64)
- [x] scripts/install.sh: curl|sh installer with platform detection
- [x] scripts/citeck.service: systemd service template
- [x] GitHub Actions release workflow

### E2E Verification — All 5 Configs Tested (2026-03-25)

| # | Auth | Host | TLS | Port | Apps | Browser Verified |
|---|------|------|-----|------|------|-----------------|
| 1 | BASIC | localhost | no | 80 | 19/19 | Dashboard + Admin (Playwright) |
| 2 | BASIC | localhost | self-signed | 443 | 19/19 | TLS dashboard (Playwright) |
| 3 | KEYCLOAK | custom.launcher.ru | self-signed | 443 | 20/20 | OIDC discovery (curl) |
| 4 | KEYCLOAK | localhost | no | 80 | 20/20 | Full OIDC login flow (Playwright) |
| 5 | BASIC | custom.launcher.ru | self-signed | 8443 | 19/19 | curl + Playwright API |

- Code review: 15 issues fixed (deadlock, race conditions, init container wait)
- Generator diff: 17 differences with Kotlin fixed (MongoHost, Keycloak DB, port counter, EAPPS init containers, etc.)

## Summary

**Binary:** 14MB single Go binary with embedded React web UI
**CLI commands:** 22 total, all support `-o json`
**Tests:** 43 Go unit + 9 Vitest + 8 Playwright E2E = 60 total
**All 5 test configs pass** from clean start with full browser verification
