# Progress Log

## 2026-03-24 — Priority 1: Critical bug fixes

### Completed
- **4.2 Fix proxyBaseUrl** (commit 232e20c): Include port in URL for non-standard ports (not 80/443). Fixes Keycloak redirect URIs and OIDC logout on custom ports.
- **4.1 Fix TLS startup probe** (commits d720e83, c7ed60f): Use exec probe (`curl -sf` inside container) for TLS mode instead of HTTP probe that checks host port 80 (not published in TLS mode). Fixed wget→curl since proxy image has curl but not wget.
- **4.5 Fix lua file bind mount** (commit d720e83): Mount lua to `/tmp` and copy via init action instead of direct bind mount to `/etc/nginx/includes/`, avoiding `sed: cannot rename` errors.
- **4.3 Fix install script upgrade restart** (commit b8c4c02): Replace `sleep 2` with 30s polling loop, add `systemctl reset-failed`, add post-start verification.
- **4.4 ERR_HTTP2_PROTOCOL_ERROR**: NOT reproduced in any config. Static assets load fine with HTTP/1.1 and HTTPS.

## 2026-03-24 — Priority 2: New CLI commands (commit 08927a8)

### Completed
- `citeck logs <app> [--tail N] [--follow]` — show container logs
- `citeck restart <app>` — restart a single application
- `citeck inspect <app>` — show container details (ports, volumes, env, network, uptime)
- `citeck version` — show version, build time, Java, OS
- `citeck health` — check Docker, containers, disk, JVM memory
- `citeck exec <app> <cmd...>` — execute command in a container
- `citeck config show` — display current namespace.yml

## 2026-03-24 — Critical runtime fixes found during testing (commit e2b53b4)

### Bugs found and fixed
- **Daemon auto-start**: Namespace loaded but not started. Added `setActive(true)` + `updateAndStart()`.
- **Shadow JAR resource loading**: CiteckJarFile.getAllFiles failed with nested URLs. Fixed to use `jarFile.getInputStream(entry)`.
- **appfiles prefix bug**: Shadow JAR returned file keys with `appfiles/` prefix, causing config files to be created as directories instead of files. Added prefix stripping.
- **Configurable run dir**: Added `-Dciteck.run` system property.

## 2026-03-24 — E2E Testing Results

### Config 1: BASIC + localhost + HTTP (port 80) — PASS
- All 19 apps reach RUNNING
- Static assets load (JS, CSS) with HTTP 200
- BASIC auth works (curl + Playwright)
- Dashboard renders fully with workspaces, tasks, navigation

### Config 2: BASIC + localhost + TLS (port 443) — PASS
- All 19 apps reach RUNNING including proxy (TLS exec probe works)
- HTTPS serves pages and API correctly
- Self-signed cert works

### Config 4: KEYCLOAK + localhost + HTTP (port 80) — PASS
- All 20 apps reach RUNNING (including Keycloak)
- OIDC redirect to Keycloak login page works
- Login with admin/admin → password update → redirect back to dashboard
- Dashboard fully functional after OIDC authentication

### Not yet tested
- Config 3: KEYCLOAK + custom.launcher.ru + TLS (port 443)
- Config 5: BASIC + custom.launcher.ru + TLS (port 8443)
- Unit tests

### Commits (in order)
1. 232e20c — Fix proxyBaseUrl to include non-standard port
2. d720e83 — Fix TLS startup probe and lua file bind mount for proxy
3. b8c4c02 — Fix citeck-install.sh upgrade restart reliability
4. 08927a8 — Add new CLI commands: logs, restart, inspect, exec, version, health, config
5. e2b53b4 — Fix daemon startup, JAR resource loading, and appfiles prefix bug
6. c7ed60f — Fix TLS startup probe to use curl instead of wget
