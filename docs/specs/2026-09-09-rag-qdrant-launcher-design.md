# Опциональное подключение citeck-rag и Qdrant в лаунчер (1.x и 2.x)

Дата: 2026-09-09. Статус: дизайн согласован, ожидает реализации.

## 1. Проблема

`citeck-rag` (enterprise) не разворачивается ни одной версией лаунчера.

* Образ **объявлен** в enterprise-бандлах: `EcosRagApp → enterprise/citeck-rag:1.2.2`
  (`launcher-public-workspace/enterprise/2026.2.yaml:103`, `enterprise-rc/2026.3-RC2.yaml:103`,
  `2026.3-RC3.yaml:103`, а также `docker-compose-kit/ecos-enterprise/develop/2026.3-RC3/values.yaml:104`).
* Но в `workspace-v1.yml` **нет** записи `webapps[]` с алиасом `EcosRagApp` — ни в публичном
  воркспейсе, ни в enterprise-ветке `launcher-workspaces@core`.
* Лаунчер генерирует вебаппы как пересечение бандла и `webapps` (2.x — `internal/namespace/generator.go:99-113`,
  маппинг алиасов `internal/bundle/resolver.go:926-946, 1041-1045`; 1.x — `NamespaceGenerator.kt:81-86`).
  Ключ `EcosRagApp` не мапится в id вебаппа, поэтому **контейнер не создаётся вообще**, молча.
* Qdrant (векторная БД, без которой rag бесполезен) не описан нигде.

Следствие: семантический поиск в UI и RAG-инструменты ассистента на стендах из лаунчера не работают.

## 2. Требования

1. rag и qdrant доступны в **обеих** версиях лаунчера — 1.x официально ещё не выведена из эксплуатации.
2. По умолчанию **не запускаются**; включаются явным действием пользователя (кнопка Start).
3. Не появляются там, где их нет в бандле (community-стенды не должны их видеть).
4. В 2.x зависимость вебаппа от другого приложения выражается конфигурацией (generic `dependsOn`).
5. Образ, запиненный в бандле, приоритетнее конфигурации.

## 3. Решение

### 3.1 Где что объявлено

**Бандл** (единственный источник версии образа) — enterprise-бандлы в
`launcher-public-workspace/enterprise/*.yaml` и `docker-compose-kit/ecos-enterprise/*/values.yaml`:

```yaml
EcosRagApp:                 # уже есть
  image: { repository: enterprise/citeck-rag, tag: 1.2.2 }
qdrant:                     # новое; ключ — каноническое имя, как у stt-sidecar
  image: { repository: qdrant/qdrant, tag: v1.14.1 }
```

Ключ `qdrant` — каноническое имя, а не алиас: не-вебаппы объявляются в бандлах именно так
(`docker-compose-kit/.../values.yaml:108` — `stt-sidecar:`). `resolveImageURL` при неизвестном
первом сегменте отдаёт ссылку как есть (`internal/bundle/resolver.go:949-961`), поэтому
`qdrant/qdrant:v1.14.1` тянется с Docker Hub, а зеркало вида `enterprise/qdrant` — с harbor.

**workspace-v1.yml** (оба репозитория — публичный и `launcher-workspaces@core`):

```yaml
webapps:
  - id: rag
    aliases: [ 'EcosRagApp' ]
    defaultProps:
      heapSize: 512m
      memoryLimit: 1200m
qdrant:                     # только не-версионные свойства
  memoryLimit: 1g
  grpcPort: 6334            # порт, который получает rag; HTTP-порт пробы 6333 фиксирован
namespaceTemplates:
  - id: default
    detachedApps: [ ..., rag ]   # ТОЛЬКО rag; qdrant сюда не добавляется
```

`qdrant` намеренно **не** входит в `detachedApps`: он существует лишь тогда, когда rag включён,
и должен стартовать вместе с ним. Detached qdrant означал бы, что rag ждёт зависимость,
которую никто не поднимет.

> **Устарело (2026-09-22).** Хранилище больше не следует за rag: оно генерируется по
> наличию образа qdrant в бандле и стартует как обычное приложение (условие «rag включён»
> ушло вместе с вердиктом `AutoDetachedApps`, см. AGENTS.md). Поэтому если `rag`
> добавляется в `detachedApps`, туда же нужно добавить и `qdrant` — иначе на стенде с
> выключенным RAG хранилище поднимется в одиночку и будет занимать память. Утверждение
> «rag ждёт зависимость, которую никто не поднимет» при этом верно и сегодня: явный
> `citeck start rag` зависимости за собой не тянет, rag встаёт в `DEPS_WAITING`,
> неймспейс — в `STALLED`, и лечится это `citeck start qdrant`.

