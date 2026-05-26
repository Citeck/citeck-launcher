# 05 — Workspaces, Bundles, CloudConfig

## 1. Workspaces

### 1.1 Концепция

Workspace = top-level configuration unit. Биндит launcher на remote Git репозиторий, содержащий runtime config (`workspace-v1.yml` или legacy `workspace.yml`). Одна установка launcher'а может управлять несколькими workspace'ами; ровно один "selected" в любой момент. Selected workspace определяет active namespace list, bundle repos, image repos, license pool, snapshot catalog.

Global (launcher-level) workspace id `"global"` — сентинел, используемый `EntitiesService` для launcher-scoped entities (workspace definitions сами). Приложение шипает hard-coded default workspace:

```kotlin
// core/workspace/WorkspaceDto.kt
WorkspaceDto.DEFAULT:
    id = "default"
    name = "Default Workspace"
    repoUrl = "https://github.com/Citeck/launcher-workspace.git"
    repoBranch = "main"
    repoPullPeriod = Duration.parse("PT6H")
    authType = AuthType.NONE
```

### 1.2 Lifecycle

1. **Created**: `WorkspacesService` слушает `EntityCreatedEvent<WorkspaceDto>`. При создании вызывает `loadWorkspaceConfig`, который клонит (или pull'ит) backing Git repo в `<AppDir>/ws/<workspaceId>/repo/`.
2. **Selected**: `LauncherServices.setWorkspace(id)` (line 139) конструирует `WorkspaceServices`, dispose'ит предыдущий, persist'ит в `DataRepo("launcher", "state")` под key `"selectedWorkspace"`, lazy re-init сервисов.
3. **Deleted**: `WorkspacesService` слушает `EntityDeletedEvent<WorkspaceDto>`. При удалении рекурсивно удаляет `<AppDir>/ws/<workspaceId>/`.
4. **Switched**: `LauncherServices.setWorkspace` — единая точка входа. Держит `thisLock` (`ReentrantLock`) во время swap для concurrent access.

### 1.3 `WorkspaceDto` vs `WorkspaceConfig`

`WorkspaceDto` (`workspace/WorkspaceDto.kt`) — **identity record**, хранится в БД через entity framework:

| Field | Type |
|---|---|
| `id` | `String` |
| `name` | `String` |
| `repoUrl` | `String` |
| `repoBranch` | `String` |
| `repoPullPeriod` | `Duration` |
| `authType` | `AuthType` |

`WorkspaceConfig` (`workspace/WorkspaceConfig.kt`) — **parsed YAML content** репозитория. Загружается on-demand `WorkspacesService.getWorkspaceConfig` и кэшируется в `ConcurrentHashMap<String, WorkspaceConfig>`. Cache invalidate'ится при txn rollback (через `doAfterRollback`) и при `GitUpdatePolicy.REQUIRED`. Содержит:

- `quickStartVariants: List<QuickStartVariant>`
- `imageRepos: List<ImageRepo>` (для авторизации в registries)
- `bundleRepos: List<BundlesRepo>`
- `webapps: Map<String, WebappConfig>` (per-app workspace defaults: `defaultProps`, `aliases`)
- `defaultWebappProps: WebappProps`
- `postgres: PostgresConfig` (image override)
- `keycloak: KeycloakConfig` (image override)
- `alfresco: AlfrescoConfig` (`enabled` + image + aliases)
- `onlyoffice: OnlyOfficeConfig`
- `sttSidecar: SttSidecarConfig` (image, port)
- `pgadmin: PgAdminConfig`
- `zookeeper: ZookeeperConfig`
- `citeckProxy: ProxyConfig` (aliases)
- `mongodb: MongoDbConfig`
- `licenses: List<LicenseInstance>`
- `snapshots: List<Snapshot>` (workspace-level shared snapshots с url+sha256+size)
- `namespaceTemplates: List<NamespaceTemplate>` (id, detachedApps)

### 1.4 Versioned config files

`WorkspacesService.loadWorkspaceConfig` итерирует от `CONFIG_VERSION_MAX = 1` до 0:
1. Сначала пытается `workspace-v1.yml`
2. Fallback на `workspace.yml` (v0)

Versions ниже `CONFIG_VERSION_MIN = 1` → hard error.

### 1.5 `WorkspaceEntityDef`

`workspace/WorkspaceEntityDef.kt` регистрирует `WorkspaceDto` как entity type:

```kotlin
EntityDef(
    idType = EntityIdType.String,
    typeId = "workspace",
    typeName = "Workspace",
    defaultEntities = [WorkspaceDto.DEFAULT],
    createForm = FormSpec(...)  // name, repoUrl, repoBranch, repoPullPeriod, authType
    editForm = null              // uses createForm для редактирования
)
```

FormSpec title `"Workspace"`. Точные label'ы и defaults (`WorkspaceEntityDef.kt:12-28`):

| Field key | Тип | Label | Default | Mandatory |
|---|---|---|---|---|
| `name` | `NameField` | `"Name"` | `""` | yes (≤50 chars) |
| `repoUrl` | `TextField` | `"Repo URL"` | `""` | yes |
| `repoBranch` | `TextField` | `"Repo Branch"` | `"main"` | yes |
| `repoPullPeriod` | `TextField` | `"Pull Period (ISO 8601)"` | `"PT2H"` | yes |
| `authType` | `SelectField` | `"Auth Type"` | `"NONE"` | yes; options = `[AuthType.NONE.displayName, AuthType.TOKEN.displayName]` |

⚠️ Default `repoPullPeriod` в форме = `"PT2H"`, что отличается от `WorkspaceDto.DEFAULT.repoPullPeriod = PT6H` (последний — литерал hard-coded default workspace).

`EntityRef` для workspace: `workspace@<id>`.

### 1.6 Git binding

`WorkspaceDto`'s repo поля передаются verbatim в `GitRepoProps`, потом в `GitRepoService.initRepo`. Auth secret id: `"ws:<workspaceId>:repo"`. Bundle repos используют тот же auth как parent workspace; их auth id — этот же workspace-level (`WorkspacesService.getRepoAuthId`).

### 1.7 Selected workspace / namespace persistence

`LauncherStateService` (`core/LauncherStateService.kt`):
- Хранит selected workspace id в `DataRepo("launcher", "state")` под key `"selectedWorkspace"`.
- На startup `init` читает; пустое → fallback `WorkspaceDto.DEFAULT.id` = `"default"`.

Selected namespace внутри workspace:
- `DataRepo("workspace-state", workspaceId)` под key `"selectedNamespace"` (`WorkspaceServices.kt:37`).
- **Legacy migration**: если ws id = `"default"` и репа для `"default"` нет — проверяется uppercase `"DEFAULT"`.

---

## 2. Bundles

### 2.1 `BundleDef`

`bundle/BundleDef.kt`:

```kotlin
data class BundleDef(
    val key: BundleKey,
    val applications: Map<String, BundleAppDef>,   // canonical app name → image
    val citeckApps: List<BundleAppDef>,            // images from ecosAppsImages
    val content: DataValue                          // raw YAML как DataValue tree
) {
    companion object {
        val EMPTY = BundleDef(BundleKey("0.0.0"), emptyMap(), emptyList(), DataValue.NULL)
    }
}

data class BundleAppDef(val image: String)   // fully-resolved Docker image ref
```

### 2.2 `BundleKey`

`bundle/BundleKey.kt` — comparable version identifier с path scope.

**String format**: `[scope/path/]<versionParts>[suffix]`

Examples:
- `"2.0.0"` — scope=empty, versionParts=[2,0,0], suffix=empty
- `"community/2.0.0"` — scope=["community"], versionParts=[2,0,0]
- `"community/2.0.0-rc1"` — scope=["community"], versionParts=[2,0,0], suffix=[("rc",1)]
- `"a/b/1.2.3"` — scope=["a","b"], versionParts=[1,2,3]

**Parsing**:
1. Всё до последнего `/` — split по `/`, образует `scope: List<String>`.
2. Остаток — parse: leading digit/dot characters → `versionParts: List<Int>` (trailing zeros stripped).
3. После digits — suffix: alphanumeric groups → nested `BundleKey`, non-numeric text → raw strings.

**Comparison order**: scope (empty preferred) → versionParts (larger preferred) → suffix parts (empty preferred) → raw string lexicographic. `TreeMap` в `BundleUtils` использует reversed comparator (highest version first).

`BundleKey.toString()` = `rawKey` unchanged. Jackson serialize/deserialize через `@JsonValue` / `@JsonCreator` на raw string.

**Тест парности с Go**: на стороне 2.x Go `compareBundleVersions` имеет explicit parity-тест `TestCompareBundleVersions_KotlinParity` — критично не нарушить порядок.

### 2.3 `BundleRef`

`bundle/BundleRef.kt` — two-part pointer:

```kotlin
data class BundleRef(val repo: String, val key: String)
```

**String format**: `<repoId>:<keyString>`. Split по **последнему** `:` (потому что keys могут содержать `:`). `BundleRef.EMPTY` = `("", "")`. `valueOf` parsing, `create` constructor (errors если either part пустой). Jackson через `@JsonValue` / `@JsonCreator`.

`BundleRef` — unresolved pointer: имена repo + raw key string. `BundleKey` — parsed представление key. `BundlesService.getBundleByRef` ресолвит ref в `BundleDef`: lookup `repoId` в workspace's bundle repo map → map lookup на `ref.key` (raw string, не parsed key).

### 2.4 `BundlesService`

`bundle/BundlesService.kt`:

- **Cache**: `ConcurrentHashMap<String, BundlesRepoInfo>` per repo id. `BundlesRepoInfo` содержит sorted `List<BundleDef>`, `nextPlannedUpdateMs` timestamp, `Map<String, BundleDef>` keyed by raw key.
- `getRepoInfo` (private, `ReentrantLock`): entry point для всех cache reads. `REQUIRED` policy → evict first. `ALLOWED` policy → check `nextPlannedUpdateMs`; expired → evict, reload.
- `pullAndReadBundlesRepo`: `GitRepoService.initRepo` для bundle repo clone (`<AppDir>/ws/<workspaceId>/bundles/<repoId>/`) → `BundleUtils.loadBundles` на configured sub-path. Empty → `listOf(BundleDef.EMPTY)`.
- `getBundleByRef`: lookup через `bundlesByRawKey[ref.key]`; miss → `BundleNotFoundException`.
- `getRepoBundles(repoId, max)`: `List<BundleRef>` для repo (highest-version-first).
- `getLatestRepoBundle(repoId)`: первый элемент `getRepoBundles`, или `BundleRef.EMPTY`.

### 2.5 `BundleUtils`

`bundle/BundleUtils.kt`:

- Recursively walks directory tree; обрабатывает каждый `.yml`/`.yaml`.
- **Special case** `values.yml`: key выводится из **parent directory** path (relative to root), не имени файла. Иначе key = file path without extension.
- **Alias resolution**: до парсинга строит два lookup map'а из `WorkspaceConfig`:
  1. `appNameByAliases: Map<String, String>` — alias → canonical app id (из `webapps[*].aliases`, `citeckProxy.aliases`, `alfresco.aliases`).
  2. `eappsAppNames: Set<String>` — names, для которых триггерится `ecosAppsImages` parsing.
- **Image URL resolution**: если repository field начинается с known `imageReposById` key (часть до первого `/`), configured image repo URL добавляется как prefix.
- `"ecos"` top-level key — scope wrapper; children process'ятся recursively.
- Пустые bundles (no applications, no citeckApps) скипаются.

### 2.6 Откуда bundles берутся

Bundles живут в Git repositories. Каждый `WorkspaceConfig.BundlesRepo` декларирует repo:

```yaml
bundleRepos:
  - id: community
    name: Citeck Community
    url: https://github.com/Citeck/launcher-bundles-community.git
    branch: main
    path: ""
    pullPeriod: PT1H
```

Файлы bundle'ов — standard Helm `values.yaml`-style YAML, **не кастомный формат**. Нет "cloud" или CDN bundle source — bundles **всегда** из Git.

---

## 3. CloudConfig

### 3.1 Что это

Embedded HTTP server, делающий launcher **Spring Cloud Config Server'ом** для ECOS микросервисов в Docker контейнерах. Контейнеры конфигурятся через standard Spring Cloud Config client protocol: вызывают `GET /config/{appName}/{profiles}` и получают Spring Cloud Config JSON response. Launcher инжектит per-app конфигурацию (ports, secrets, URLs) которую знает из active namespace конфига.

### 3.2 `CloudConfig` interface

`config/cloud/CloudConfig.kt`:

```kotlin
interface CloudConfig {
    fun getConfig(appName: String, profiles: Collection<String>): Map<String, Any?>
}
```

Returns flat property map (dot-separated keys).

### 3.3 `MutableCloudConfig`

`config/cloud/MutableCloudConfig.kt` extends `CloudConfig`:

```kotlin
fun put(appName: String, config: Any)
fun put(appName: String, profile: String, config: Any)
```

`put(appName, config)` сетит profile `""` (default profile).

### 3.4 `CloudConfigImpl`

`config/cloud/CloudConfigImpl.kt`:

- Хранит в `LinkedHashMap<ConfigKey(appName, profile), Map<String, Any?>>`.
- `getConfig` мерджит: сначала `(appName, "")` entry, потом каждый profile по порядку (later wins).
- `put` конвертит incoming `Any` в `Map<String, Any?>` через Jackson, потом **flatten'ит** nested map в dot-notation (arrays → `[0]`, `[1]` suffixes). Это standard Spring Cloud Config flat property format.

**Пример flattening**:

```yaml
spring:
  datasource:
    url: jdbc:postgresql://...
    pool:
      max: 10
```

→

```
spring.datasource.url = jdbc:postgresql://...
spring.datasource.pool.max = 10
```

### 3.5 `CloudConfigServer`

`config/cloud/CloudConfigServer.kt`:

- **Port**: `8761` (hardcoded `PORT = 8761`)
- **Protocol**: plain HTTP (Ktor CIO)
- **Route**: `GET /config/{appName}/{profiles?}/{...}` — profiles — comma-separated в URL path segment
- **Response format**: Spring Cloud Config JSON:
  ```json
  {
    "name": "<appName>",
    "profiles": ["<p1>", "<p2>"],
    "label": "main",
    "version": "1",
    "propertySources": [
      { "name": "citeck-launcher://application.yml", "source": { ... } },
      { "name": "citeck-launcher://<appName>.yml", "source": { ... } }
    ]
  }
  ```
- **Baseline propertySource** (всегда есть): name `"citeck-launcher://application.yml"`, две key'и:
  - `ecos.webapp.web.authenticators.jwt.secret` (hardcoded dev JWT secret)
  - `configserver.status`
- **App-specific propertySource**: если `cloudConfig.getConfig(appName, profiles)` non-empty — добавляется `"citeck-launcher://<appName>.yml"`. App-specific overrides baseline.
- `init()` стартует Ktor server; `dispose()` stops с 0ms grace, 1000ms timeout.

`cloudConfig` — nullable field на `CloudConfigServer`. Заполняется namespace runtime при start (runtime push'ит per-app config через `MutableCloudConfig.put`). Без active namespace отдаётся только baseline.

### 3.6 Refresh cadence

**Нет periodic push'а или WebSocket**. Каждый Spring Cloud Config client опрашивает on startup (и опционально через Spring Cloud Bus, но launcher не реализует bus). Launcher отдаёт что в памяти на момент query.

---

## 4. Что важно для портёра

### Workspace lifecycle и migration

1. **AppDir layout** `~/.citeck/launcher/storage.db` хранит selected workspace в `launcher!state`. Сохранить путь и map name для backward compat на апгрейде с 1.x.
2. **WorkspaceDto entity** маппится на `EntityRef workspace@<id>`. Если в 2.x уберём entity framework полностью, нужна explicit migration.

### Bundle versioning

3. **`BundleKey.compareTo` parity** — критично. Go 2.x уже имеет `TestCompareBundleVersions_KotlinParity` — не трогать без сверки.
4. **Bundle URL/path conventions**: bundles в git, файлы `.yml/.yaml`, special case `values.yml`, alias resolution из `WorkspaceConfig`. Mass merge logic в `BundleUtils` — не trivial, аккуратно копировать.

### CloudConfig 8761 — критичный protocol-level контракт

5. Этот сервер должен быть на **точно том же порту 8761** и отдавать **байт-в-байт совместимый** Spring Cloud Config format. Spring Boot контейнеры (`eapps`, `gateway`, `emodel`) клиентят его при старте; если формат сломается — все ECOS контейнеры упадут.
6. Flattening algorithm в `CloudConfigImpl.buildFlattenedMap` (nested maps → dot-notation, arrays → `[0]`, `[1]`) должен быть **exact byte-compatible** реимплементацией. Тест с golden JSON fixtures обязателен.

### Go SDK маппинг

| Kotlin | Go |
|---|---|
| `WorkspaceDto` | struct в `internal/config/workspace.go` |
| `WorkspaceConfig` | struct в `internal/config/workspace.go` (parsed YAML) |
| `BundleKey` | `internal/bundle/key.go` |
| `BundlesService` | `internal/bundle/resolver.go` |
| `CloudConfigServer` | Go `net/http` handler в `internal/daemon/cloudconfig.go` |
| `LauncherStateService` | flat file `~/.citeck/launcher/state.json` или в той же БД |

### Что НЕ нужно портировать "как есть"

- Multi-workspace в 2.x server-only модели может быть out of scope. Текущий Go runtime использует single `daemon.yml` + `namespace.yml`. Спецификация (см. 10-2x-status) — это документированный gap.
