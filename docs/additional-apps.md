# Additional apps — run a custom container by configuration alone

The **workspace config** (`workspace-v1.yml`) can declare **additional apps**: arbitrary
containers that run alongside the built-in Citeck/infra apps, **without any change to the
launcher**. This is the config-driven way to add a new service (a mock/simulator, an
auxiliary Go service, a sidecar, …) instead of writing a dedicated generator.

Because they live in the workspace config, additional apps are **defined once and
distributed to every namespace** that uses the workspace (from the workspace git repo),
and are applied **live on each generation** — exactly like the `webapps:` section. Edit
the workspace config to add or change a custom service for everyone. (They are **not**
declared per-namespace in `namespace.yml`.)

Each entry maps to a generic `ApplicationDef` (image, env, ports, dependencies,
probes, …). Environment values support the same `${PG_HOST}` / `${ZK_HOST}` /
`${ZK_PORT}` / `${RMQ_HOST}` / `${MONGO_HOST}` / … template variables as the rest of
the config. In **server mode** the container is internal to the Docker network
(reachable by `name` / `networkAliases`) — only the proxy publishes ports.

## Schema (`additionalApps[]`)

| Field | Required | Notes |
|---|---|---|
| `name` | yes | Container/app name; unique; must not collide with a built-in app. Collisions with infra/core names (`proxy`, `gateway`, `keycloak`, …) are rejected at config validation; collisions with bundle-loaded webapp ids (`edi`, `integrations`, enterprise apps, …) are detected at generation time — the built-in app wins and the colliding entry is skipped with an error log (it never overwrites a real container). |
| `image` | yes | Full image ref (`registry/repo:tag`, or a locally-present tag), **or a bundle-style `<repoId>/path:tag`** whose first segment is an `imageRepos` id — resolved to that registry's URL exactly like bundle apps (e.g. `core/citeck-edi-sim:0.1.0` → `nexus.citeck.ru/citeck-edi-sim:0.1.0`). Same resolution applies to `initContainers[].image`. |
| `enabled` | no | Default `true`; set `false` to keep the definition but not deploy. |
| `kind` | no | `CITECK_CORE` / `CITECK_CORE_EXTENSION` / `CITECK_ADDITIONAL` / `THIRD_PARTY` (default). |
| `networkAliases` | no | Extra DNS aliases on the namespace network. |
| `environments` | no | `KEY: value` map; `${VAR}` template variables are resolved. |
| `cmd` | no | Override the image command; `${VAR}` resolved per arg. |
| `ports` | no | `host:container` (published only in desktop mode). |
| `volumes` | no | Docker volume / bind mounts. |
| `dependsOn` | no | App names this container starts after (e.g. `zookeeper`). If a name here is **not** present in the generated namespace (typo, an app disabled by mode, or another app that was itself excluded), this app is **excluded from the namespace** — transitively: in a chain `A → B → C`, a missing `C` drops `B` and then `A`. The exclusion is logged. This rule applies to every app uniformly; built-in apps only ever point `dependsOn` at apps that are present (or guard the dependency on the target's existence), so in practice it excludes only misconfigured `additionalApps`. |
| `initContainers` | no | Containers run to completion **before** the main one (wait-for, migration, fixtures). Each: `image` (required) + `environments` / `cmd` (`${VAR}` resolved) / `volumes` / `kind`. |
| `initActions` | no | `exec` commands run inside the container right after creation (`${VAR}` resolved per arg). |
| `startupConditions` | no | Readiness gates (`probe` / `log`). |
| `livenessProbe` | no | HTTP/exec liveness probe. |
| `resources` | no | `limits.memory`. |
| `shmSize` | no | `/dev/shm` size. |
| `stopTimeout` | no | Per-app graceful-stop budget in seconds (SIGTERM→SIGKILL); `0` = daemon default. |

These cover **every container-level knob** the launcher's own app generators set, so
any app — not just the EDI sim — is expressible by config alone. `${PG_HOST}` /
`${PG_PORT}` / `${ZK_HOST}` / `${ZK_PORT}` / `${MONGO_HOST}` / `${RMQ_HOST}` /
`${MAILHOG_HOST}` / `${ONLYOFFICE_HOST}` template variables are resolved in every
string you supply (env, cmd, init-action exec, init-container env/cmd).

**Boundary.** `additionalApps` defines a *container* on the namespace network. It does
**not** auto-wire an HTTP route through the Citeck proxy, nor publish ECOS webapp
cloud-config / `dataSources` / webapp-properties — those remain the job of the built-in
Citeck-app generators (the `webapps:` section). A self-registering service (like the EDI
sim, via ZooKeeper) needs none of that; a plain HTTP app that must be reachable at a
proxy path is out of `additionalApps`' scope.

## Example — the EDI simulator (`citeck-edi-sim`)

The simulator is a plain Go container that self-registers in the ECOS ZooKeeper
Service Registry, so the platform discovers it by name. Adding it is pure config:

```yaml
# workspace-v1.yml
additionalApps:
  - name: edi-sim
    image: registry.citeck.ru/community/citeck-edi-sim:0.1.0
    networkAliases: [ EcosEdiSimApp ]
    dependsOn: [ zookeeper ]
    environments:
      ZOOKEEPER_HOSTS: "${ZK_HOST}:${ZK_PORT}"   # → registers under /ecos/webapps
      DISCOVERY_APPNAME: edi-sim
      # AUTH_JWTSECRET: "<base64 HS512>"          # optional: admin-only in-platform
    livenessProbe:
      http: { path: /health, port: 8080 }
```

On `citeck reload` the launcher pulls the image, starts the container with those env
vars, and the simulator registers itself in ZooKeeper — the real `ecos-edi` (or any
service) in the same namespace then finds it by name with no fixed address.