Ключа `image` в секции `qdrant:` нет — версия живёт только в бандле.

### 3.2 Генерация: цепочка rag → qdrant

Новая функция `generateQdrant()` вызывается после цикла вебаппов, по образцу `generateSttSidecar`
(2.x — `internal/namespace/generator_webapp.go:811-860`; 1.x — `NamespaceGenerator.kt:259-292`):

1. `rag` отсутствует среди сгенерированных приложений → выходим молча. Это и есть гейт
   «нет `EcosRagApp` в бандле → нет qdrant», одинаковый в обеих ветках.
2. `rag` есть, но detached → qdrant не генерируем (зеркало правила «ai detached → нет stt»).
3. Образ: `bundle.applications["qdrant"]`; если пусто — `slog.Error` и выход.
4. Контейнер: kind `THIRD_PARTY`, named volume под `/qdrant/storage`, HTTP-проба `GET /healthz`
   на 6333, лимит памяти из `qdrant.memoryLimit`, gRPC-порт из `qdrant.grpcPort`, портов наружу не публикуем.
5. Обвязка rag: `QDRANT_HOST=qdrant`, `QDRANT_GRPC_PORT=6334` (имена из
   `citeck-rag/src/main/resources/config/application.yml`), `dependsOn(qdrant)`.
   Делается в генераторе, а не конфигом: env и зависимость должны появляться атомарно.

Гейт **одинарный** — только по наличию rag. Проверено, что rag потребляется не только `ai`:
`emodel` ходит в `rag/rag-search` из глобального поиска (`ecos-model/.../SearchRecordsDao.kt:63-64`,
защищено `isAppAvailable`), а `ecos-ui` выполняет админ-действия `rag/workspace-reindex@` и
`rag/gitlab-sync@` (артефакты `citeck-rag/src/main/resources/eapps/artifacts/ui/action/rag-*.yml`).
Гейт по `ai` отрезал бы семантический поиск при выключенном ассистенте.

### 3.3 Связка с ai

RAG в ассистенте по умолчанию выключен: `citeck.ai.rag.enabled: false`
(`citeck-ai/src/main/resources/config/application.yml:156-157`, `RagProperties.kt`), под
`@ConditionalOnProperty` стоят `RagServiceClient.kt:22`, `RagSearchTool.kt:17`,
`RagGetDocumentTool.kt`, `SearchDocumentationTool.kt`.

Поэтому генератор выставляет `ai` переменную `CITECK_AI_RAG_ENABLED=true`, когда `ai` присутствует
и не detached **и** `rag` присутствует и не detached. Иначе переменная не выставляется. Приём тот же,
что для связки ai↔stt (`CITECK_AI_CALLRECORDING_STT_SIDECARURL`, `NamespaceGenerator.kt:288-292`).

### 3.4 Регенерация при переключении detach (2.x)

Гейт работает, только если Start/Stop на звене цепочки вызывает перегенерацию неймспейса.
Сейчас список таких приложений захардкожен в демоне: `internal/daemon/attach_toggle_regen.go:22-26`
(`{onlyoffice, ai, stt-sidecar}`).

Хардкод убираем. Генератор возвращает в `GenResp` набор `GatingApps` — приложения, detach-состояние
которых меняет **состав** неймспейса; генераторы наполняют его сами (`generateSttSidecar` → `ai`,
`generateQdrant` → `rag`, `generateProxy` → `onlyoffice`, `alfresco`). Демон читает набор из
результата генерации. Следующая такая цепочка не потребует правок в `daemon`.

### 3.5 Generic `dependsOn` для вебаппов (2.x)

* Схема: `webapps[].defaultProps.dependsOn: []string` (`internal/bundle/resolver.go:57-80`) и парный
  override `webapps.<id>.dependsOn` в `namespace.yml` (`internal/namespace/config.go:61-74`).
  Namespace перебивает workspace — как у `enabled` (`generator_webapp.go:355-373`).
* Применение — в `applyWebappDefaults`: конфигурационные зависимости **объединяются** с
  захардкоженными (ZK, RMQ, keycloak, postgres), не заменяют их.
* Валидация конфига отклоняет самозависимость и циклы (сейчас цикл молча оставляет приложения
  в `DEPS_WAITING` навсегда).
