# 09 — Docker layer + default appfiles

## 1. `DockerApi`

`core/namespace/runtime/docker/DockerApi.kt`. Тонкая обёртка над docker-java (`com.github.dockerjava`). Все Docker I/O — через этот класс.

### 1.1 Container lifecycle

- **Create**: `createContainerCmd(image)` с name, env, exposed ports, labels, `HostConfig` (restart policy `unless-stopped`, port bindings, network mode = namespace network name, shm size, binds, memory limit), hostname = `appDef.name`.
- **Start**: `startContainerCmd(containerId)`.
- **Stop**: `stopContainerCmd(containerId).withTimeout(GRACEFUL_SHUTDOWN_SECONDS)` (10 секунд), потом polls `inspectContainer` до 12 секунд. Если still running — force removal.
- **Remove**: `removeContainerCmd(id).withForce(true).withRemoveVolumes(true)`.

### 1.2 Image pull

`pullImage(image)` возвращает `PullImageCmd`. Аутентификация инжектится `AppImagePullAction`'ом. `DockerApi` сам про auth не знает.

### 1.3 Volume operations

- `createVolume(nsRef, originalName, name)` — creates с labels: `WORKSPACE`, `NAMESPACE`, `ORIGINAL_NAME`, `LAUNCHER`, `DOCKER_COMPOSE_PROJECT`.
- `getVolumeByOriginalNameOrNull(nsRef, name)` — фильтрит по `NAMESPACE` label + `ORIGINAL_NAME` label, потом post-filter по workspace.
- `getVolumes(nsRef)` — все volumes для namespace, filter `NAMESPACE` + `WORKSPACE` labels.
- `getVolumesSize()` — вызывает Docker HTTP API `GET /system/df?type=volume` напрямую.

### 1.4 Network operations

- `createBridgeNetwork(nsRef, name)` — bridge network с `WORKSPACE`, `NAMESPACE`, `LAUNCHER`, `DOCKER_COMPOSE_PROJECT` labels.
- `deleteNetwork(name)` — если Docker возвращает "has active endpoints" но контейнеров не attached — `DockerStaleNetworkException`, не re-throw raw.

### 1.5 Stats streaming

`watchContainerStats(containerId, onStats, onError)`:
- `statsCmd(containerId).withNoStream(false)`.
- Stats arrive as Docker `Statistics` objects.
- CPU percent: `(cpuDelta / systemDelta) * onlineCpus * 100.0`.
- Memory: `usage - inactive_file` (tries `totalInactiveFile`, потом `inactiveFile`, потом `cache` — cgroup v1/v2).
- System info (CPU cores, total memory) — cached 120 секунд.

### 1.6 Snapshot import/export

- Uses `UTILS_IMAGE = "registry.citeck.ru/community/launcher-utils:1.0"` как helper container для tar operations.
- **Export**: stops containers check, runs `tar {--zstd|--xz} -cvf` внутри utils container для каждого volume, creates ZIP с `meta.json` + volume archives.
- **Import**: для каждого volume в `meta.json` — creates Docker volume с scoped name, runs `tar -xf` внутри utils container.

### 1.7 `DockerLabels`

`core/namespace/runtime/docker/DockerLabels.kt`. **КРИТИЧНЫЙ КОНТРАКТ ОБНАРУЖЕНИЯ.** Эти labels стамп'ятся на каждом container, volume, network created launcher'ом:

| Label key | Значение |
|---|---|
| `citeck.launcher.app.name` | App name (`"gateway"`) |
| `citeck.launcher.app.hash` | SHA-256 app definition; для detection configuration drift |
| `citeck.launcher.workspace` | Workspace id |
| `citeck.launcher.namespace` | Namespace id |
| `citeck.launcher.original-name` | Для volumes: plain name before scoping (`"postgres2"`) |
| `citeck.launcher` | `"true"` — marks object managed by launcher |
| `com.docker.compose.project` | `DockerConstants.getDockerProjectName(nsRef)` — Docker Desktop группирует контейнеры by namespace |

