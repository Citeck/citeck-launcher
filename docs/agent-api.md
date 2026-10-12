# Agent API

Launcher provides a catalog of available operations at `GET /api/v1/agent`.
Read it through the built-in client:

```bash
citeck agent
citeck agent application.stop
citeck agent namespace.start
```

The command always outputs JSON, connects to an existing daemon, and never starts
a new one. Use a binary that implements this API: changing the workspace does not
add discovery support to an older installed version.

The desktop package can also read the catalog without opening a window:

```bash
citeck-launcher agent
citeck-launcher agent application.stop
```

In the GUI binary, this headless mode applies specifically to the `agent` command.
The `citeck start/stop/...` control examples use the `citeck` CLI binary;
on desktop, you can also call the HTTP API directly through the socket.

## Connection

The CLI uses the same transport settings as other commands:
`CITECK_RUN`, `CITECK_DESKTOP`, `CITECK_HOST`, `CITECK_TLS_CERT`, `CITECK_TLS_KEY`.
The `citeck-launcher agent` command selects desktop paths automatically. To use a
separate CLI binary on desktop, set `CITECK_DESKTOP=true` and the same `CITECK_HOME`
as the target Launcher, or explicitly select its socket directory with `CITECK_RUN`.
These paths and transport variables refer to settings, not credential values.

In server mode, the default socket is `/run/citeck/daemon.sock`:

```bash
curl --noproxy '*' --unix-socket /run/citeck/daemon.sock \
  http://localhost/api/v1/agent
curl --noproxy '*' --unix-socket /run/citeck/daemon.sock \
  http://localhost/api/v1/agent/operations/application.stop
```

The API uses the existing Unix/TCP transport. Discovery opens no new port and does
not change TLS or `api_auth`: TCP requests follow the configured authentication
rules, and the local Unix socket retains the existing local access boundary.
The catalog does not authorize actions beyond the user's task.
On Linux desktop, the default socket is `~/.citeck/launcher/run/daemon.sock`;
on macOS and Windows, the base directory is the platform-specific Citeck data
directory. If `CITECK_HOME`/`CITECK_RUN` are set, use those paths.

## Response contents

The English catalog contains:

- `interfaceVersion` — the discovery format version (currently `1`);
- `launcherVersion`, `mode`, `socketPath` — information about the connected daemon;
- `instructions` — rules for managing resources and observing operation results;
- `routes` — the complete list of actually registered `/api/v1` routes;
- `operations` — contracts for core automation workflows: HTTP method/path,
  stable `id`, scope, parameters and request body JSON Schema, response format,
  CLI examples, `readOnly`, `destructive`, `async` flags, and supported modes.

The route list is collected when production HTTP handlers are registered. An
operation appears in the catalog only if its route is actually registered.
For example, desktop update routes are absent without the update service.
A route's `operationId` links it to a detailed contract; other routes are listed
for capability discovery, but their request bodies must not be guessed.

`GET /api/v1/agent/operations/{id}` returns one contract; an unknown `id`
returns a JSON `404`. `supported` means compatibility with the process mode,
not user permission or readiness in the current state.
For example, desktop namespace activation has `supported=false` on a server.
Discovery requests using mutating HTTP methods receive a JSON `405`.

The catalog reads route metadata, version/mode, and the socket path. It does not
export namespace configuration, secret values, cookies, or keys, and does not
access Docker. Responses from individual diagnostic operations, such as
`application.inspect`, may contain environment variables: redact sensitive
contents before sharing them with others or saving them in shared reports.

## Managing applications and namespaces

| Task | HTTP | CLI |
|---|---|---|
| Current state and short application names | `GET /api/v1/namespace` | `citeck status --format json` |
| Start the active namespace | `POST /api/v1/namespace/start` | `citeck start --detach` |
| Stop the active namespace | `POST /api/v1/namespace/stop` | `citeck stop --detach` |
| Start/re-attach an application | `POST /api/v1/apps/{name}/start` | `citeck start <name> --detach` |
| Stop/detach an application | `POST /api/v1/apps/{name}/stop` | `citeck stop <name> --detach` |
| Restart an application | `POST /api/v1/apps/{name}/restart` | `citeck restart <name> --detach` |
| Preview configuration changes | `GET /api/v1/namespace/reload-plan` | `citeck diff --format json` |
| Apply configuration | `POST /api/v1/namespace/reload` | `citeck reload` |
| Application logs | `GET /api/v1/apps/{name}/logs?tail=100` | `citeck logs <name> --tail 100` |
| Launcher logs | `GET /api/v1/daemon/logs?tail=100` | `citeck logs --tail 100` |

These operations target the **active** namespace, not an arbitrary `id`.
First read its `id` and short application names from `apps[].name`. A server has
one namespace; on desktop, select the target namespace through the namespace
list/activation in the current workspace. Do not substitute a Docker container
name or Docker image for `name`.

Stopping an application persists a manual detach; starting it clears the detach.
When detaching an application that affects namespace composition, Launcher also
recalculates the configuration of dependent applications. Stopping a namespace
preserves data and manual detach settings, leaving the daemon running. Stopping
an essential dependency may leave the namespace in `STALLED`; this reflects the
selected state and is not a reason to bypass Launcher.

`docker stop` does not persist this intent: reconciliation may start the container
again, and Launcher's state will diverge from Docker. For Launcher-managed
resources, use the lifecycle API/CLI, including restart and detach.
Do not start a second daemon on the same Docker engine to access the API:
`CITECK_HOME`, `CITECK_RUN`, and a different id do not isolate daemon Docker resources.

