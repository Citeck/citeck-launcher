# API Reference

All endpoints are under `/api/v1`. Responses are JSON unless noted.

## Transport

| Transport | Mux | Auth | CSRF |
|---|---|---|---|
| Unix socket | socketMux (all routes) | implicit (local) | not required |
| mTLS TCP (non-localhost) | socketMux (all routes) | client certificate | not required |
| Localhost TCP | tcpMux (safe routes only) | none | `X-Citeck-CSRF: 1` required on POST/PUT/DELETE |

## Error Response

```json
{
  "error": "Not Found",
  "code": "APP_NOT_FOUND",
  "message": "app \"foo\" not found"
}
```

### Error Codes

| Code | Description |
|---|---|
| `APP_NOT_FOUND` | App name not found in namespace |
| `NOT_CONFIGURED` | No namespace configured |
| `SNAPSHOT_IN_PROGRESS` | Another snapshot operation is running |
| `INVALID_CONFIG` | Config parse/validation error |
| `INVALID_REQUEST` | Malformed request body |
| `SSRF_BLOCKED` | URL resolved to blocked IP range |
| `APP_ALREADY_RUNNING` | App is already in RUNNING state |
| `NAMESPACE_RUNNING` | Operation requires stopped namespace |
| `CSRF_MISSING` | X-Citeck-CSRF header missing (localhost TCP only) |
| `INTERNAL_ERROR` | Unhandled server error (panic recovery) |

---

## Daemon

### GET /api/v1/daemon/status

Returns daemon status.

```bash
curl --unix-socket /run/citeck/daemon.sock http://localhost/api/v1/daemon/status
```

```json
{"running": true, "pid": 1234, "uptime": 60000, "version": "2.0.0", "workspace": "daemon", "socketPath": "/run/citeck/daemon.sock", "desktop": false, "locale": "en"}
```

### POST /api/v1/daemon/shutdown

Graceful shutdown. **Socket-only.**

```bash
curl --unix-socket /run/citeck/daemon.sock -X POST http://localhost/api/v1/daemon/shutdown
```

### GET /api/v1/daemon/logs

Returns daemon log tail. Query: `?tail=200` (default 200).

### PUT /api/v1/daemon/loglevel

Change runtime log level. **Socket-only.**

```bash
curl --unix-socket /run/citeck/daemon.sock -X PUT -H 'Content-Type: application/json' \
  -d '{"level":"debug"}' http://localhost/api/v1/daemon/loglevel
```

Levels: `debug`, `info`, `warn`, `error`.

---

## Namespace

### GET /api/v1/namespace

Returns current namespace with all apps.

### POST /api/v1/namespace/start

Start namespace (all apps).

### POST /api/v1/namespace/stop

Stop namespace (all apps).

### POST /api/v1/namespace/reload

Reload config and regenerate. **Socket-only.**

---

## Apps

### GET /api/v1/apps/{name}/logs

Stream app logs. Query: `?tail=500&follow=true`.

Follow mode returns a chunked stream (not SSE).

### POST /api/v1/apps/{name}/restart

Restart a single app.

### POST /api/v1/apps/{name}/stop

Stop a single app.

### POST /api/v1/apps/{name}/start

Start a single app.

### GET /api/v1/apps/{name}/inspect

Returns detailed container info (ports, volumes, env, labels, uptime).

### POST /api/v1/apps/{name}/exec

Execute command in container. **Socket-only.**

```bash
curl --unix-socket /run/citeck/daemon.sock -X POST -H 'Content-Type: application/json' \
  -d '{"command":["ls","-la"]}' http://localhost/api/v1/apps/emodel/exec
```

### GET /api/v1/apps/{name}/config

Returns app YAML config (text/yaml).

### PUT /api/v1/apps/{name}/config

Update app config (env, resources, probes only — image/volumes/cmd locked). **Socket-only.**

### GET /api/v1/apps/{name}/files

List editable bind-mount files.

### GET /api/v1/apps/{name}/files/{path}

Read a bind-mount file.

### PUT /api/v1/apps/{name}/files/{path}

Write a bind-mount file. **Socket-only.**

### PUT /api/v1/apps/{name}/lock

Toggle app lock (prevents regeneration).

```json
{"locked": true}
```

---

## Config

### GET /api/v1/config

Returns namespace.yml content (text/yaml).

### PUT /api/v1/config

Write namespace.yml. **Socket-only.** Validates before saving.

---

## Events (SSE)

### GET /api/v1/events

Server-Sent Events stream. Events include sequence numbers for gap detection.

```
data: {"type":"status","seq":42,"timestamp":1711500000000,"namespaceId":"default","appName":"emodel","before":"STARTING","after":"RUNNING"}
```

---

## Namespaces (Desktop mode)

### GET /api/v1/namespaces

List all namespaces.

### POST /api/v1/namespaces

Create namespace.

### DELETE /api/v1/namespaces/{id}

Delete namespace.

### GET /api/v1/templates

List namespace templates.

### GET /api/v1/quick-starts

List quick-start presets.

---

## Secrets

### GET /api/v1/secrets

List secrets (metadata only).

### POST /api/v1/secrets

Create or update a secret.

```json
{"id": "gitlab-token", "name": "GitLab Token", "type": "token", "value": "glpat-xxx", "scope": "global"}
```

### DELETE /api/v1/secrets/{id}

Delete a secret.

### GET /api/v1/secrets/{id}/test

Test secret connectivity (e.g., git auth).

---

## Snapshots

### GET /api/v1/snapshots

List local snapshots.

### POST /api/v1/snapshots/export

Export namespace volumes to ZIP.

### POST /api/v1/snapshots/import

Import snapshot ZIP. Multipart form: `file` field.

### POST /api/v1/snapshots/download

Download snapshot from URL.

```json
{"url": "https://example.com/snap.zip", "sha256": "abc123", "name": "my-snapshot.zip"}
```

### PUT /api/v1/snapshots/{name}

Rename snapshot.

### GET /api/v1/workspace/snapshots

List snapshots from workspace config.

---

## Volumes

### GET /api/v1/volumes

List namespace volumes.

### DELETE /api/v1/volumes/{name}

Delete a volume (namespace must be stopped).

---

## Diagnostics

### GET /api/v1/diagnostics

Run diagnostic checks.

### POST /api/v1/diagnostics/fix

Auto-fix fixable issues.

---

## Bundles

### GET /api/v1/bundles

List available bundle versions.

---

## Forms

### GET /api/v1/forms/{formId}

Get form spec (for UI dialogs).

---

## Health

### GET /api/v1/health

```json
{"status": "healthy", "healthy": true, "checks": [...]}
```

Status: `healthy`, `degraded`, `unhealthy`.

---

## Metrics

### GET /api/v1/metrics

Prometheus text exposition format.

```
citeck_build_info{version="2.0.0"} 1
citeck_uptime_seconds 3600.0
citeck_apps_total 20
citeck_apps_running 20
citeck_apps_failed 0
citeck_app_status{app="emodel",status="RUNNING"} 1
citeck_namespace_status{status="RUNNING"} 1
citeck_sse_subscribers 2
```

---

## System

### GET /api/v1/system/dump

System dump. Query: `?format=zip` for ZIP (includes per-app logs and daemon logs).