**ВАЖНО**: Любая Go-реализация которая хочет manage / discover existing launcher-created контейнеры **обязана** использовать **exactly** эти label keys + те же values. Изменение сломает backward compat с running deployments.

`citeck.launcher.app.hash` — особо критичен: idempotency key. Если Go runtime вычислит hash иначе (другой field ordering JSON serialization, разные SHA-256 inputs) — все existing running containers будут torn down + recreated при first start.

### 1.8 `DockerConstants`

`core/namespace/runtime/docker/DockerConstants.kt`:

```kotlin
NAME_DELIM = "_"

getNameSuffix(nsRef) = "_${namespace.lowercase()}_${workspace.lowercase()}"
getNamePrefix(nsRef) = "citeck_"
getDockerProjectName(nsRef) = "citeck_launcher_${namespace.lowercase()}_${workspace.lowercase()}"
getVolumeName(srcName, nsRef) = "citeck_volume_${srcName}_${namespace.lowercase()}_${workspace.lowercase()}"
```

Container names follow: `citeck_{appName}_{namespace}_{workspace}`.

### 1.9 Exception hierarchy

- `DockerNotAvailableException(cause)` — wraps connectivity failures. `isDockerNotRunning: Boolean` = `true` если root cause = `ConnectException` или `ConnectionClosedException`.
- `DockerStaleNetworkException(cause)` — wraps "has active endpoints" error когда no containers attached. Runtime logs warning + continues; stale network left in place.
- `DockerImageNotFound(image)` — thrown из `inspectImageOrNull` в `AppStartAction` когда image not present locally. App status reverts к `READY_TO_PULL` (не `START_FAILED`).

---

## 2. Runtime actions

### 2.1 `AppImagePullAction`

`.../runtime/actions/AppImagePullAction.kt`. Implements `ActionExecutor<Params, Unit>`. Pulls main image + всех init container images.