* Ссылка на отсутствующее приложение уже обрабатывается `pruneAppsWithMissingDeps`
  (`internal/namespace/generator.go:253-271`) — добавляем явный лог «кто кого утянул».
* Порядок генерации безопасен: вебаппы — `generator.go:111-113`, additionalApps — `:122`,
  prune — последним на `:131`. Зависимость вебаппа на additionalApp работает уже сейчас.
* `DependsOn` входит в `GetHashInput` (`internal/appdef/appdef.go:205`), поэтому смена зависимости
  корректно пересоздаёт контейнер.

### 3.6 Семантика остановленной зависимости (2.x)

`appsDepsSatisfied` считает detached-зависимость удовлетворённой (`internal/namespace/runtime_loop.go:311-331`,
ветка `if r.manualStoppedApps[dep]`). Для пары proxy/onlyoffice эта ветка не нужна: `dependsOn(onlyoffice)`
добавляется только когда тот не detached (`generator_proxy.go:30-33`; так же для `ai` `:98-107` и
`alfresco` `:148-152`). Реально под неё попадают безусловные зависимости вебаппов на
`zookeeper`/`rabbitmq` (`generator_webapp.go:151-152`), `postgres`, `keycloak`: остановив руками
postgres, пользователь получает вебаппы, которые стартуют «мимо» зависимости и падают на пробах.

Ветку убираем. Приложение остаётся в `DEPS_WAITING` и пишет в `StatusText`, кого именно ждёт и в
каком тот состоянии — например «Ожидает: qdrant (остановлен), postgres (запускается)». UI этот текст
уже рендерит (`web/src/components/AppTable.tsx:236-241`); то же отражаем в reload-плане.

Это изменение поведения: остановка инфраструктурного приложения теперь удерживает зависящие в
ожидании вместо бесполезного старта. Отдельный пункт в changelog.

### 3.7 Приоритет образов (2.x)

Целевое правило: **workspace-дефолты < `namespace.yml` < бандл < `citeck edit`**.
Бандл пинит версию релиза; если её нужно подменить на конкретном стенде — для этого есть явный и
видимый `citeck edit <app>` (такие приложения помечаются `*` в `citeck status`), а патчи применяются
после генерации (`internal/namespace/generator.go:157-168`).

Вводим единый хелпер:

```go
// bundle → namespace.yml → workspace defaults → fallback
func resolveAppImage(ctx *NsGenContext, name, wsImage, nsImage, fallback string) string
```

Правим места, где правило нарушено:

| Приложение | Где | Что сейчас |
|---|---|---|
| вебаппы | `generator_webapp.go:54`, `:95`, `:335` | workspace и namespace перебивают бандл |
| pgadmin | `generator_infra.go:87-93` | namespace → workspace → бандл |
| stt-sidecar | `generator_webapp.go:830-831, 871` | workspace выше бандла |
| observer, alfresco | `generator_webapp.go:574`, `:708` | проверить при реализации |

Уже соответствуют правилу и не меняются: postgres (`generator_infra.go:114-119`),
keycloak (`generator_keycloak.go:27-33`), proxy (`generator_proxy.go:120-124`).
MongoDB (`generator_infra.go:38-42`) **вне объёма** — приложение выводится из эксплуатации.

Совместимость: сегодня `image` не задан ни в одном реальном конфиге — ни в
`launcher-public-workspace/workspace-v1.yml`, ни в `launcher-workspaces@core`, ни в локальных
`namespace.yml` (там только пустые `image: ""`). Практический риск нулевой; в changelog — отдельный пункт.

### 3.8 Патч 1.x

Ветка заморожена на `v1.4.1` (2026-04-30), поэтому объём минимальный — ровно шаблон stt-sidecar:

* `core/namespace/AppName.kt` — `RAG = "rag"`, `QDRANT = "qdrant"` в секцию `// enterprise`.
* `core/workspace/WorkspaceConfig.kt` — `QdrantProps(memoryLimit = "1g", grpcPort = 6334)` + поле `qdrant`
  (по образцу `SttSidecarProps`, `WorkspaceConfig.kt:18, 92-100`). Без `image`.
* `core/namespace/gen/NamespaceGenerator.kt` — `generateQdrant()` + вызов в `generate()`;
  обвязка rag (env + `addDependsOn`); `CITECK_AI_RAG_ENABLED` для `ai`; `RAG` в
  `dependsOnDetachedApps` (`:110`) — иначе Start на rag не вызовет регенерацию и qdrant не появится.
