# Progress Log

## Summary

All Priority 1 bug fixes and Priority 2 CLI commands are implemented. All 5 test configurations pass end-to-end.

## Priority 1: Critical bug fixes

| # | Bug | Fix | Commit |
|---|-----|-----|--------|
| 4.2 | proxyBaseUrl omits non-standard port | Include port when != 80/443 | 232e20c |
| 4.1 | TLS startup probe checks port 80 via host (not published) | Use exec probe with `curl -sf` inside container | d720e83, c7ed60f |
| 4.5 | Bind-mounted lua file causes `sed: cannot rename` | Mount to /tmp, copy via init action | d720e83 |
| 4.3 | Install script upgrade restart too short + failed state | Polling loop (30s), reset-failed, post-start check | b8c4c02 |
| 4.4 | ERR_HTTP2_PROTOCOL_ERROR | NOT reproduced — static assets load fine in all configs | N/A |

### Additional bugs found during testing
| Bug | Fix | Commit |
|-----|-----|--------|
| Daemon doesn't auto-start namespace | Call `setActive(true)` + `updateAndStart()` | e2b53b4 |
| Shadow JAR resource loading fails | Use `jarFile.getInputStream(entry)` directly | e2b53b4 |
| appfiles prefix creates dirs instead of files | Strip `appfiles/` prefix from keys | e2b53b4 |

## Priority 2: New CLI commands (commit 08927a8)

| Command | Description |
|---------|-------------|
| `citeck logs <app> [--tail N] [--follow]` | Show container logs |
| `citeck restart <app>` | Restart a single application |
| `citeck inspect <app>` | Container details (ports, volumes, env, network, uptime) |
| `citeck version` | Version, build time, Java, OS |
| `citeck health` | Docker, containers, disk, JVM memory |
| `citeck exec <app> <cmd...>` | Execute command in container |
| `citeck config show` | Display current namespace.yml |

## Priority 3: E2E Testing Results

| # | Config | Status | Notes |
|---|--------|--------|-------|
| 1 | BASIC + localhost + HTTP (80) | PASS | 19 apps RUNNING, dashboard loads, Playwright verified |
| 2 | BASIC + localhost + TLS (443) | PASS | 19 apps RUNNING, TLS exec probe works, HTTPS OK |
| 3 | KEYCLOAK + custom.launcher.ru + TLS (443) | PASS | 20 apps RUNNING, OIDC discovery correct, proxyBaseUrl correct |
| 4 | KEYCLOAK + localhost + HTTP (80) | PASS | 20 apps RUNNING, login→update password→dashboard Playwright flow |
| 5 | BASIC + custom.launcher.ru + TLS (8443) | PASS | 19 apps RUNNING, non-standard port works, proxyBaseUrl includes port |

## Commits

1. 232e20c — Fix proxyBaseUrl to include non-standard port
2. d720e83 — Fix TLS startup probe and lua file bind mount for proxy
3. b8c4c02 — Fix citeck-install.sh upgrade restart reliability
4. 08927a8 — Add new CLI commands: logs, restart, inspect, exec, version, health, config
5. e2b53b4 — Fix daemon startup, JAR resource loading, and appfiles prefix bug
6. c7ed60f — Fix TLS startup probe to use curl instead of wget
7. 07e45f4 — Update PROGRESS.md with E2E test results

## Remaining items
- Unit tests for proxyBaseUrl, NamespaceConfig serialization, new DTOs
- `citeck config validate` command (5.8, lower priority)