## Observing operation results

A successful lifecycle HTTP response means the request was accepted. Afterwards,
check `namespace.status`, `updating`, `updateError`, `apps[].status`, and `detached`,
or subscribe to `GET /api/v1/events` (SSE). While waiting, verify that the active
namespace's `id` still matches the target. SSE supports `lastSeq` and the
`Last-Event-ID` header.

`namespace.start` updates configuration before starting. The `force=true`
parameter only bypasses the git pull timer; it does not force a re-pull of release
images already present. The default is `force=false`.

Application start/restart requires a namespace in `RUNNING`, `STARTING`, or
`STALLED`. The exception for start: an already detached application can be
re-attached while the namespace is `STOPPED`, and will start with the namespace.
Snapshot/migration and recovery gates still apply. If the API returns `409`,
read `code` and `message`, the current state, and any blocking conditions;
do not replace the refusal with a direct Docker command.

The catalog also describes logs/health/events, exec/JVM diagnostics, infrastructure
preflight/migrate/rollback, S3 availability and provisioning, snapshots, and
volume/bundle lists. Before performing data operations, read the contract and
preflight results.

## Git refresh recovery

Explicit Git updates report refresh failures even when a cached copy exists.
Synchronous failures include `git` metadata in the JSON error; asynchronous
namespace updates expose `updateGitError` beside `updateError`. The metadata
contains the failed `repoUrl`, owning `workspaceId`, selected `secretId`,
`cacheAvailable`, and `cacheUpdatedAt` (epoch milliseconds). An absent timestamp
means unknown, not the modification time of repository files.

After explicit user approval, repeat `cachedRepo=<repoUrl>` query parameters on
`POST /api/v1/namespace/start`, `POST /api/v1/workspace/update`, or
`POST /api/v1/bundles/{id}/pull` to use those existing repository copies for the
current recovery attempt. `cachedRepos` in a subsequent failure identifies
already selected copies so retries can carry those choices forward. This does
not suppress Git access globally; a later independent update attempts refresh
normally. The server checks repository ownership and local availability again.
Cached content may be outdated. Snapshot, migration, configuration and namespace
lifecycle gates still apply.

To select another existing token, use the `git.repository-secret` contract:
`PUT /api/v1/workspaces/{id}/git-secret` with JSON `repoUrl` and `secretId`.
This desktop-only operation changes a reference; it does not receive or return
the token value. For a bundle repository it preserves the workspace repository's
own credential and stores the bundle link in the local workspace overlay.

### Download activity

Desktop self-update status (`GET /api/v1/desktop/update/status`) includes
`progress`: `phase`, `receivedBytes`, `totalBytes`, `startedAt`, and `updatedAt`.
Times are Unix milliseconds; a total <= 0 means unknown. Staging phases are
`checking`, `downloading`, `verifying`, `extracting`, and `ready` (verified payload
staged, not proof of a successful daemon swap). The desktop polls status during
the pending apply request and continues observing the existing swap/rollback gate.

Snapshot SSE `snapshot_progress` events with `phase: downloading` include
`receivedBytes` and `totalBytes`; existing `current`/`total` remain volume counters
for export/import. Download start, import start, and terminal events drive the
snapshot overlay. Progress silence is an observation, never proof of completion,
failure, or cancellation. Download percentages never represent verification or
restoration completion.

Payload downloads (Launcher updates and snapshots) have no overall HTTP time
limit. Waiting for headers or the next response byte is limited to 30 seconds;
each received byte renews that inactivity deadline. Caller cancellation still
applies. Snapshot and Launcher update asset downloads retry transient transfer
failures
with 3-second pauses. Retry counters reset only when a requested Range resume
receives new bytes in a 206 response. Bytes from a full 200 response do not reset
counters, including when the server ignores Range and restarts from zero. Three
consecutive failed attempts without resumed byte progress stop the transfer;
there is no lifetime attempt limit while successful partial resumes progress.
Update assets (payload,
SHA256 and signature sidecars) resume using Range and, when available, If-Range
with a strong ETag or Last-Modified validator. If Range is ignored or the validator
changed and the server returns 200, the partial file is truncated and restarted.
For update assets, invalid partial-response ranges/validators, oversize bodies,
local file errors and permanent HTTP errors fail immediately. Snapshot invalid
partial responses are rejected before append and consume the bounded retry count. Partial updater files are scoped to one
staging call and removed on final failure/cancellation; a later manual attempt
starts fresh. SHA256/signature mismatch is immediate and never retried. Download
progress includes the resumed offset; restarted transfers honestly reset bytes.
Retries only fetch assets and never repeat an apply/health-gate failure.
Metadata requests retain their separate 30-second request timeout.

The desktop apply POST has no total client timeout, and its daemon handler lifts
the ordinary HTTP response write deadline. Caller cancellation and the asset
byte-inactivity timeout remain active during long staging. Snapshot and updater
partial responses share Content-Range offset/span/length validation. Snapshot
retry chains retain an in-memory resume validator and use identity encoding;
invalid partial responses never append data or reset progress counters. A short
chunked range remains a retryable incomplete transfer even when total size is
unknown. Git URL diagnostic fields omit userinfo, query and fragment data;
printable Git errors are sanitized while preserving their underlying identity.