* `build.gradle.kts` + `CHANGELOG.md` — версия 1.4.2.
* Тест `NamespaceGeneratorRagTest.kt` по образцу `NamespaceGeneratorAiTest.kt`.

В 1.x **не делаем**: generic `dependsOn`, динамический `GatingApps`, смену семантики detached-зависимостей,
унификацию приоритета образов. Цепочка `rag → qdrant` от них не зависит.

Релиз: ветка `release/1.4.2` от тега `v1.4.1` в том же репозитории `citeck-launcher` (Kotlin-исходники
живут в тегах), заголовок `# Release 1.4.2` в начале `CHANGELOG.md` (workflow вырезает секцию по
точному совпадению с версией тега), затем lightweight-тег `v1.4.2` → `.github/workflows/release.yml`
собирает deb/msi/dmg×2. `make_latest: 'false'` в workflow гарантирует, что релиз 1.x не перебьёт 2.x.

## 4. Порядок выката

Конфиг общий для обеих версий, поэтому порядок обязателен:

1. **Код обеих веток** — 2.x (2.11.0) и 1.x (1.4.2). Пока в бандлах и конфиге ничего нет,
   поведение не меняется ни у кого.
2. **Бандлы** — `qdrant:` в enterprise-бандлы (`launcher-public-workspace/enterprise/*.yaml`,
   `docker-compose-kit/ecos-enterprise/*`).
3. **workspace-v1.yml** — `webapps: - id: rag` + `rag` в `detachedApps`, в обоих репозиториях.

Остаток, который порядком не лечится: у пользователей 1.4.1 и старше после шага 3 в списке появится
выключенный `rag`, который при ручном старте не заработает (их сборка не умеет qdrant). Приложение
detached, само по себе ничего не ломает. Шаг 3 выполняется после того, как 1.4.2 разойдётся.

## 5. Проверка

* **Генератор 2.x** — юнит-тесты: нет `EcosRagApp` в бандле → нет ни rag, ни qdrant; rag detached →
  qdrant не сгенерирован; rag включён → qdrant есть, у rag `QDRANT_HOST`/`QDRANT_GRPC_PORT`/`dependsOn`,
  у `ai` выставлен `CITECK_AI_RAG_ENABLED`; таблица случаев на приоритет образов.
* **Рантайм 2.x** — тест: приложение с остановленной зависимостью висит в `DEPS_WAITING` и пишет в
  `StatusText`, кого ждёт.
* **1.x** — `NamespaceGeneratorRagTest.kt`.
* **Гейты** — `make check` в 2.x, `./gradlew build` (с ktlint) в 1.x, с выводом.
* **Живой прогон** — неймспейс на enterprise-бандле, Start на rag: qdrant появляется сам, оба выходят
  в RUNNING, rag отвечает на `/management/health` (порт 8614). Скриншот UI.

Ограничение: `CTK_OPENAI_API_KEY` не предоставляется, поэтому индексация и семантический поиск
**не проверяются**. Проверяется механика лаунчера: гейт, регенерация по toggle, порядок старта, env,
`dependsOn`, здоровье контейнеров.

## 6. Вне объёма

* MongoDB (выводится из эксплуатации) — приоритет образа не трогаем.
* Отдельный location для rag в прокси: не нужен, `ai` и `emodel` ходят в rag внутри сети
  (дискавери через ZooKeeper), UI — через gateway.
* Postgres для rag: не требуется, миграций нет, персистентность — Qdrant + emodel.
* Настройка `CTK_OPENAI_API_KEY`: секрет, задаётся пользователем через `citeck edit rag`;
  в общие конфиги не кладётся.
* Generic `dependsOn`, `GatingApps` и унификация образов в 1.x.

## 7. Ссылки

* Требования сервиса: `citeck-rag/src/main/resources/config/application.yml`,
  `citeck-rag/docker-compose.dev.yml`, `ecos-helm/templates/ecos-rag-app.yaml`,
  `ecos-helm/values.yaml:1910-1962` (qdrant — `:960-994`).
* Шаблон не-вебаппа в 2.x: `internal/namespace/generator_webapp.go:811-860`.
* Шаблон не-вебаппа в 1.x: `NamespaceGenerator.kt:259-292`.
* `additionalApps` (рассмотрены и отвергнуты для qdrant — не умеют пинить версию по релизам):
  `docs/additional-apps.md`.
