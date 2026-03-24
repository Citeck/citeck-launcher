# Progress Log — Citeck CLI Production Readiness

## Status: COMPLETE

All priorities implemented. All 5 test configurations pass. All tests pass. All code lints clean.

## Priority 1: Bug Fixes

| # | Bug | Status |
|---|-----|--------|
| 4.1 | TLS startup probe (port 80 not published) | Fixed — exec probe with `curl -sf` inside container |
| 4.2 | proxyBaseUrl omits non-standard port | Fixed — includes port when != default for scheme |
| 4.3 | Install script upgrade restart too short | Fixed — polling loop, reset-failed, post-start check |
| 4.4 | ERR_HTTP2_PROTOCOL_ERROR | NOT reproduced in any config |
| 4.5 | Lua file bind mount sed errors | Fixed — mount to /tmp, copy via init action |

### Additional bugs found and fixed during testing
- Daemon doesn't auto-start namespace → `setActive(true)` + `updateAndStart()`
- Shadow JAR resource loading → `jarFile.getInputStream(entry)` directly
- appfiles prefix creates dirs instead of files → strip `appfiles/` prefix
- TLS probe uses `wget` (not available) → changed to `curl`

## Priority 2: CLI Commands (8 total)

| Command | Description |
|---------|-------------|
| `citeck logs <app> [--tail N] [--follow]` | Container logs |
| `citeck restart <app>` | Restart single app |
| `citeck inspect <app>` | Container details |
| `citeck version` | Version info |
| `citeck health` | System health check |
| `citeck exec <app> <cmd...>` | Exec in container |
| `citeck config show` | Display config |
| `citeck config validate` | Validate config |

## Priority 3: E2E Testing

| # | Config | Result |
|---|--------|--------|
| 1 | BASIC + localhost + HTTP (80) | PASS — Playwright dashboard verified |
| 2 | BASIC + localhost + TLS (443) | PASS — TLS exec probe works |
| 3 | KEYCLOAK + custom.launcher.ru + TLS (443) | PASS — OIDC discovery correct |
| 4 | KEYCLOAK + localhost + HTTP (80) | PASS — Playwright login flow verified |
| 5 | BASIC + custom.launcher.ru + TLS (8443) | PASS — proxyBaseUrl port works |

## Unit Tests
- NsGenContext.proxyBaseUrl: 10 test cases (default/non-standard ports, scheme, host)
- NamespaceConfig: YAML deserialization, default values, Builder round-trip

## All Commits

| # | Hash | Message |
|---|------|---------|
| 1 | 232e20c | Fix proxyBaseUrl to include non-standard port |
| 2 | d720e83 | Fix TLS startup probe and lua file bind mount for proxy |
| 3 | b8c4c02 | Fix citeck-install.sh upgrade restart reliability |
| 4 | 08927a8 | Add new CLI commands: logs, restart, inspect, exec, version, health, config |
| 5 | e2b53b4 | Fix daemon startup, JAR resource loading, and appfiles prefix bug |
| 6 | c7ed60f | Fix TLS startup probe to use curl instead of wget |
| 7 | 07e45f4 | Update PROGRESS.md with E2E test results |
| 8 | e2bff61 | Update PROGRESS.md — all 5 test configs pass E2E |
| 9 | e1ffa03 | Add unit tests for NsGenContext.proxyBaseUrl and NamespaceConfig |
| 10 | 2b9d584 | Add citeck config validate command |