**Workflow**:
1. Acquires global `Semaphore(4)` (blocks до 1 минуты; times out если no pull response 1 минуту).
2. Для каждого image в queue (main + init containers, dedup'нутый по `HashSet<ImageToPull>`):
   - a. Если app — Citeck (`isCiteckApp()`) И image без `/` — skip (locally built).
   - b. Если `pullIfPresent == false` И image present locally — skip.
   - c. Determine registry host (всё до первого `/`).
   - d. Auth lookup: check `WorkspaceConfig.imageReposByHost` для host. Если `authType == BASIC` или previous `RepoUnauthorizedException` — fetch `SecretDef(id="images-repo:{host}", AuthType.BASIC)` от `AuthSecretsService`.
   - e. Если secret found — `AuthSecretsService.getSecret()` (may prompt user через dialog) + attach `AuthConfig` to pull cmd.
   - f. Execute `dockerApi.pullImage(image)` с callback.
   - g. Virtual thread updates `appRuntime.statusText` каждые 2 секунды.
   - h. Если `onError` detects "unauthorized" — `RepoUnauthorizedException(secretVersion)`.
   - i. On `RepoUnauthorizedException` — retry immediately с `retryDelay = 0` (re-prompts credentials с bumped version).
   - j. On `AuthenticationCancelled` — `retryDelay = -1` (abort, no retries).
   - k. Если `pullIfPresent == true` И pull fails после `RETRIES_COUNT_FOR_EXISTING_IMAGE = 3` итераций но image exists locally — complete successfully.
3. Normal retry delays: `[1s, 1s, 1s, 5s, 10s]` (index-capped at 4).

**`pullImageIfPresent` flag**: `true` только если app — не `THIRD_PARTY` и image name содержит `"snapshot"` (case-insensitive). Триггерит fresh pull snapshot images на каждом start.

### 2.2 `AppStartAction`

`.../runtime/actions/AppStartAction.kt`.

**Workflow**:
1. Fetch existing containers для `(nsRef, appName)`.
2. Compute `deploymentHash`: SHA-256 над `appDef.getHash()` + `repoDigests` main image + всех init container images.
3. Для каждого existing container:
   - Hash matches + state = `running` → keep (add to `validContainersNames`).
   - Иначе → stop + remove.
4. Loop пока `validContainersNames.size == 1`:
   - a. Run all `initContainers` sequentially.
   - b. Create container: name `citeck_{appName}_{namespace}_{workspace}`, restart policy `unless-stopped`, network mode = `nsRuntime.networkName`, all port bindings, env vars, volume binds, memory limit.
   - c. `prepareVolume(runtime, volume)`: если source `./...` — resolve against `runtimeFiles.resolveAbsPathInFilesDir`; если source no-separator — `createVolumeIfNotExists` (lookup по `ORIGINAL_NAME` label, create со scoped name если absent).
   - d. Start container.
   - e. Run all `startupConditions`.
   - f. Run all `initActions` (post-start exec).
5. Container creation retry до 5 раз; на `ConflictException` — try find + remove conflicting container.

**Init container execution**:
- Create container с `noRestart` policy, `100m` memory cap, без network attachment (default bridge).
- Start, wait до 30 секунд для exit code 0.
- Non-zero exit → log last 10 000 lines + throw.
- Always remove container в `finally`.

### 2.3 `AppStopAction`

`.../runtime/actions/AppStopAction.kt`.

Fetches all containers для `(nsRef, appName)`, вызывает `stopAndRemoveContainer` на каждом. Retry on error с 1-секундной delay (no limit от action framework, но `STOPPING_FAILED` — stalled state требующий manual recovery).

### 2.4 Authentication error flow

- `RepoUnauthorizedException(secretVersion)` — thrown когда registry возвращает 401/403. Carries secret version which was rejected, чтобы secrets service знал bump version + re-prompt.
- `AuthenticationCancelled(secretDef, requiredFor)` — thrown когда user dismisses credentials dialog. Pull action aborts с `retryDelay = -1`.

---

## 3. Volumes

### 3.1 `VolumeInfo`

```kotlin
class VolumeInfo(
    val name: String,         // scoped Docker volume name (e.g. "citeck_volume_postgres2_ns1_ws1")
    val sizeMb: String,       // formatted "%.2f mb"
    val sizeBytes: Long       // -1 if /system/df didn't return size
)
```

### 3.2 `VolumesRepo`

`core/namespace/volume/VolumesRepo.kt`. Implements generic `Repository<String, VolumeInfo>`. Scoped к currently selected namespace.

| Method | Behavior |
|---|---|
| `find(max)` | List all Docker volumes для selected namespace, sorted by name. Вызывает `getVolumesSize()` один раз для full list. |
| `get(id)` | Lookup по scoped Docker name. |
| `delete(id)` | Delete volume. **Требует namespace = STOPPED**, throws иначе. |
| `set(id, value)` | Not supported (throws). |

### 3.3 Naming convention

Generator использует plain names (`"postgres2"`, `"rabbitmq2"`, `"mongo2"`, `"zookeeper2"`, `"pgadmin2"`, `"alf_postgres"`, `"alf_content"`, `"alf_solr_data"`, `"stt_models"`). На container-create time `AppStartAction.createVolumeIfNotExists()` маппит plain name к scoped form через `DockerConstants.getVolumeName`. `ORIGINAL_NAME` label на Docker volume — lookup key.

---

## 4. Default appfiles

Classpath `src/main/resources/appfiles/` pre-loaded в `NsGenContext.files` на generator construction time. Generator может override individual entries; runtime пишет final map на диск.

### 4.1 `alfresco/alfresco_additional.properties` (36 lines)

Bind-mounted: `/tmp/alfresco/alfresco_additional.properties`. Alfresco-specific Spring/Activiti properties что extend image default config:
- DB connection pool tuning: `db.pool.idle=5`, `db.pool.max=275`, `db.pool.min=30`
- ECOS history service integration: `http://history:8086`
- RabbitMQ integration (host `rabbitmq`, port `5672`, user/pass `admin`)
- Zookeeper: `ecos.zookeeper.host=zookeeper`
- Flowable DB pool + mail server settings (`mailhog:1025`)
- Event server: `event.server.host=rabbitmq`, `event.server.port=5672`
- External auth header: `external.authentication.proxyHeader=X-ECOS-User`
- Tenant id: `ecos.server.tenant.id=ecos-g7s2`

### 4.2 `keycloak/ecos-app-realm.json`

Bind-mounted: `/opt/keycloak/data/import/ecos-app-realm.json`. Defines `ecos-app` realm в Keycloak. Key settings: `sslRequired=none`, `accessTokenLifespan=300`, `ssoSessionMaxLifespan=2592000`, `ssoSessionIdleTimeout=3600`. Contains:
- Client definitions
- User accounts (`admin`/`admin`)
- Roles
- Identity provider mappers
- Protocol mappers для ECOS proxy app

Keycloak импортит realm на first start (`--import-realm` flag).

### 4.3 `keycloak/healthcheck.sh` (17 lines)

Bind-mounted: `/healthcheck.sh`. Used as `ExecProbeDef` command `["bash", "/healthcheck.sh"]`. Opens raw TCP socket `127.0.0.1:8080`, sends HTTP/1.0 GET `/realms/master`, checks response contains `"200 OK"`. Exit 0 success, 1 иначе. Lightweight alternative к `curl` где curl может быть не available в Keycloak image.

### 4.4 `pgadmin/servers.json`

Bind-mounted: `/pgadmin4/servers.json`. Pre-register'ит one server definition в pgAdmin указывающий на `postgres:5432` с username `postgres`, SSL preferred. Eliminate'ит manual server setup после container start.

### 4.5 `postgres/init_db_and_user.sh` (31 lines)

Bind-mounted: `/init_db_and_user.sh`. Вызывается через `AppInitAction(ExecShell("/init_db_and_user.sh {dbName}"))` после postgres start. Convention: `dbName == dbUser == dbPassword`. Checks для existing DB перед create; grants все privileges + `USAGE, CREATE ON SCHEMA public` + sets `search_path = public`. Idempotent.

### 4.6 `postgres/pg_hba.conf` (128 lines)

Bind-mounted: `/etc/postgresql/pg_hba.conf`. Allows `trust` для local/localhost connections. Adds `host all all all md5` для всех TCP connections с password auth. Referenced from `postgresql.conf` через `hba_file = '/etc/postgresql/pg_hba.conf'`.

### 4.7 `postgres/postgresql.conf`

Bind-mounted: `/etc/postgresql/postgresql.conf`. Container command: `-c config_file=/etc/postgresql/postgresql.conf`. Key non-default settings:
- `listen_addresses = '*'`
- `max_connections = 1000`
- `max_prepared_transactions = 1000`
- `hba_file = '/etc/postgresql/pg_hba.conf'`

Остальное — commented out (postgres defaults). Standard full template.

### 4.8 `proxy/lua_oidc_full_access.lua` (207 lines)

Bind-mounted: `/etc/nginx/includes/lua_oidc_full_access.lua:ro`. Используется только в Keycloak authentication mode. OpenResty/nginx Lua script implementing OIDC через `resty.openidc`:

- `client_id = "ecos-proxy-app"`, `client_secret = "2996117d-9a33-4e06-b48a-867ce6a235db"` (hardcoded, matches Keycloak realm JSON)
- Discovery: `http://keycloak:8080/realms/ecos-app/.well-known/openid-configuration`
- Static assets (`.js`, `.css`, `.png`, `.svg`, etc.) bypass auth с `userName = "guest"`.
- Specific paths bypass OIDC: `/healthcheck/`, `/rabbitmq`, `/node-exporter`, `/postgres-exporter`, `/cadvisor/`, `/alfresco/monitoring`.
- Auth priority:
  1. access token в cookie `PA` через introspection
  2. `Authorization` header bearer token через introspection
  3. OIDC session через `resty.openidc.authenticate`
- Non-navigate (AJAX/fetch) requests получают 401 вместо redirect (избежать CORS).
- On success — sets headers `X-Alfresco-Remote-User` и `X-ECOS-User` = authenticated username.

---

## 5. Что важно для портёра

### docker-java → Go Docker SDK

1. `com.github.dockerjava` → `github.com/docker/docker/client` (official). Call patterns close map: `ContainerCreate`, `ContainerStart`, `ContainerStop`, `ContainerRemove`, `ImagePull`, `NetworkCreate`, `VolumeCreate`. Streaming callbacks (`ResultCallbackTemplate`) → Go channels / goroutines reading `io.Reader`.

### `DockerLabels` — contract обнаружения

2. **СОХРАНИТЬ ВСЕ LABEL KEYS И ЗНАЧЕНИЯ**:
   - `citeck.launcher.app.name`
   - `citeck.launcher.app.hash`
   - `citeck.launcher.workspace`
   - `citeck.launcher.namespace`
   - `citeck.launcher.original-name`
   - `citeck.launcher=true`
   - `com.docker.compose.project`

   Изменение = backward compat fail.

3. `citeck.launcher.app.hash` — особо: реализовать SHA-256 с **byte-exact** input что Kotlin. Best practice: написать parity-тест который Kotlin и Go runtime'ы вычисляют тот же hash для same `ApplicationDef`.

### Volume naming

4. Go runtime обязан implement тот же scoping: `"citeck_volume_{originalName}_{namespace.lowercase()}_{workspace.lowercase()}"`. И writing `citeck.launcher.original-name` label на каждом volume — потому что `getVolumeByOriginalNameOrNull` использует этот label для finding existing scoped volume по plain generator name.

### Container/network naming

5. Container names: `citeck_{appName}_{namespace.lowercase()}_{workspace.lowercase()}`.
6. Network name: workspaces/namespaces auto-scoped (точная форма — см. `DockerApi.createBridgeNetwork`).

### Init actions / probes

7. Init actions — post-start exec через `docker exec`. 10s timeout — лояльно увеличить.
8. `HttpProbeDef.port` — **container port**, не host. Probe должен inspect port bindings + hit `127.0.0.1:{hostPort}`. **Нет** container-DNS probe пути.
9. `failureThreshold = 10_000` × `periodSeconds = 10` — preserve или config'урировать.

### Generator merge logic

10. 3-level merge (workspace-default → workspace-per-app → namespace-per-app). Jackson round-trip + deep merge:
    - Scalars: last non-zero/non-empty wins
    - Objects (`cloudConfig`, `dataSources`): deep-merge applies
11. Template substitution (`TmplUtils.applyAtts`) на merged props — `{PG_HOST}` ресолвится из `NsGenContext.VARS`.

### Detach/Attach

12. `detachedApps` (= `manualStoppedApps`) — single source of truth. Persist across restarts. App в `detachedApps` → runtime НЕ start'ит даже если namespace started. Bundle update / config change → `dependsOnDetachedApps = {onlyoffice, ai, stt-sidecar}` triggers full regeneration (proxy env vars change).

### Edited apps + locked defs

13. `editedApps` (= `STATE_EDITED_APPS`) — user overrides. `editedAndLockedApps` — survive bundle updates. Locked app's def никогда не replaced на regeneration. Оба persistence keys + same merge semantics.

### `UTILS_IMAGE`

14. `registry.citeck.ru/community/launcher-utils:1.0` — dependency для snapshot import/export + Zookeeper init container. Go-порт должен ensure это image available перед любой snapshot или Zookeeper операцией.

### Mapping table

| Kotlin | Go |
|---|---|
| `DockerApi` | `internal/runtime/docker.go` (или подобный пакет) |
| `docker-java client` | `github.com/docker/docker/client` |
| `DockerLabels` constants | `internal/runtime/labels.go` |
| `DockerConstants` | `internal/runtime/naming.go` |
| `AppImagePullAction` | `internal/runtime/pull.go` |
| `AppStartAction` | `internal/runtime/start.go` |
| `AppStopAction` | `internal/runtime/stop.go` |
| `VolumesRepo` | `internal/volume/repo.go` |

### Appfiles bundling

15. Все 8 файлов в `resources/appfiles/` нужно embed через `//go:embed` в Go binary. Layout copy as-is. Если меняются — критично сверить с tests existing deployments что namespace всё ещё стартует.

16. `lua_oidc_full_access.lua` — `client_secret` hardcoded должен match с `ecos-app-realm.json`. Если меняем — менять в обоих местах.
