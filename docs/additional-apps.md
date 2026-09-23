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
| `image` | no | **A default: the image the BUNDLE names under this entry's name wins**, from its `dependencies:` section or above it — the same precedence a typed entry and a built-in service take. **Omitted ⇒ only the bundle can name it**, and a bundle that names none gets **no container** — the release decides that the service exists, the workspace how it is configured (this is how the observer is declared, see [Observer](observer.md)). An entry claims a bundle application of its name, so that service is generated from the entry and never as a webapp as well — **except a platform webapp the workspace lists in `webapps:`** (and the reserved core names, refused at load): such an entry is skipped with an error in the log, so `additionalApps` cannot re-declare a boxed service. When given: a full image ref (`registry/repo:tag`, or a locally-present tag), **or a bundle-style `<repoId>/path:tag`** whose first segment is an `imageRepos` id — resolved to that registry's URL exactly like bundle apps (e.g. `core/citeck-edi-sim:0.1.0` → `nexus.citeck.ru/citeck-edi-sim:0.1.0`). Same resolution applies to `initContainers[].image`. |
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
| `cloudConfig` | no | Keys the desktop CloudConfigServer hands this service when it is stopped in the launcher and run from an IDE. Strings take the same `${VAR}` substitution as env; numbers, booleans, lists and maps pass through. |
| `type` | no | Empty for a container (this table). `POSTGRES` / `QDRANT` make the entry a dependency the launcher knows, with its own settings — see [Additional databases and stores](databases.md). |

These cover **every container-level knob** the launcher's own app generators set, so
any app — not just the EDI sim — is expressible by config alone.

**Template variables** (`${VAR}`) are resolved in every string you supply (env, cmd,
init-action exec, init-container env/cmd, cloudConfig):

| Group | Variables |
|---|---|
| Infra hosts/ports | `${PG_HOST}` `${PG_PORT}` `${MONGO_HOST}` `${MONGO_PORT}` `${ZK_HOST}` `${ZK_PORT}` `${RMQ_HOST}` `${RMQ_PORT}` `${MAILHOG_HOST}` `${ONLYOFFICE_HOST}` `${KK_HOST}` |
| Platform secrets / URL | `${JWT_SECRET}` (same HS512 secret the webapps/observer validate — e.g. `AUTH_JWTSECRET: "${JWT_SECRET}"` to make a service admin-only behind the gateway) · `${OIDC_SECRET}` · `${WEB_URL}` (public base URL of the namespace) · `${ADMIN_PASSWORD}` |
| Messaging / Keycloak creds | `${RMQ_USER}` / `${RMQ_PASSWORD}` (the `citeck` service account) · `${KK_ENABLED}` `${KK_ADMIN_URL}` `${KK_ADMIN_USER}` `${KK_ADMIN_PASSWORD}` |
| Namespace secrets | `${secret:<id>}` — the namespace's own value of a secret the workspace declares in `secrets:`. **An app referencing a secret with no value is not generated** (and neither is anything depending on it). See [Workspace secrets](workspace-secrets.md). |

> There is **no** single `${PG_USER}`/`${PG_PASSWORD}` for the stand's own database —
> the platform uses a per-database `user==password==dbName` convention. A service that
> needs a database of its own declares one: an entry with `type: POSTGRES`
> ([Additional databases and stores](databases.md)), its password a `${secret:<id>}`.

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
      # AUTH_JWTSECRET: "${JWT_SECRET}"           # optional: admin-only in-platform
                                                  # (validates the JWT the gateway forwards)
    livenessProbe:
      http: { path: /health, port: 8080 }
```

On `citeck reload` the launcher pulls the image, starts the container with those env
vars, and the simulator registers itself in ZooKeeper — the real `ecos-edi` (or any
service) in the same namespace then finds it by name with no fixed address.
