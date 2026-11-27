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
| `name` | yes | Container/app name; unique; must not collide with a built-in app. Collisions with infra/core names (`proxy`, `gateway`, `keycloak`, …) are rejected at config validation, and one such entry makes the **whole** `additionalApps` section ignored (with a warning in the log). A bundle application of the same name (`edi`, `integrations`, enterprise apps, …) is **claimed** by the entry and generated from it; the built-in webapp wins only if the workspace lists it in `webapps:` — then the entry is skipped with an error log (it never overwrites a real container). |
| `image` | no | **A default: the image the BUNDLE names under this entry's name wins**, from its `dependencies:` section or above it; next comes the workspace `dependencies:` entry of that name, and only then this `image:` — the same precedence a typed entry and a built-in service take. **Omitted ⇒ only the bundle (or the workspace `dependencies:` section) can name it**, and a bundle that names none gets **no container** — the release decides that the service exists, the workspace how it is configured (this is how the observer is declared, see [Observer](observer.md)). An entry claims a bundle application of its name, so that service is generated from the entry and never as a webapp as well — **except a platform webapp the workspace lists in `webapps:`** (and the reserved core names, refused at load): such an entry is skipped with an error in the log, so `additionalApps` cannot re-declare a boxed service. When given: a full image ref (`registry/repo:tag`, or a locally-present tag), **or a bundle-style `<repoId>/path:tag`** whose first segment is an `imageRepos` id — resolved to that registry's URL exactly like bundle apps (e.g. `core/citeck-edi-sim:0.1.0` → `nexus.citeck.ru/citeck-edi-sim:0.1.0`). Same resolution applies to `initContainers[].image`. |
| `enabled` | no | Default `true`; set `false` to keep the definition but not deploy. |
| `kind` | no | `CITECK_CORE` / `CITECK_CORE_EXTENSION` / `CITECK_ADDITIONAL` / `THIRD_PARTY` (default). |
| `networkAliases` | no | Extra DNS aliases on the namespace network. |
| `environments` | no | `KEY: value` map; `${VAR}` template variables are resolved. |
| `cmd` | no | Override the image command; `${VAR}` resolved per arg. |
| `ports` | no | `[hostIP:]host:container[/proto]`, published only in desktop mode. Without an address the port binds `127.0.0.1`; `*` means all interfaces. |
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
| `postgresDatabases` | no | Databases the service gets in the **stand's own** `postgres` (Launcher 2.20+), see [A database in the stand's postgres](#a-database-in-the-stands-postgres-postgresdatabases). |
| `type` | no | Empty for a container (this table). `POSTGRES` / `QDRANT` make the entry a dependency the launcher knows, with its own settings — see [Additional databases and stores](databases.md). `SEAWEEDFS` supplies a managed mini profile: volume generations, command, standard probes and desktop ports; optional `settings` is a scalar YAML map, not a command array; see [SeaweedFS updates](seaweedfs-upgrade.md). |

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
> needs a database asks for one in the stand's `postgres` with `postgresDatabases`
> (same convention, see [below](#a-database-in-the-stands-postgres-postgresdatabases)),
> or declares a cluster of its own: an entry with `type: POSTGRES`
> ([Additional databases and stores](databases.md)), its password a `${secret:<id>}`.

**Boundary.** `additionalApps` defines a *container* on the namespace network. It does
**not** auto-wire an HTTP route through the Citeck proxy, nor publish ECOS webapp
cloud-config / `dataSources` / webapp-properties — those remain the job of the built-in
Citeck-app generators (the `webapps:` section). A self-registering service (like the EDI
sim, via ZooKeeper) needs none of that; a plain HTTP app that must be reachable at a
proxy path is out of `additionalApps`' scope. The one entry the launcher does wire is
the one a workspace `contentStorage.s3` block names: on a namespace that stores its
content in S3, ecos-content waits for it and is connected to it once — see
[Content storage](content-storage.md).

## A database in the stand's postgres (`postgresDatabases`)

Launcher 2.20+. An entry that keeps its data in PostgreSQL can use the stand's own
`postgres` instead of a cluster of its own:

```yaml
additionalApps:
  - name: litellm
    kind: THIRD_PARTY
    postgresDatabases: [litellm]
    environments:
      DATABASE_URL: postgresql://litellm:litellm@${PG_HOST}:${PG_PORT}/litellm
```

* Each name becomes a database **and** a role of that name whose password is the
  name too — the init action the platform runs for its webapps' datasources
  (`/init_db_and_user.sh <name>`). The database is owned by `postgres` with
  privileges granted to the `<name>` role (which is not a `SUPERUSER`). The
  database is dumped, migrated and rolled back together with the platform's own
  (`citeck deps upgrade postgres`), and is visible in pgAdmin like any other.
* Applies only to **untyped** (raw container) entries: typed entries (`type:
  POSTGRES`, `QDRANT`, `SEAWEEDFS`) ignore `postgresDatabases`.
* The entry gets `dependsOn: [postgres]` by itself.
* The database is created on **every stand of the workspace**, whether the service
  runs there or not — the rule Keycloak's database follows. The init actions of
  `postgres` feed its deployment hash, so a database that came and went with its
  service would recreate `postgres` each time a feature is switched; this way
  switching the service on or off leaves `postgres` alone, at the price of an empty
  database where the service is off. Database names across all `additionalApps`
  entries are sorted and deduplicated globally so entry order does not alter
  `postgres`'s deployment hash. A workspace update that **adds** such an entry
  recreates `postgres` once on every stand, as a new platform database does.
* Names are lower-case identifiers (`^[a-z][a-z0-9_]{0,62}$`). PostgreSQL's own
  reserved names (`postgres`, `template0`, `template1`, `public`, `none`), the
  platform's `citeck_` prefix and PostgreSQL's `pg_` prefix are refused. An entry
  asking for an invalid name or carrying a malformed `postgresDatabases` YAML
  shape (for example a scalar instead of a list) is **ignored as a whole** with a
  warning in the log; the rest of `additionalApps` and the workspace config stay
  intact.
* The password follows the platform convention, as for every webapp database: the
  stand's `postgres` is reachable only from the namespace network (and from
  `127.0.0.1` in desktop mode). A service that needs a secret password keeps its
  own `type: POSTGRES` cluster.

Older launchers do not read the key: the service starts against a database that
does not exist. Ship such an entry only in a bundle that requires 2.20 (or behind
an experimental feature that says so).

## Companion settings for webapps (`webapps[].companions`)

Launcher 2.20+. A workspace webapp entry can declare `companions:` — environment
variables and `dependsOn` targets that the webapp receives **only while a named
companion app is present in the generated namespace** (for example, pointing `ai`
and `rag` at an opt-in LiteLLM gateway without hardcoding service keys in the
launcher):

```yaml
webapps:
  - id: ai
    aliases: [EcosAiApp]
    companions:
      litellm:
        environments:
          SPRING_AI_OPENAI_BASEURL: http://litellm:4000
          SPRING_AI_OPENAI_APIKEY: sk-${secret:litellm-master-key}
        dependsOn: [litellm]
```

* **Target scope:** `companions` applies only to generated bundle webapps listed
  in `webapps:` (never to infrastructure containers or `additionalApps`). When
  `webapps:` contains duplicate entries with the same `id`, the first entry wins.
  YAML decoding is total: a malformed `companions` section or an unusable
  companion block (wrong type for `environments` or `dependsOn`) logs a warning
  and drops only that section or block, preserving the webapp and the rest of the
  workspace config.
* **Environment precedence:** `namespace.yml` (`webapps.<id>.environments`) wins,
  followed by active `webapps[].companions.<name>.environments` (applied in
  alphabetical order of companion name when multiple companions are active), then
  `webapps[].defaultProps.environments`, then `defaultWebappProps.environments`.
  A per-app `citeck edit` patch still overlays all of them.
* **Secrets (`${secret:<id>}`):** keys already overridden in `namespace.yml` are
  skipped **before** template/secret resolution, so an unresolved secret in an
  overridden key does not block the rest of the companion block. If any remaining
  key in a companion block references an unresolved secret, that block is skipped
  whole with an error log while the webapp stays running on its own configuration.
  If `defaultProps.environments` references an unresolved `${secret:<id>}` and a
  surviving companion (whose own secrets and `dependsOn` chain survive) overrides
  that key with an available value, the webapp and its dependents are kept rather
  than excluded; if the companion is absent, excluded, or pruned, it wires nothing
  and the webapp's own unresolved secret excludes it as usual.
* **`dependsOn` and cycles:** companion `dependsOn` entries are wired on the final
  surviving app set. A target absent from the namespace is skipped (it never
  prunes the webapp), and a self-dependency (`dep == webapp id`) or any edge that
  would close a direct or transitive `dependsOn` cycle is skipped with a warning.
  Listing the webapp itself as its own companion is ignored with a warning.
* **Behavior with `detachedApps`:** companion wiring is governed by whether the
  companion app is **generated** in the namespace (`ctx.Applications`), not by
  whether it is detached (`detachedApps`), and companions are not registered as
  gating apps (`GatingApps`). While a companion lives under `experimental:`,
  stands that did not opt in do not generate it at all; if the module later
  graduates to a regular bundle entry and is held back only via `detachedApps`,
  note that companion settings will still apply to the webapp (adjust
  `detachedApps` or graduation rollout accordingly).
* **Boundary (`cloudConfig` / IDE):** `webapps[].companions` modifies only
  container `environments` and `dependsOn`. It does **not** merge `cloudConfig`
  or alter what `CloudConfigServer` serves to a webapp started locally from an
  IDE.

## Localized link tooltips (`links[].descriptions`)

Launcher 2.20+. A workspace `links[]` entry can provide per-language hover
tooltips via `descriptions:` (`LocalizedText`) alongside the plain `description:`
field:

```yaml
links:
  - name: LiteLLM
    url: http://127.0.0.1:14000/ui
    description: "LLM gateway"
    descriptions:
      ru: "LLM-шлюз"
      en: "LLM gateway"
```

* `description` remains a plain string so launchers older than 2.20 continue
  loading the workspace config unchanged.
* `GET /api/v1/namespace` resolves `description` per request locale and omits the
  raw `descriptions` map from the JSON response. Resolution order: single-language
  scalar in `descriptions` → normalized request locale (`ru-RU` → `ru`) → `"en"`
  in `descriptions` → plain `description` → first alphabetical key in
  `descriptions` (only when plain `description` is empty). The Web UI refetches
  `GET /api/v1/namespace` when the user switches language so tooltips update
  immediately without reloading the page.

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

## Experimental (beta) modules

An entry **without `image:`** whose name the bundle lists only under a feature of its
top-level `experimental:` section is generated **only for a namespace that switched that
feature on** (`namespace.yml` `experimentalFeatures:`). Everywhere else the bundle "names no
image" for it, and the entry is not generated — the ordinary rule above. The entry
itself needs nothing special: it configures the module, the release decides that it
exists and that it is beta. See [experimental-features.md](experimental-features.md).
