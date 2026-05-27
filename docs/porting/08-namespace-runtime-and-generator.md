# 08 — NamespaceRuntime, ApplicationDef, Generator

Самый объёмный домен. Это сердце launcher'а — что нужно реплицировать байт-в-байт чтобы существующие deployments не сломались на апгрейде.

## 1. Концепции

### 1.1 App

App = одиночный deployable service (один Docker контейнер). На design time описывается `ApplicationDef` (image, ports, env, volumes, probe, resources, init containers). Apps **не хранятся как persistent entities**; они генерятся on demand `NamespaceGenerator`'ом из combination bundle + namespace overrides.

### 1.2 Namespace

Namespace = named group of Apps deployed together на shared Docker bridge network. Roughly = docker-compose project. Каждый namespace персистится как `NamespaceConfig` entity (через `EntitiesService`). На runtime governed by `NamespaceRuntime`. FS working dir: `{workspace-dir}/ns/{namespaceId}/`.

### 1.3 Workspace

См. §05.

### 1.4 `AppName`

Файл: `core/namespace/AppName.kt`. Plain Kotlin object с `const val` string constants. Известные имена:

- **Community**: `"proxy"`, `"gateway"`, `"eproc"`, `"emodel"`, `"uiserv"`, `"eapps"`, `"history"`, `"notifications"`, `"transformations"`
- **Enterprise**: `"content"`, `"integrations"`, `"edi"`, `"ai"`, `"stt-sidecar"`
- **Other Citeck**: `"ecom"`, `"service-desk"`, `"ecos-project-tracker"`, `"alfresco"`
- **Infrastructure**: `"postgres"`, `"pgadmin"`, `"zookeeper"`, `"mongo"`, `"mailhog"`, `"onlyoffice"`, `"rabbitmq"`, `"keycloak"`

Эти имена используются verbatim как Docker container hostnames (`withHostName(appDef.name)` в `AppStartAction`). Внутри namespace все контейнеры на shared bridge network, резолвят друг друга directly по app name.

### 1.5 `NamespaceRef`

Plain data class с двумя string fields: `workspace`, `namespace`. `toString()` → `"$workspace:$namespace"`. Имеет `withWorkspace(workspace)` helper. Используется везде как primary compound key для Docker label filtering и naming.

---

## 2. `ApplicationDef` — App schema

`core/appdef/ApplicationDef.kt`. Immutable data class (builder pattern, Jackson `@JsonDeserialize`).

