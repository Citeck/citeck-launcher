# Tests for `internal/appfiles/proxy/lua_oidc_full_access.lua`

That file is the `access_by_lua_file` handler the launcher copies into the
`ecos-proxy-oidc` container at start (`internal/namespace/generator_proxy.go`),
replacing the image's own, whenever authentication is `KEYCLOAK`. It decides
which identity a request is given and writes it into the `X-ECOS-User` /
`X-Alfresco-Remote-User` headers that `ecos-gateway` trusts unconditionally,
so a mistake here is an authentication bypass.

Vendored from **citeck-devops** (`docker/nginx/ecos-proxy-oidc/tests/`), which
owns the upstream copies of the handler — `harness.lua` is byte-identical and
`spec_full_access.lua` differs only in detecting `is_gateway_public_uri` by its
call site rather than its definition (the launcher's copy still defines the
helper but no longer calls it). Keep them in sync: the spec is version-aware and
runs unmodified against either repo's copies.

## Running

```bash
./tests/proxy-lua/run.sh      # needs Docker; override with OPENRESTY_IMAGE
```

The openresty image is used only as a LuaJIT host. `harness.lua` runs the real
script under a stubbed `ngx` table and a `resty.openidc` that answers "this
client has no credentials at all", then reports the identity it assigned.
Nothing is started, nothing is contacted.

The same spec runs from `go test` as `TestProxyLuaSpec`, opt-in via
`CITECK_PROXY_LUA_E2E=1` (see `internal/appfiles/proxy_lua_test.go`, whose other
two tests are Docker-free source guards and do run in `make check`).

## What it guards

Every service exception grants an identity without authentication, so it must
match the path nginx actually ROUTED and nothing else — not a query parameter,
not a path segment in the middle, and not a path that `../` escapes from. All
three were live here, reproduced against this repo's handler before the fix
(unauthenticated: no PA cookie, no `Authorization`, no session):

| request | identity handed to `ecos-gateway` |
| --- | --- |
| `/gateway/emodel/api/records/query?probe=/healthcheck/monitor.html` | `service_healthcheck` |
| `/gateway/emodel/healthcheck/monitor.html` (marker mid-path) | `service_healthcheck` |
| `/healthcheck/x/..%2f..%2f..%2f..%2fgateway/emodel/api/records/query` | `service_healthcheck` |
| `/gateway/emodel/alfresco/monitoring/heartbeat` | `guest` |
| `/camunda/app/admin/users?probe=.css` (faked static extension) | `guest` |

nginx normalizes the encoded traversal and routes the request to an arbitrary
gateway endpoint, while the handler sees a raw `$request_uri` that still
contains the marker. Once `userName` is set the OIDC branch is skipped entirely.
`service_healthcheck` is not privileged in itself — `ecos-gateway` auto-creates
the person and it gets `GROUP_EVERYONE` + `ROLE_USER` — so the effect is
anonymous → ordinary authenticated user.

The fix is `getRequestPath()`: `$request_uri` with the query string dropped,
`%XX` decoded the way nginx decodes a path, and `.`, `..` and duplicate slashes
resolved. The healthcheck and infrastructure-metrics rules (`/healthcheck/`,
`/rabbitmq`, `/node-exporter`, `/postgres-exporter`, `/cadvisor/`) were deleted
rather than anchored — those locations carry their own authentication and never
run this handler. Only two rules remain, both matched against
`getRequestPath()`: `/alfresco/monitoring` (anchored at `^`) and the
static-resource rule.

Since 2.15.2 `isStaticResUri` also excludes paths containing `/share/res/` and
`/alfresco/`, so anonymous requests for Alfresco and Share resources are no
longer handed `guest` for having a static-looking extension.

## Why not `ngx.var.uri`

`$uri` is normalized and decoded, but the rewrite phase runs **before** the
access phase, so by the time this script runs `$uri` has already been rewritten
(measured against the upstream config: `/gateway/emodel/api/records/query`
arrives as `/emodel/api/records/query`, `/v2/dashboard` as `/index.html`). The
script therefore derives its own value.

Side effect worth knowing: a static resource with a cache-busting query
(`/main.js?v=1.2.3`) is now recognised as a static resource. It previously fell
through to the OIDC branch, because the `.3` of the query string was read as its
file extension.