| Field | Type | Описание |
|---|---|---|
| `name` | `String` | App identifier; Docker container hostname. Validate'ится через `NameValidator` |
| `image` | `String` | Full Docker image (`nexus.citeck.ru/community/ecos-gateway:2.x`) |
| `environments` | `Map<String, String>` | Env vars `KEY=VALUE`. Insertion-ordered (`LinkedHashMap`) |
| `cmd` | `List<String>?` | Optional override entrypoint/command. `null` = image default |
| `ports` | `List<String>` | Docker format: `"8080:8080"`, `"8080:8080/tcp"` |
| `volumes` | `List<String>` | Volume specs. Source-format detection: `./...` = relative file/dir, no-separator = named volume (auto-scoped), absolute = bind-mount |
| `volumesContentHash` | `String` | SHA-256 xxh3 hash контента всех bind-mounted файлов (`./`-prefixed). При изменении hash launcher рекреатит контейнер даже если image tag не менялся |
| `initActions` | `List<AppInitAction>` | Post-start in-container commands (см. §6) |
| `dependsOn` | `Set<String>` | App names, которые должны достичь `RUNNING` перед transition из `DEPS_WAITING` в `READY_TO_START` |
| `startupConditions` | `List<StartupCondition>` | Ordered list conditions; все должны pass после container start перед `RUNNING` |
| `livenessProbe` | `AppProbeDef?` | Liveness probe (определена в schema, **не active polled** runtime loop'ом; часть hash для change detection) |
| `resources` | `AppResourcesDef?` | Memory limit/request |
| `kind` | `ApplicationKind` | Classification — pull behavior + Keycloak config |
| `shmSize` | `String` | Shared memory size (`/dev/shm`). Default `"64m"`. Postgres = `"128m"` |
| `initContainers` | `List<InitContainerDef>` | Ephemeral containers run-to-completion перед main container start |

**`ApplicationDef.getHash()`** — SHA-256 над всеми полями including `volumesContentHash`. Этот hash стамп'ится на контейнере как label `citeck.launcher.app.hash`. На start any existing container, чей hash не match'ит — stopped & replaced.

### 2.1 `ApplicationKind`

`core/appdef/ApplicationKind.kt`:

| Value | Meaning |
|---|---|
| `CITECK_CORE` | Core Citeck microservices (`proxy`, `gateway`, `eproc`, `emodel`, `uiserv`, `eapps`, `history`, `notifications`, `transformations`). `isCiteckApp() = true`. Pull skip'ается для images без `/` (locally built) |
| `CITECK_CORE_EXTENSION` | Core extensions (`integrations`, `edi`, `content`). Same pull rules |
| `CITECK_ADDITIONAL` | Optional Citeck (`ai`, `stt-sidecar`, `alfresco`, `eapps`-init-containers, `alf-solr`). Same pull rules |
| `THIRD_PARTY` | Infrastructure (`rabbitmq`, `postgres`, `zookeeper`, `keycloak`). Image — assumed always present или public registry. `pullImageIfPresent` forced to `false` независимо от tag |

### 2.2 `AppProbeDef`

`core/appdef/AppProbeDef.kt`:

```kotlin
data class AppProbeDef(
    val exec: ExecProbeDef? = null,           // mutually exclusive with http
    val http: HttpProbeDef? = null,
    val initialDelaySeconds: Int = 5,
    val periodSeconds: Int = 10,
    val failureThreshold: Int = 10_000,        // effectively infinite retries
    val timeoutSeconds: Int = 5
)

data class ExecProbeDef(val command: List<String>)        // run inside container
data class HttpProbeDef(val path: String, val port: Int)  // GET http://127.0.0.1:{hostPort}{path}
```

**Важно**: `HttpProbeDef.port` — **container port**, не host port. `AppStartAction.httpProbeCheck` lookup'ит host port через inspecting container's port bindings. Если container port не published (no host port mapping) — probe возвращает `false` immediately. Это означает каждый app с HTTP startup probe **должен** иметь matching port binding публикующий probe port в host.

`failureThreshold` default `10_000` итераций означает startup effectively никогда не timeout'ит probe-side; outer 240-second running-state wait + thread interruption — actual hard limits.

### 2.3 `AppResourcesDef`

`core/appdef/AppResourcesDef.kt`:

```kotlin
data class AppResourcesDef(val limits: LimitsDef)
data class LimitsDef(val memory: String = "")    // "1g", "512m", "256m"
```

Memory parse'ится `MemoryUtils.parseMemAmountToBytes`. Когда set — и `withMemory(bytes)` и `withMemorySwap(bytes)` передаются в Docker host config — swap = memory limit, эффективно disabling swap для всех managed containers.

### 2.4 `InitContainerDef`

`core/appdef/InitContainerDef.kt`. Fields: `image`, `environments`, `volumes`, `kind`, `cmd?`.

**Семантика**: run-to-completion перед main container start. Memory hard-capped `100m`. Timeout 30 секунд (`waitContainer.awaitStatusCode(30, TimeUnit.SECONDS)`). Non-zero exit → fatal error. `RestartPolicy.noRestart()`. Volumes — те же `./`-relative и named-volume правила что и main container.

Init container может писать в named volume, который main container потом mount'ит read-only. Так deploy'ятся ECOS app artifacts: multiple `InitContainerDef`'ов, один per `citeckApp` в bundle, копируют ECOS app `.jar` файлы в `./app/eapps/ecos-apps` bind-mount directory (см. `generateWebapp` для `eapps`).

### 2.5 `StartupCondition`

`core/appdef/StartupCondition.kt`:

```kotlin
data class StartupCondition(
    val probe: AppProbeDef? = null,
    val log: LogStartupCondition? = null
)
data class LogStartupCondition(
    val pattern: String,                  // regex
    val timeoutSeconds: Int = 60
)
```

`ApplicationDef` несёт `List<StartupCondition>`. Все conditions должны pass последовательно. `waitStartup` в `AppStartAction` сначала ждёт до 240 секунд для container's `running` state = `true`, потом итерирует conditions. Condition имеет либо `probe` (poll loop), либо `log` (regex match в log stream с timeout). `null` condition — no-op.

**Примеры**:
- Postgres: log match `".*database system is ready to accept connections.*"` + exec probe `psql -U postgres -d postgres -c 'SELECT 1' || exit 1`
- Keycloak: single exec probe `["bash", "/healthcheck.sh"]`
- ECOS webapps: HTTP probe `/management/health` на server port

---

## 3. `NamespaceConfig`

`core/namespace/NamespaceConfig.kt`. Persisted namespace entity (через `EntitiesService`). Builder pattern + recursive JSON merge для `WebappProps`.

| Field | Type | Default | Описание |
|---|---|---|---|
| `id` | `String` | `""` | Unique в пределах workspace. Used as namespace segment `NamespaceRef` |
| `name` | `String` | `""` | Display name (`"Citeck #1"`) |
| `snapshot` | `String` | `""` | Snapshot id для import на create. Non-empty → snapshot скачивается и импортится в Docker volumes перед runtime registration |
| `template` | `String` | `""` | Namespace template id из `WorkspaceConfig.namespaceTemplates`. Non-empty → `setDetachedApps` вызывается с `template.detachedApps` |
| `authentication` | `AuthenticationProps` | `BASIC / {admin, fet}` | Type + users |
| `bundleRef` | `BundleRef` | empty | Pointer на bundle (repo + key) |
| `pgAdmin` | `PgAdminProps` | enabled=true | Override pgAdmin image; disabled → pgAdmin не генерится |
| `mongodb` | `MongoDbProps` | image="" | Optional MongoDB image override |
| `citeckProxy` | `ProxyProps` | image="" | Optional proxy image override |
| `webapps` | `Map<String, WebappProps>` | empty | Per-app overrides |

### 3.1 `AuthenticationProps`

Два режима:

**`BASIC`** — nginx basic auth:
- Users в `Set<String>` (default `{"admin", "fet"}`)
- Passwords = usernames
- Proxy получает `BASIC_AUTH_ACCESS=admin:admin,fet:fet`

**`KEYCLOAK`** — OIDC через Keycloak using `ecos-app` realm:
- Proxy получает `EIS_TARGET`, `EIS_SCHEME`, etc.
- Lua OIDC script bind-mounted

### 3.2 `WebappProps` + Merge Model

`WebappProps` несёт per-webapp overrides:
- `enabled?: Boolean`
- `image: String`
- `cloudConfig: DataValue` (arbitrary YAML)
- `environments: Map<String, String>`
- `debugPort: Int?`
- `heapSize: String?`
- `memoryLimit: String?`
- `serverPort: Int?`
- `javaOpts: String?`
- `dataSources: List<DataSourceProps>`
- `springProfiles: List<String>`

**Merge order** (lowest → highest priority) в `generateWebapp`:
1. `workspaceConfig.defaultWebappProps` — workspace-wide
2. `workspaceConfig.webappsById[name]?.defaultProps` — workspace per-app
3. `namespaceConfig.webapps[name]` — namespace per-app

`apply(other: WebappProps?)` использует Jackson serialization: serialize `this` в JSON, потом для каждого non-default поля `other` deep-merge value (recursive object merge, scalar replacement). Object fields (`cloudConfig`, `dataSources`) — **deep-merged**, не replaced.

### 3.3 DataSources

Каждый entry в `WebappProps.dataSources` описывает JDBC или MongoDB URL. Generator парсит scheme (`jdbc:` или `mongodb:`), извлекает db name, переписывает URL на internal Docker hostname (`postgres:5432` или `mongo:27017`). Когда `cloudConfig` flag set — URL переписывается на `localhost:14523` (postgres) или `localhost:27017` (mongo) для external tools.

---

## 4. `NamespacesService`

`core/namespace/NamespacesService.kt`. Workspace-scoped service. **Не прямой CRUD API**; вместо этого регистрирует entity definitions в `EntitiesService` и слушает entity lifecycle events.

### 4.1 Public API

| Method/Property | Описание |
|---|---|
| `init(services)` | Создаёт entity def, регистрирует существующие namespaces как runtimes, wires event listeners + selection listener |
| `getRuntime(id)` | `NamespaceRuntime` для namespace id; throws если not found |
| `nsAppsGenerator` | `NamespaceGenerator` singleton для этого workspace |
| `namespaceRuntimes` | `ConcurrentHashMap<String, NamespaceRuntime>` — все live runtimes |

### 4.2 Lifecycle events

- **Entity created**: optionally import snapshot в volumes, потом `registerNsRuntime`. Если template specified — `setDetachedApps`. Потом select new namespace as active.
- **Entity updated**: push new `NamespaceConfig` в runtime's `namespaceConfig` reactive property. Runtime regenerates namespace на next tick если active.
- **Entity deleted**: stop runtime (wait до 1 минуты для `STOPPED`), `dispose()`, delete DB repo, delete namespace filesystem directory, delete все Docker volumes для namespace ref, re-select другой namespace если есть.
- **Selection changed**: `setActive(false)` для previously selected + `setActive(true)` для new. Только active runtime запускает свой runtime thread.

### 4.3 Persistence

NamespaceConfig'ы — через `EntitiesService` → H2-backed `DataRepo`. Namespace id — `String`. Runtime state (status, detached apps, edited apps, cached bundle def) — в отдельном `DataRepo` scope `"namespace-runtime-state"` с key pattern `"$workspace:$namespace"`.

FS namespace dir: `{workspace-dir}/ns/{namespaceId}/rtfiles/` — где generated + user-edited files.

---

## 5. Namespace Generation

### 5.1 `NsGenContext`

`core/namespace/gen/NsGenContext.kt`. Mutable context, передаваемый через все generator methods:

| Field | Type | Описание |
|---|---|---|
| `namespaceConfig` | `NamespaceConfig` | Namespace-level config |
| `bundle` | `BundleDef` | Bundle (image tags для всех apps) |
| `workspaceConfig` | `WorkspaceConfig` | Workspace-wide settings |
| `detachedApps` | `Set<String>` | App names currently detached |
| `files` | `MutableMap<String, ByteArray>` | Output file map (relative paths → bytes). Pre-seeded с classpath `appfiles/` |
| `applications` | `MutableMap<String, ApplicationDef.Builder>` | Accumulator generated apps. `LinkedHashMap` — order preserved |
| `portsCounter` | `AtomicInteger` | Port allocator. Стартует с `17020`. Increment для каждого webapp без explicit `serverPort`. Используется для Zookeeper admin port + Alfresco app port |
| `cloudConfig` | `MutableCloudConfig` | Per-app cloud config accumulator; push'ится в `CloudConfigServer` после generation |
| `links` | `MutableList<NamespaceLink>` | Ordered list links для UI |

**Companion constants** — все internal Docker hostnames + ports:

```kotlin
PG_HOST = "postgres"      PG_PORT = 5432
ZK_HOST = "zookeeper"     ZK_PORT = 2181
RMQ_HOST = "rabbitmq"     RMQ_PORT = 5672
MONGO_HOST = "mongo"      MONGO_PORT = 27017
MAILHOG_HOST = "mailhog"
ONLYOFFICE_HOST = "onlyoffice"
KK_HOST = "keycloak"
JWT_SECRET = "my-secret-key-which-should-be-changed-in-production-and-be-base64-encoded"
```

### 5.2 Generation sequence

`NamespaceGenerator.generate()` вызывает sub-generators в fixed order:

1. **`generateMailhog`** — image `"mailhog/mailhog:v1.0.1"`, ports `1025:1025` + `8025:8025/tcp`, limit `128m`.
2. **`generateMongoDb`** — image из `namespaceConfig.mongodb.image` или `"mongo:4.0.2"`, port `27017:27017`, volume `mongo2:/data/db`, limit `512m`.
3. **`generatePgAdmin`** — skipped если `pgAdmin.enabled == false`. Port `5050:80`, env `PGADMIN_DEFAULT_EMAIL=admin@admin.com`, `PGADMIN_DEFAULT_PASSWORD=admin`, volumes `pgadmin2:/var/lib/pgadmin` + `./pgadmin/servers.json:/pgadmin4/servers.json`.
4. **`generatePostgres`** — image из `workspaceConfig.postgres.image`, port `14523:5432`, shm `128m`, volumes для data + init script + конфигов. Startup: log match + exec probe.
5. **`generateZookeeper`** — image из `workspaceConfig.zookeeper.image`, ports `2181:2181` + `{counter}:8080`, volume `zookeeper2:/citeck/zookeeper`. Один `InitContainerDef` (`registry.citeck.ru/community/launcher-utils:1.0`) `mkdir -p /zkdir/data /zkdir/datalog`.
6. **`generateRabbitMq`** — image `"rabbitmq:4.1.2-management"`, ports `5672:5672` + `15672:15672`, volume `rabbitmq2:/var/lib/rabbitmq`, env `RABBITMQ_DEFAULT_USER=admin`, `RABBITMQ_DEFAULT_PASS=admin`, limit `256m`.
7. **`generateKeycloak`** — ВСЕГДА добавляет init action в postgres для создания `citeck_keycloak` DB, но добавляет actual keycloak app только если `authentication.type == KEYCLOAK`. Image из `workspaceConfig.keycloak.image`. Передаёт `--import-realm` и mount'ит `./keycloak/ecos-app-realm.json` + `./keycloak/healthcheck.sh`. Startup: exec probe `["bash", "/healthcheck.sh"]`.
8. **`generateAlfresco`** — только если `workspaceConfig.alfresco.enabled` И bundle содержит alfresco entry. Добавляет отдельный postgres (`alf-postgres`, image `"postgres:9.4"`, port `54329:5432`) с init actions для `alfresco` + `alf_flowable` DBs. Добавляет `alf-solr` app с image `"nexus.citeck.ru/ess:1.1.0"`. Alfresco port — из `portsCounter`, limit explicitly не set.
9. **Webapp loop** — для каждого app в `bundle.applications`, который registered в `workspaceConfig.webappsById` — вызывает `generateWebapp`. Это main path для ECOS микросервисов.
10. **`generateSttSidecar`** — только если `ai` app генерился и не detached. Image из `workspaceConfig.sttSidecar.image` или `bundle.applications["stt-sidecar"]?.image`. Port из `workspaceConfig.sttSidecar.port`. HTTP startup probe на `/health`. Volume `stt_models:/root/.cache/gigaam`.
11. **`generateProxyApp`** — ВСЕГДА LAST. Читает gateway's `SERVER_PORT` из уже-built applications map. Конфигурит basic auth или OIDC в зависимости от `authentication.type`. Binds port `80:80`. HTTP startup probe `/eis.json` на port 80.
12. **`generateOnlyOffice`** — image из `workspaceConfig.onlyoffice.image`, ports `8070:80/tcp` + `443/tcp`, env `JWT_ENABLED=false`, `ALLOW_PRIVATE_IP_ADDRESS=true`.

После loop'а — unconditionally Spring Boot Admin link (`http://localhost/gateway/eapps/admin/wallboard`), потом `context.links` сортируется по `order: Float`.

### 5.3 Port allocation

**Static**:
- Postgres: `14523:5432`
- RabbitMQ: `5672:5672`, `15672:15672`
- Zookeeper: `2181:2181`
- MongoDB: `27017:27017`
- PgAdmin: `5050:80`
- MailHog: `1025:1025`, `8025:8025`
- Proxy: `80:80`
- OnlyOffice: `8070:80`, `443`
- Alfresco Postgres: `54329:5432`
- Alfresco Solr: `38080:8080`

**Dynamic** (`portsCounter` от `17020`): Zookeeper admin (8080), Alfresco main (8080), потом все ECOS webapp `SERVER_PORT` values в bundle order (если не override'нуты `WebappProps.serverPort`). Каждый атомарно increment'ит counter.

### 5.4 `NamespaceGenResp`

```kotlin
class NamespaceGenResp(
    val applications: List<ApplicationDef>,        // все generated apps (не detached)
    val files: Map<String, ByteArray>,             // read-only files to stage
    val cloudConfig: CloudConfig,                  // per-app spring cloud config
    val links: List<NamespaceLink>,                // sorted UI links
    val dependsOnDetachedApps: Set<String>         // = {onlyoffice, ai, stt-sidecar}
)
```

`dependsOnDetachedApps` hardcoded — `setOf(AppName.ONLYOFFICE, AppName.AI, AppName.STT_SIDECAR)`. Когда any of these detached, proxy должен regenerate'нуться чтобы remove `ONLYOFFICE_TARGET` или `AI_TARGET` env vars.

### 5.5 `GlobalLinks`

`core/namespace/gen/GlobalLinks.kt`. `GlobalLinks.LINKS` всегда добавляет два entries (независимо от namespace state):

| Name | URL | Category | Order |
|---|---|---|---|
| `"Documentation"` | `https://citeck-ecos.readthedocs.io/` | `"Resources"` | 200f |
| `"AI Documentation Bot"` | `https://t.me/haski_citeck_bot` | `"Resources"` | 201f |

Оба `alwaysEnabled = true`. Mix'аются в UI layer, не generator'ом.

### 5.6 `NamespaceLink`

```kotlin
class NamespaceLink(
    val url: String,
    val name: String,
    val description: String,
    val icon: String,           // path to icon SVG resource
    val order: Float,           // lower = earlier
    val category: String? = null,
    val alwaysEnabled: Boolean = false
)
```

### 5.7 Volume naming gotcha

Plain имена в `addVolume()` (`"postgres2"`, `"rabbitmq2"`, `"mongo2"`, `"zookeeper2"`) **не содержат** namespace prefix. Runtime auto-scope'ит их в `AppStartAction.createVolumeIfNotExists()` через `DockerConstants.getVolumeName(originalName, nsRef)`, который produces:

```
citeck_volume_{originalName}_{namespaceId}_{workspaceId}
```

Тот же generator output можно reuse'ить по namespaces; scoping — на container-create time, не generation time.

---

## 6. `AppInitAction`

`core/namespace/init/AppInitAction.kt`. Sealed class с единственным subtype `ExecShell(command: String)`, serialized как `{"type": "exec-shell", "command": "..."}`.

**Когда выполняется**: после container started и все `startupConditions` passed. Init action runs **внутри уже-running container** через `docker exec`. Это post-start hook, не pre-start или sidecar.

**Как выполняется**: `AppStartAction` zовёт `docker exec` с `/bin/sh -c {command}`. Output streamed + logged. Timeout: 10 секунд; warning логируется если не complete'ит, но execution continues.

**Typical use cases**:
- Postgres: `ExecShell("/init_db_and_user.sh citeck_keycloak")` — creates Keycloak DB + user внутри running postgres container. Similarly для каждого webapp datasource DB + Alfresco DBs.
- Proxy (Keycloak mode): два `ExecShell` calls — sed для patch nginx config для Keycloak path rewrite, потом `nginx -s reload`.

**Idempotency**: `init_db_and_user.sh` checks DB существование перед creation. Другие actions — не idempotent by default.

**Failure handling**: если не complete'ит в 10s — WARN log, no exception, no retry.

---

## 7. `NamespaceRuntime`

`core/namespace/runtime/NamespaceRuntime.kt`. Центральный orchestrator. Запускает dedicated background thread per active namespace.

### 7.1 Public API

| Method/Property | Описание |
|---|---|
| `updateAndStart(forceUpdate)` | Enqueue `StartNsCmd(forceUpdate)`. Trigger regeneration + start all non-detached apps. `forceUpdate=true` — forces git bundle update; иначе cached allowed |
| `stop()` | Enqueue `StopNsCmd`. → `STOPPING`, stop all apps |
| `setActive(active)` | Activate/deactivate runtime thread. Только selected namespace's runtime active. On activation: trigger `generateNs(ALLOWED)` + spawn runtime thread. On deactivation: interrupt thread. `ReentrantLock` protected |
| `setDetachedApps(set)` | Replace entire detached set. Если `dependsOnDetachedApps` changed — schedule `RegenerateNsCmd` |
| `addDetachedApp(name)` | Add one app to detached. Called когда user manually stops app |
| `resetAppDef(name)` | Clear edited override; restore generated def |
| `updateAppDef(before, after, locked)` | Persist user edit. `locked=true` — app не обновится future regenerations |
| `pushEditedFile(path, content)` | Replace runtime file user content; update volume content hash для affected apps |
| `resetEditedFile(path)` | Reset runtime file к generated |
| `nsStatus` | `MutProp<NsRuntimeStatus>` |
| `appRuntimes` | `MutProp<List<AppRuntime>>` |
| `namespaceStats` | `MutProp<NamespaceStats>` |
| `namespaceGenResp` | `MutProp<NamespaceGenResp?>` |

### 7.2 `NsRuntimeStatus`

```kotlin
enum class NsRuntimeStatus { STOPPING, STOPPED, STARTING, STALLED, RUNNING }
```

- `STOPPED`: все apps `STOPPED`, network deleted.
- `STOPPING`: stop command received, apps being stopped.
- `STARTING`: at least one app в start-phase state.
- `RUNNING`: все non-stopped apps `RUNNING`.
- `STALLED`: at least one app в stalled state (`PULL_FAILED`, `START_FAILED`, `STOPPING_FAILED`). Recover'ится в `STARTING` или `STOPPING` когда all stalled cleared.

### 7.3 `AppRuntimeStatus` state machine

```
STOPPED
  │
  │ start() called
  ▼
READY_TO_PULL ──────────── (image already present, !pullIfPresent) ──┐
  │                                                                   │
  │ runtime thread picks up                                            │
  ▼                                                                    │
PULLING ──────── fail ─────▶ PULL_FAILED (stalled)                    │
  │                                                                    │
  │ pull success                                                        │
  ▼ ◀───────────────────────────────────────────────────────────────────┘
DEPS_WAITING (if dependsOn apps not yet RUNNING)
  │
  │ deps satisfied
  ▼
READY_TO_START
  │
  │ runtime thread picks up
  ▼
STARTING ────── fail ─────▶ START_FAILED (stalled)
  │             │
  │             └── DockerImageNotFound ─────▶ READY_TO_PULL
  │ success
  ▼
RUNNING
  │
  │ stop() called
  ▼
READY_TO_STOP
  │
  │ runtime thread picks up
  ▼
STOPPING ────── fail ─────▶ STOPPING_FAILED (stalled)
  │
  │ success
  ▼
STOPPED
```

Helpers:
- `isStoppingState()` = `READY_TO_STOP || STOPPING || STOPPED`
- `isStartingState()` = `READY_TO_PULL || PULLING || DEPS_WAITING || READY_TO_START || STARTING || RUNNING`
- `isStalledState()` = `PULL_FAILED || START_FAILED || STOPPING_FAILED`

### 7.4 Runtime thread

Работает пока `isActive.get() == true` и `activeStateVersion` matches. Loop'ит `runtimeThreadAction()`. Если no work — wait'ит на `runtimeThreadSignalQueue` с exponential backoff: 1, 2, 3, 5, 8, 10 секунд. Каждые 30 секунд inactivity — force status re-evaluation. Thread также flushed (через queue `offer`) когда any app status changes.

### 7.5 Concurrency model

- Все app state transitions driven by single runtime thread через `runtimeThreadAction()`. Avoids most race conditions.
- `appRuntimeStatus.setValue(newStatus, statusVersion)` — versioned CAS-style update: version check prevents stale async callback от overwriting newer status.
- Image pull + container start/stop — каждый Promise (async на `ActionsService` thread pool). Promise callbacks post back trigger `flushRuntimeThread()`.
- Global `Semaphore(4)` в `AppImagePullAction` — concurrent pulls limit.
- `DEPS_WAITING` re-checked каждые 20 секунд как fallback (если `RUNNING` transition missed).

### 7.6 `NsRuntimeCmd`

Three sealed subclasses:
- `StartNsCmd(forceUpdate: Boolean)` — regeneration + start all non-detached.
- `StopNsCmd` (singleton).
- `RegenerateNsCmd` (singleton) — re-generate без start.

Adjacent commands в queue collapse:
- `Start` после `Regenerate` → `Start`
- `Stop` после `Start` → `Stop`

Queue capacity: 100 system commands + 50 user commands.

### 7.7 `NsRuntimeFiles`

`core/namespace/runtime/NsRuntimeFiles.kt`. Manage'ит FS на `{ns-dir}/rtfiles/`.

Responsibilities:
- `applyGeneratedFiles(map)`: write generated files, remove stale. `TreeMap<Path, String>` SHA-256 hashes maintained.
- `applyEditedFile(path, bytes)`: store user-edited content в DB (`changedRtFiles` repository) + write to disk + update hash.
- `resetEditedFile(path)`: remove DB entry + revert disk к generated.
- `getPathsContentHash(paths)`: xxh3 hash all file content hashes для given path prefixes. Used для `volumesContentHash`.
- Paths starting `./` resolve against `filesDir`. Absolute paths outside `filesDir` silently ignored (security guard).
- `.sh` files автоматически made executable on write.

### 7.8 `NsFileInfo`

```kotlin
data class NsFileInfo(val path: Path, val hash: String, val edited: Boolean)
```

Lightweight descriptor для file в runtime files dir. `edited = true` если user overrode generated content.

### 7.9 Detach/Attach desired-state model

`detachedApps` set implement'ит desired-state model analogous к Kubernetes pod-level disabling:

- Add app в `detachedApps` = "desired stopped" signal. Runtime stop'ит app + не restart'ит.
- Remove app из `detachedApps` = "desired running" signal. На next generation cycle app started.
- `detachedApps` persisted в DB как `"manualStoppedApps"`. Survives restarts.
- `AppRuntime.stop(manual=true)` adds app в `detachedApps` + `READY_TO_STOP`.
- `detachedAppsToRemove` accumulated во время `READY_TO_PULL` transitions; apps re-entering start path removed из `detachedApps` (re-attached by being started).

---

## 8. Что важно для портёра

1. **`ApplicationDef.getHash()` — идемпотентность контракт**. Хеш стамп'ится на контейнере. Если Go runtime вычислит hash иначе (другой field ordering, другой serialization input), все existing контейнеры будут torn down + recreated на first start.
2. **`HttpProbeDef.port` — container port, не host**. Probe инспектит port bindings, hits `127.0.0.1:{hostPort}`. Нет container-DNS probe path.
3. **`failureThreshold = 10_000` × `periodSeconds = 10`** = potentially 27+ часов retry для slow-startup services (Alfresco). Сохранить или config'урировать.
4. **Init actions 10s timeout** — слабый. DB init scripts могут silently timeout. Go-порт лучше увеличить или сделать proper await.
5. **3-level WebappProps merge**: workspace-default → workspace-per-app → namespace-per-app. Jackson round-trip с deep merge. Реплицировать: scalar last-non-zero wins; object — deep-merge; template substitution (`TmplUtils.applyAtts`) на merged props (`{PG_HOST}` ресолвится из `NsGenContext.VARS`).
6. **`detachedApps` (= `manualStoppedApps`)** — single source of truth для desired state. Persist + restore. App в `detachedApps` НЕ должен start'ить даже если namespace started. Bundle update / config change → `dependsOnDetachedApps` triggers full regeneration.
7. **`editedApps` (= `STATE_EDITED_APPS`)** — user overrides. `editedAndLockedApps` — overrides survive bundle updates. Locked app's definition никогда не replaced regeneration. Оба persistence keys + same merge semantics в port.
8. **Volume naming auto-scope в runtime layer**. Generator pure-named, runtime tagging через `ORIGINAL_NAME` label — discovery contract. См. §09.
9. **`portsCounter` от 17020**. Динамические ports для webapps в bundle order. Если порядок изменится — container restart triggered (hash diff). Sticky port allocation по app name полезно но not implemented.
10. **`JWT_SECRET` hardcoded в `NsGenContext`** — development-only. Production hardening = TBD.

---

## 9. 2.x порт — landed deviations / additions (`6fe02d1`)

### 9.1 `EditedFileOverlay` (volume content hash invalidation)

Generator hashes mounted file content into `VolumesContentHash` (см. §2),
который stamp'ится на container как `citeck.launcher.app.hash` input. До
2026-05-27 `Generate()` hash'ал против embedded defaults из
`appfiles.GetFiles()`, поэтому UI-инициированные edits НЕ bump'или hash и
state machine оставлял stale container running.

Fix landed (Doubtful A):

- `GenerateOpts.EditedFileOverlay` — `map[appName]map[relPath][]byte` с
  user-edited содержимым. `internal/namespace/generator.go::Generate`
  hash'ит overlay поверх embedded defaults.
- `Runtime.EditedFileOverlay(volumesBase)` populates overlay из persisted
  `changedRtFiles` repo на reload.
- `readEditedFileOverlay()` запускается на daemon startup для inflate
  overlay из state.
- `SetFileEdited(edited=true)` enqueues `cmdRegenerate`, так что edit
  takes effect без manual reload (mirrors Kotlin
  `NamespaceRuntime.pushEditedFile`).

Tests: `TestGenerate_EditedFileOverlayChangesHash`,
`TestRuntime_EditedFileOverlay_ReadsDiskContent`,
`TestRuntime_EditedFileOverlay_EmptyWhenNoEdits`
(`internal/namespace/runtime_edited_files_test.go`).

### 9.2 Detached Alfresco excluded from `proxyTarget`

Generator's `proxyTarget` calculation теперь skip'ает Alfresco если он
detached (`manualStoppedApps` contains "alfresco"). Mirrors Kotlin
`NamespaceGenerator.kt` behavior (item 16). Без этого proxy пытался
проксировать на stopped Alfresco и отдавал 502.

### 9.3 `exec ` prefix on shell-form init actions

`internal/namespace/generator.go::buildInitActions` теперь префиксует
shell-form init actions через `exec ` (PID/signal parity — без `exec` шелл
становится отдельным процессом, SIGTERM не доходит до actual init script).
Mirrors Kotlin `AppStartAction` (item 24).

### 9.4 Pull retry fallback

`runtime_workers.go::runPullTask` — на ошибке pull делает до
`PullRetriesForExistingImage = 3` retries; если после retries image
локально присутствует (`docker image inspect` succeeds), startup
продолжается без повторного pull. Mirrors Kotlin
`RETRIES_COUNT_FOR_EXISTING_IMAGE` (item 17). Watchdog: 5-минутный stall
timeout (`pullStallTimeout`) с 30-секундным polling per attempt.

### 9.5 `AppInitAction.Trigger` removed

Dead field в Go-only типе `AppInitAction.Trigger` (никогда не имел соответствия
в Kotlin) удалён (item 25). Все callers использовали имплицитный
`OnStart` trigger.
