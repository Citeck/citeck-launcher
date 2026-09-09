# citeck-rag + Qdrant в лончере — план реализации

> **Для агентов-исполнителей:** ОБЯЗАТЕЛЬНЫЙ САБ-СКИЛЛ — `superpowers:subagent-driven-development`
> (рекомендуется) или `superpowers:executing-plans`. Шаги помечены чекбоксами `- [ ]`.

**Цель:** дать обеим версиям лончера (2.x и 1.x) возможность запускать `citeck-rag` вместе с
Qdrant по явному действию пользователя, и заодно навести порядок в приоритете образов и в
поддержке `dependsOn` в 2.x.

**Архитектура:** rag — обычный вебапп из бандла (гейт «нет `EcosRagApp` в бандле → нет rag»
работает сам). Qdrant — новое приложение, генерируемое только когда rag присутствует и включён,
по образцу `stt-sidecar`. Связка (env + `dependsOn`) делается в генераторе атомарно. В 2.x
дополнительно: единый резолв образа, конфигурируемый `dependsOn` у вебаппов, честное ожидание
остановленной зависимости и динамический набор gating-приложений вместо хардкода в демоне.

**Стек:** Go 1.x + React (2.x), Kotlin/Compose + Gradle (1.x), YAML-конфиги в
`launcher-public-workspace`, `launcher-workspaces`, `docker-compose-kit`.

**Спека:** `docs/specs/2026-09-09-rag-qdrant-launcher-design.md`

## Глобальные ограничения

- Версия 2.x — **2.11.0**; версия 1.x — **1.4.2**.
- Полный гейт 2.x: `make check`. Прогонять перед каждым коммитом задачи.
- Changelog 2.x: папка `changelog/2.11.0/` со **всеми 8 локалями** (`en ru zh es de fr pt ja`) плюс
  запись в `changelog/index.json` — иначе падает `internal/update/changelog_repo_test.go`.
- 1.x собирается JDK 21, ktlint-хук ставится автоматически; версия правится в `build.gradle.kts:18`,
  секция `# Release 1.4.2` — в начале `CHANGELOG.md` (workflow вырезает её по точному совпадению).
- Приоритет образа: **workspace defaults < `namespace.yml` < bundle < `citeck edit`**.
- Порт rag — 8614, health — `GET /management/health`. Qdrant: HTTP 6333 (`/healthz`), gRPC 6334.
- `CTK_OPENAI_API_KEY` **не попадает** ни в один конфиг репозитория — только `citeck edit rag`.
- MongoDB не трогаем.
- Ветка работы в 2.x: `feature/rag-qdrant` (уже создана, в ней лежит спека).

---

## Файловая структура

**2.x (`citeck-launcher/`)**
- `internal/namespace/generator_util.go` — сюда добавляется `resolveAppImage` (рядом с `bundleImageOr`).
- `internal/namespace/generator_qdrant.go` — **новый**: генерация qdrant + обвязка rag/ai.
- `internal/namespace/generator_webapp.go` — резолв образа, конфигурируемый `dependsOn`.
- `internal/namespace/generator_infra.go` — резолв образа pgadmin.
- `internal/namespace/generator.go` — `GenResp.GatingApps`, вызов `generateQdrant`, проверка циклов.
- `internal/namespace/context.go` — `NsGenContext.GatingApps`.
- `internal/namespace/runtime_loop.go` — семантика остановленной зависимости + `StatusText`.
- `internal/bundle/resolver.go` — `WebappDefaultProps.DependsOn`, `QdrantProps`.
- `internal/namespace/config.go` — `WebappProps.DependsOn`.
- `internal/appdef/appdef.go` — константы `AppRag`, `AppQdrant`.
- `internal/daemon/attach_toggle_regen.go` — динамический набор вместо хардкода.
- `internal/i18n/locales/*.json` — ключ статуса ожидания.
- Тесты: `generator_image_priority_test.go`, `generator_dependson_test.go`,
  `generator_qdrant_test.go`, `runtime_deps_waiting_test.go` — все новые.

**1.x (ветка `release/1.4.2` от тега `v1.4.1`)**
- `core/namespace/AppName.kt`, `core/workspace/WorkspaceConfig.kt`,
  `core/namespace/gen/NamespaceGenerator.kt`, `build.gradle.kts`, `CHANGELOG.md`,
  `src/test/kotlin/.../NamespaceGeneratorRagTest.kt` (новый).

**Конфиги**
- `launcher-public-workspace/`: `enterprise/2026.2.yaml`, `enterprise-rc/2026.3-RC2.yaml`,
  `enterprise-rc/2026.3-RC3.yaml`, `workspace-v1.yml`.
- `launcher-workspaces` (ветка `core`): `workspace-v1.yml`.
- `docker-compose-kit`: `ecos-enterprise/develop/2026.3-RC3/values.yaml` и соседние enterprise-бандлы.

---

## Фаза 1 — фундамент 2.x (не зависит от rag)

### Задача 1: единый резолв образа

**Файлы:**
- Изменить: `internal/namespace/generator_util.go` (рядом с `bundleImageOr`, строка 32)
- Изменить: `internal/namespace/generator_webapp.go:54, 95, 333-335, 858-866` (stt), `574` (alfresco), `708` (observer)
- Изменить: `internal/namespace/generator_infra.go:87-93` (pgadmin)
- Тест: `internal/namespace/generator_image_priority_test.go` (создать)

**Интерфейсы:**
- Производит: `resolveAppImage(ctx *NsGenContext, name, wsImage, nsImage, fallback string) string` —
  используется задачей 5 для qdrant.

- [ ] **Шаг 1: написать падающий тест**

```go
package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Бандл пинит версию релиза, поэтому он сильнее обоих конфигов.
func TestImagePriority_BundleBeatsWorkspaceAndNamespace(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{"emodel": {Image: "ns/emodel:ns"}}
	ws := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID:           "emodel",
			DefaultProps: bundle.WebappDefaultProps{Image: "ws/emodel:ws"},
		}},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.Equal(t, "bundle/emodel:1.0", app.Image)
}

// У приложения без записи в бандле слои конфигурации продолжают работать:
// namespace сильнее workspace-дефолтов.
func TestImagePriority_NamespaceBeatsWorkspaceWhenBundleSilent(t *testing.T) {
	config.SetDesktopMode(true) // pgadmin генерируется только в desktop-режиме
	defer config.ResetDesktopMode()

	cfg := basicCfg()
	cfg.PgAdmin = PgAdminProps{Enabled: true, Image: "ns/pgadmin:ns"}
	ws := &bundle.WorkspaceConfig{PgAdmin: bundle.PgAdminWsProps{Image: "ws/pgadmin:ws"}}

	resp, err := Generate(cfg, &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "pgadmin")
	require.NotNil(t, app)
	assert.Equal(t, "ns/pgadmin:ns", app.Image)
}
```

Типы проверены: `namespace.PgAdminProps{Enabled bool; Image string}` (`internal/namespace/config.go:44-47`)
и `bundle.PgAdminWsProps{Image string}` (`internal/bundle/resolver.go:152-155`), поле воркспейса —
`WorkspaceConfig.PgAdmin` (`resolver.go:274`).

- [ ] **Шаг 2: убедиться, что тест падает**

Запуск: `go test ./internal/namespace/ -run TestImagePriority -v`
Ожидание: `TestImagePriority_BundleBeatsWorkspaceAndNamespace` падает —
получено `ns/emodel:ns`, ожидалось `bundle/emodel:1.0`.

- [ ] **Шаг 3: добавить хелпер**

В `internal/namespace/generator_util.go`, сразу после `bundleImageOr`:

```go
// resolveAppImage picks an app's image according to the layering contract in
// docs/config-layers.md: bundle → namespace.yml → workspace defaults → fallback.
// The bundle wins because it is what pins a release; deviating on a single stand
// is the job of `citeck edit <app>`, which is applied after generation.
func resolveAppImage(ctx *NsGenContext, name, wsImage, nsImage, fallback string) string {
	if ctx.Bundle != nil {
		if app, ok := ctx.Bundle.Applications[name]; ok && app.Image != "" {
			return app.Image
		}
	}
	if nsImage != "" {
		return nsImage
	}
	if wsImage != "" {
		return wsImage
	}
	return fallback
}
```

- [ ] **Шаг 4: перевести вебаппы на хелпер**

В `generator_webapp.go` убрать `app.Image = bundleApp.Image` (строка 54), убрать присваивание
образа из `applyWebappDefaults` (строки 333-335) и из namespace-ветки (строка 95). Вместо них —
один расчёт после того, как известны оба слоя:

```go
	wsImage := ""
	if ctx.WorkspaceConfig != nil {
		for _, wsCfg := range ctx.WorkspaceConfig.Webapps {
			if wsCfg.ID == name {
				wsImage = wsCfg.DefaultProps.Image
				break
			}
		}
	}
	nsImage := ""
	if wp, ok := ctx.Config.Webapps[name]; ok {
		nsImage = wp.Image
	}
	app.Image = resolveAppImage(ctx, name, wsImage, nsImage, "")
	if bundleApp.Image != "" && (wsImage != "" || nsImage != "") {
		slog.Warn("Image override ignored: the bundle pins this app's image; "+
			"use `citeck edit <app>` to deviate on this namespace",
			"app", name, "bundle", bundleApp.Image, "workspace", wsImage, "namespace", nsImage)
	}
```

Предупреждение обязательно: у вебаппа образ в бандле есть всегда (иначе он не генерируется), поэтому
оба конфигурационных ключа для вебаппов становятся мёртвыми — пользователь должен об этом узнать,
а не гадать, почему его правка не сработала.

- [ ] **Шаг 5: перевести pgadmin, stt-sidecar, observer, alfresco**

`generator_infra.go` (pgadmin), вместо цепочки строк 87-93:

```go
	wsImage := ""
	if ctx.WorkspaceConfig != nil {
		wsImage = ctx.WorkspaceConfig.PgAdmin.Image // bundle.PgAdminWsProps
	}
	img := resolveAppImage(ctx, appdef.AppPgadmin, wsImage, ctx.Config.PgAdmin.Image,
		"dpage/pgadmin4:9.15.0")
```

`generator_webapp.go`, в `generateSttSidecar` вместо строк с `props.Image`:

```go
	image := resolveAppImage(ctx, appdef.AppSttSidecar, props.Image, "", "")
	if image == "" {
		return
	}
```

Для observer (`:708`) и alfresco (`:574`) — прочитать текущие цепочки резолва и привести к тому же
виду; если у них образ и так берётся из бандла первым, оставить как есть и записать это в
коммит-сообщение. Обновить доккомментарий `generateSttSidecar` (`:830-831`), где сейчас написано
«workspaceConfig.sttSidecar.image wins». Postgres, keycloak и proxy не трогать. MongoDB не трогать.

- [ ] **Шаг 6: прогнать тесты**

Запуск: `go test ./internal/namespace/ -run TestImagePriority -v` — ожидание PASS.
Затем `go test ./internal/namespace/...` — ожидание PASS (регресса быть не должно, `image` не задан
ни в одном фикстурном конфиге).

- [ ] **Шаг 7: обновить статус в доке**

В `docs/config-layers.md` в разделе «Status» убрать перечисление генераторов, которые ещё не
соответствуют правилу, оставив только исключение про MongoDB.

- [ ] **Шаг 8: коммит**

```bash
git add internal/namespace/generator_util.go internal/namespace/generator_webapp.go \
        internal/namespace/generator_infra.go internal/namespace/generator_image_priority_test.go \
        docs/config-layers.md
git commit -m "fix(namespace): let the bundle win over config when resolving images"
```

### Задача 2: конфигурируемый dependsOn у вебаппов

**Файлы:**
- Изменить: `internal/bundle/resolver.go:57-80` (`WebappDefaultProps`)
- Изменить: `internal/namespace/config.go:61-74` (`WebappProps`)
- Изменить: `internal/namespace/generator_webapp.go` (применение), `internal/namespace/generator.go:253-271` (лог prune)
- Тест: `internal/namespace/generator_dependson_test.go` (создать)

**Интерфейсы:**
- Потребляет: ничего из предыдущих задач.
- Производит: `webappDependsOn(name string, ctx *NsGenContext) []string`;
  `detectDependencyCycles(apps map[string]*AppBuilder) error`.

- [ ] **Шаг 1: написать падающий тест**

```go
package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wsWebappWithDeps(id string, deps []string) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID:           id,
			DefaultProps: bundle.WebappDefaultProps{DependsOn: deps},
		}},
		AdditionalApps: []bundle.AdditionalAppProps{{
			Name:  "sidecar",
			Image: "example/sidecar:1.0",
		}},
	}
}

func TestWebappDependsOn_FromWorkspaceConfig(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(basicCfg(), bun, wsWebappWithDeps("emodel", []string{"sidecar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.Contains(t, []string(app.DependsOn), "sidecar")
	// Захардкоженные зависимости не потеряны.
	assert.Contains(t, []string(app.DependsOn), appdef.AppZookeeper)
	assert.Contains(t, []string(app.DependsOn), appdef.AppRabbitmq)
}

func TestWebappDependsOn_NamespaceOverridesWorkspace(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{"emodel": {DependsOn: []string{}}}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(cfg, bun, wsWebappWithDeps("emodel", []string{"sidecar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.NotContains(t, []string(app.DependsOn), "sidecar",
		"пустой список в namespace.yml снимает зависимость из workspace")
}

func TestWebappDependsOn_SelfDependencyIsRejected(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	_, err := Generate(basicCfg(), bun, wsWebappWithDeps("emodel", []string{"emodel"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "emodel")
}
```

- [ ] **Шаг 2: убедиться, что тест падает**

Запуск: `go test ./internal/namespace/ -run TestWebappDependsOn -v`
Ожидание: компиляция падает — у `bundle.WebappDefaultProps` и `WebappProps` нет поля `DependsOn`.

- [ ] **Шаг 3: расширить схему**

`internal/bundle/resolver.go`, в `WebappDefaultProps`:

```go
	// DependsOn lists apps this webapp must wait for, on top of the ones the
	// generator always adds (zookeeper, rabbitmq, postgres, keycloak). Structural,
	// not a value default — but it lives here because this struct is the per-app
	// workspace layer that namespace.yml already overrides.
	DependsOn []string `yaml:"dependsOn,omitempty"`
```

`internal/namespace/config.go`, в `WebappProps`:

```go
	DependsOn []string `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
```

Тип в namespace-слое — указатель не нужен: отличить «не задано» от «задано пустым» позволяет
`ok`-проверка наличия ключа в `ctx.Config.Webapps`, а пустой список — легитимное «снять зависимости».
Именно это проверяет `TestWebappDependsOn_NamespaceOverridesWorkspace`.

- [ ] **Шаг 4: применить зависимости в генераторе**

В `generator_webapp.go` добавить рядом с `webappEnabled`:

```go
// webappDependsOn resolves the configured dependencies of a webapp: the per-app
// workspace layer, overridden wholesale by namespace.yml when that file mentions
// the app at all (an empty list there deliberately clears them).
func webappDependsOn(name string, ctx *NsGenContext) []string {
	var deps []string
	if ctx.WorkspaceConfig != nil {
		for _, wsCfg := range ctx.WorkspaceConfig.Webapps {
			if wsCfg.ID == name {
				deps = wsCfg.DefaultProps.DependsOn
				break
			}
		}
	}
	if wp, ok := ctx.Config.Webapps[name]; ok && wp.DependsOn != nil {
		deps = wp.DependsOn
	}
	return deps
}
```

И в `generateWebapp`, после блока с захардкоженными `AddDependsOn`:

```go
	for _, dep := range webappDependsOn(name, ctx) {
		if dep == name {
			ctx.DependencyErrors = append(ctx.DependencyErrors,
				fmt.Errorf("webapp %q depends on itself", name))
			continue
		}
		app.AddDependsOn(dep)
	}
```

Поле `DependencyErrors []error` добавить в `NsGenContext` (`internal/namespace/context.go`), а в
`Generate` после генерации всех приложений вернуть первую ошибку.

- [ ] **Шаг 5: добавить проверку циклов**

В `internal/namespace/generator.go`, сразу после `pruneAppsWithMissingDeps` (строка 131):

```go
	if err := detectDependencyCycles(ctx.Applications); err != nil {
		return nil, err
	}
```

Рядом с `pruneAppsWithMissingDeps`:

```go
// detectDependencyCycles fails generation on a dependsOn cycle. Without this a
// cycle is silent: every app in it sits in DEPS_WAITING forever, with nothing in
// the UI explaining why.
func detectDependencyCycles(apps map[string]*AppBuilder) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(apps))
	var path []string

	var visit func(name string) error
	visit = func(name string) error {
		app, ok := apps[name]
		if !ok {
			return nil
		}
		switch color[name] {
		case gray:
			return fmt.Errorf("dependsOn cycle: %s -> %s", strings.Join(path, " -> "), name)
		case black:
			return nil
		}
		color[name] = gray
		path = append(path, name)
		for _, dep := range app.DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		color[name] = black
		return nil
	}

	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names) // детерминированное сообщение об ошибке
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Шаг 6: добавить лог в prune**

В `pruneAppsWithMissingDeps` (`generator.go:253-271`) при удалении приложения:

```go
			slog.Error("App excluded from the namespace: it depends on an app that is not there",
				"app", name, "missingDep", dep)
```

- [ ] **Шаг 7: прогнать тесты**

Запуск: `go test ./internal/namespace/ -run 'TestWebappDependsOn|TestPrune' -v` — PASS.
Затем `go test ./internal/...` — PASS.

- [ ] **Шаг 8: коммит**

```bash
git add internal/bundle/resolver.go internal/namespace/config.go \
        internal/namespace/generator_webapp.go internal/namespace/generator.go \
        internal/namespace/context.go internal/namespace/generator_dependson_test.go
git commit -m "feat(namespace): configurable dependsOn for webapps, with cycle detection"
```

### Задача 3: остановленная зависимость честно удерживает старт

**Файлы:**
- Изменить: `internal/namespace/runtime_loop.go:309-331` (`appsDepsSatisfied`), `:201-215` (переход в DEPS_WAITING)
- Изменить: `internal/i18n/locales/*.json`
- Тест: `internal/namespace/runtime_deps_waiting_test.go` (создать)

**Интерфейсы:**
- Производит: `func (r *Runtime) unmetDeps(app *AppRuntime) []string` — список неудовлетворённых
  зависимостей с их состоянием, для `StatusText`.

- [ ] **Шаг 1: написать падающий тест**

Опереться на существующие рантайм-тесты (`internal/namespace/runtime_*_test.go`) — взять оттуда
способ собрать `Runtime` с фиктивными приложениями. Тест:

```go
func TestDepsWaiting_StoppedDependencyHoldsDependent(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"qdrant": {Name: "qdrant"},
		"rag":    {Name: "rag", DependsOn: appdef.StringSet{"qdrant"}},
	})
	r.manualStoppedApps["qdrant"] = true
	r.apps["qdrant"].Status = AppStatusStopped
	r.apps["rag"].Status = AppStatusReadyToStart

	assert.False(t, r.appsDepsSatisfied(r.apps["rag"]),
		"остановленная зависимость больше не считается удовлетворённой")
	assert.Contains(t, r.unmetDeps(r.apps["rag"]), "qdrant")
}

func TestDepsWaiting_AbsentDependencyStillSatisfied(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"rag": {Name: "rag", DependsOn: appdef.StringSet{"keycloak"}},
	})
	r.apps["rag"].Status = AppStatusReadyToStart

	assert.True(t, r.appsDepsSatisfied(r.apps["rag"]),
		"зависимость вне текущей генерации по-прежнему не блокирует")
}
```

Если хелпера `newTestRuntimeWithApps` нет — написать его в этом же файле, собрав `Runtime`
минимально, как это делают соседние рантайм-тесты.

- [ ] **Шаг 2: убедиться, что тест падает**

Запуск: `go test ./internal/namespace/ -run TestDepsWaiting -v`
Ожидание: `TestDepsWaiting_StoppedDependencyHoldsDependent` падает — `appsDepsSatisfied` вернул `true`.

- [ ] **Шаг 3: убрать ветку и добавить причину**

В `appsDepsSatisfied` удалить блок:

```go
		if r.manualStoppedApps[dep] {
			continue
		}
```

и обновить доккомментарий: остаются только «нет в текущей генерации» и «RUNNING». Рядом добавить:

```go
// unmetDeps lists the dependencies that are keeping app out of STARTING, each
// with the state it is in, so the UI can say what the user has to start. Caller
// must hold r.mu.
func (r *Runtime) unmetDeps(app *AppRuntime) []string {
	var unmet []string
	for _, dep := range app.Def.DependsOn {
		depApp, ok := r.apps[dep]
		if !ok || depApp.Status == AppStatusRunning {
			continue
		}
		unmet = append(unmet, fmt.Sprintf("%s (%s)", dep, depApp.Status))
	}
	return unmet
}
```

- [ ] **Шаг 4: писать причину в StatusText**

В `runtime_loop.go` в обеих точках перехода в `DEPS_WAITING` (`case AppStatusReadyToStart` и
`case AppStatusDepsWaiting`):

```go
			if !r.appsDepsSatisfied(app) {
				app.StatusText = i18n.T("app.status.waitingForDeps",
					"deps", strings.Join(r.unmetDeps(app), ", "))
				r.setAppStatus(app, AppStatusDepsWaiting)
				continue
			}
```

и очищать `app.StatusText = ""` в `beginStartingUnderLock`, чтобы текст не пережил переход.
Контракт: `i18n.T(key string, args ...string)` (`internal/i18n/i18n.go:74`) — плейсхолдеры в
формате `{deps}`, аргументы идут парами ключ-значение.

- [ ] **Шаг 5: добавить ключ во все локали**

В каждый файл `internal/i18n/locales/*.json` добавить ключ `app.status.waitingForDeps`:
- en: `Waiting for: {deps}`
- ru: `Ожидает: {deps}`
- и аналогично для zh, es, de, fr, pt, ja. Плейсхолдер именно `{deps}` в одинарных фигурных скобках —
  так работает подстановка в `i18n.T`.

- [ ] **Шаг 6: прогнать тесты**

Запуск: `go test ./internal/namespace/ -run TestDepsWaiting -v` — PASS.
Затем `go test ./internal/... && cd web && pnpm test` — PASS. Отдельно проверить, что не сломались
тесты про detached-приложения (`go test ./internal/namespace/ -run Detach -v`).

- [ ] **Шаг 7: коммит**

```bash
git add internal/namespace/runtime_loop.go internal/namespace/runtime_deps_waiting_test.go \
        internal/i18n/locales
git commit -m "fix(runtime): a stopped dependency now holds its dependents, and says so"
```

### Задача 4: динамический набор gating-приложений

**Файлы:**
- Изменить: `internal/namespace/generator.go` (`GenResp.GatingApps`), `internal/namespace/context.go` (`NsGenContext.GatingApps`)
- Изменить: `internal/namespace/generator_webapp.go` (`generateSttSidecar`), `internal/namespace/generator_proxy.go`
- Изменить: `internal/daemon/attach_toggle_regen.go:22-26, 30`, плюс плита проброса по образцу `DependsOnDetachedApps` (`internal/daemon/namespace_loader.go:516`, `internal/daemon/server.go:694`)
- Тест: дополнить `internal/namespace/generator_test.go`

**Интерфейсы:**
- Потребляет: ничего.
- Производит: `GenResp.GatingApps map[string]bool`; `ctx.MarkGatingApp(name string)` — вызывается
  задачей 5 из `generateQdrant`.

- [ ] **Шаг 1: написать падающий тест**

```go
func TestGatingApps_ReportedByGenerator(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"ai":          {Image: "bundle/ai:1.0"},
		"stt-sidecar": {Image: "bundle/stt:1.0"},
	}}
	ws := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "ai"}}}

	resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	assert.True(t, resp.GatingApps["ai"],
		"переключение ai меняет состав неймспейса, значит требует регенерации")
}
```

- [ ] **Шаг 2: убедиться, что тест падает**

Запуск: `go test ./internal/namespace/ -run TestGatingApps -v`
Ожидание: компиляция падает — у `GenResp` нет поля `GatingApps`.

- [ ] **Шаг 3: добавить поле и метод**

`internal/namespace/generator.go`, в `GenResp` рядом с `DependsOnDetachedApps`:

```go
	GatingApps map[string]bool // apps whose detach state changes WHICH apps exist
```

`internal/namespace/context.go`, в `NsGenContext` + метод:

```go
	GatingApps map[string]bool
```

```go
// MarkGatingApp records that the detach state of `name` decides whether some
// other app is generated at all. The daemon regenerates the namespace when such
// an app is started or stopped; without that, the dependent app would appear or
// disappear only on the next unrelated reload.
func (c *NsGenContext) MarkGatingApp(name string) {
	if c.GatingApps == nil {
		c.GatingApps = map[string]bool{}
	}
	c.GatingApps[name] = true
}
```

Инициализировать в конструкторе контекста рядом с `DetachedApps` и прокинуть в `GenResp`.

- [ ] **Шаг 4: расставить вызовы**

- `generateSttSidecar` — `ctx.MarkGatingApp(appdef.AppAi)` в начале, сразу после проверки наличия `ai`.
- `generateProxy` — `ctx.MarkGatingApp(appdef.AppOnlyoffice)` и `ctx.MarkGatingApp(appdef.AppAlfresco)`
  там, где сейчас читается их detach-состояние.

- [ ] **Шаг 5: перевести демон на динамический набор**

В `internal/daemon/attach_toggle_regen.go` удалить хардкод-набор (строки 22-26) и переписать
`regenOnAttachToggle` так, чтобы он спрашивал у рантайма набор из последней генерации. Пробросить
`GatingApps` тем же путём, каким уже проброшен `DependsOnDetachedApps`
(`namespace_loader.go:516`, `server.go:694`), и добавить рантайму геттер по образцу
`ManualStoppedApps()`.

- [ ] **Шаг 6: прогнать тесты**

Запуск: `go test ./internal/namespace/ -run TestGatingApps -v` — PASS.
Затем `go test ./internal/...` — PASS. Особое внимание тестам демона про toggle onlyoffice/ai.

- [ ] **Шаг 7: коммит**

```bash
git add internal/namespace/generator.go internal/namespace/context.go \
        internal/namespace/generator_webapp.go internal/namespace/generator_proxy.go \
        internal/daemon internal/namespace/generator_test.go
git commit -m "refactor(daemon): derive the regenerate-on-toggle set from generation"
```

---

## Фаза 2 — rag и qdrant в 2.x

### Задача 5: генерация qdrant и обвязка rag/ai

**Файлы:**
- Создать: `internal/namespace/generator_qdrant.go`
- Создать: `internal/namespace/generator_qdrant_test.go`
- Изменить: `internal/appdef/appdef.go:229-255` (константы), `internal/bundle/resolver.go` (`QdrantProps`), `internal/namespace/generator.go` (вызов)

**Интерфейсы:**
- Потребляет: `resolveAppImage` (задача 1), `ctx.MarkGatingApp` (задача 4).
- Производит: `generateQdrant(ctx *NsGenContext)`; константы `appdef.AppRag`, `appdef.AppQdrant`.

- [ ] **Шаг 1: написать падающий тест**

```go
package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ragBundle() *bundle.Def {
	return &bundle.Def{Applications: map[string]bundle.AppDef{
		"rag":    {Image: "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"},
		"qdrant": {Image: "qdrant/qdrant:v1.14.1"},
		"ai":     {Image: "harbor.citeck.ru/enterprise/ai:1.12.0"},
	}}
}

func ragWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{
		{ID: "rag", Aliases: []string{"EcosRagApp"}},
		{ID: "ai", Aliases: []string{"EcosAiApp"}},
	}}
}

func TestQdrant_GeneratedWhenRagIsPresent(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant, "qdrant генерируется вместе с rag")
	assert.Equal(t, "qdrant/qdrant:v1.14.1", qdrant.Image)
	assert.Equal(t, appdef.KindThirdParty, qdrant.Kind)

	rag := findGeneratedApp(resp, appdef.AppRag)
	require.NotNil(t, rag)
	host, ok := rag.Environments.Get("QDRANT_HOST")
	require.True(t, ok)
	assert.Equal(t, appdef.AppQdrant, host)
	port, ok := rag.Environments.Get("QDRANT_GRPC_PORT")
	require.True(t, ok)
	assert.Equal(t, "6334", port)
	assert.Contains(t, []string(rag.DependsOn), appdef.AppQdrant)

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	enabled, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	require.True(t, ok, "иначе ассистент не воспользуется rag: флаг в ai по умолчанию false")
	assert.Equal(t, "true", enabled)
}

func TestQdrant_AbsentWhenRagIsNotInTheBundle(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"ai": {Image: "harbor.citeck.ru/enterprise/ai:1.12.0"},
	}}

	resp, err := Generate(basicCfg(), bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	assert.Nil(t, findGeneratedApp(resp, appdef.AppRag), "community-бандл не должен приносить rag")
	assert.Nil(t, findGeneratedApp(resp, appdef.AppQdrant))

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	assert.False(t, ok, "без rag флаг не выставляется")
}

func TestQdrant_AbsentWhenRagIsDetached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppRag: true}})
	require.NoError(t, err)

	assert.NotNil(t, findGeneratedApp(resp, appdef.AppRag), "спека rag остаётся, чтобы её можно было включить")
	assert.Nil(t, findGeneratedApp(resp, appdef.AppQdrant), "выключенный rag не тянет за собой qdrant")

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	assert.False(t, ok)
}

func TestQdrant_MarksRagAsGating(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.True(t, resp.GatingApps[appdef.AppRag],
		"без этого Start на rag не перегенерирует неймспейс и qdrant не появится")
}
```

Сигнатура генератора — `Generate(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig,
secrets SystemSecrets, opts ...GenerateOpts) (*GenResp, error)` (`internal/namespace/generator.go:61`);
хелпер `findGeneratedApp(resp *GenResp, name string) *appdef.ApplicationDef` уже есть в
`internal/namespace/generator_test.go:15`.

- [ ] **Шаг 2: убедиться, что тест падает**

Запуск: `go test ./internal/namespace/ -run TestQdrant -v`
Ожидание: компиляция падает — нет `appdef.AppQdrant`.

- [ ] **Шаг 3: добавить константы и props**

`internal/appdef/appdef.go`, рядом с `AppAi` и `AppSttSidecar`:

```go
	AppRag    = "rag"
	AppQdrant = "qdrant"
```

`internal/bundle/resolver.go`, рядом с `SttSidecarProps`:

```go
// QdrantProps configures the Qdrant vector store that backs the rag webapp.
// There is deliberately no image key: the version is pinned by the bundle.
type QdrantProps struct {
	MemoryLimit string `yaml:"memoryLimit,omitempty"`
	GrpcPort    int    `yaml:"grpcPort,omitempty"`
}
```

и поле `Qdrant *QdrantProps \`yaml:"qdrant,omitempty"\`` в `WorkspaceConfig`.

- [ ] **Шаг 4: написать генератор**

Создать `internal/namespace/generator_qdrant.go`:

```go
package namespace

import (
	"fmt"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Qdrant defaults. The HTTP port is fixed: 6333 is where /healthz lives, while
// the app talks gRPC on 6334.
const (
	qdrantDefaultGrpcPort = 6334
	qdrantHTTPPort        = 6333
	qdrantDefaultMemory   = "1g"
)

// generateQdrant adds the Qdrant vector store for the rag webapp, mirroring
// generateSttSidecar. Behavior:
//   - No rag in the generated set → no qdrant (this is what keeps qdrant off
//     community stands: rag itself only exists when the bundle carries EcosRagApp).
//   - rag detached → no qdrant at all, so a switched-off RAG costs no memory.
//     Starting rag regenerates the namespace (rag is marked as a gating app) and
//     qdrant appears with it.
//   - Image comes from the bundle only; the version is pinned by the release.
func generateQdrant(ctx *NsGenContext) {
	ragApp, ok := ctx.Applications[appdef.AppRag]
	if !ok {
		return
	}
	// Toggling rag decides whether qdrant exists, so the daemon must regenerate
	// on that toggle — mark it even when rag is currently detached.
	ctx.MarkGatingApp(appdef.AppRag)
	if ctx.DetachedApps[appdef.AppRag] {
		return
	}

	props := bundle.QdrantProps{}
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Qdrant != nil {
		props = *ctx.WorkspaceConfig.Qdrant
	}
	grpcPort := props.GrpcPort
	if grpcPort <= 0 {
		grpcPort = qdrantDefaultGrpcPort
	}
	memoryLimit := props.MemoryLimit
	if memoryLimit == "" {
		memoryLimit = qdrantDefaultMemory
	}

	image := resolveAppImage(ctx, appdef.AppQdrant, "", "", "")
	if image == "" {
		slog.Error("Bundle has no qdrant image; rag will start without a vector store",
			"app", appdef.AppQdrant)
		return
	}

	qdrant := ctx.GetOrCreateApp(appdef.AppQdrant)
	qdrant.Image = image
	qdrant.Kind = appdef.KindThirdParty
	qdrant.AddVolume("qdrant_storage:/qdrant/storage")
	qdrant.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			HTTP:             &appdef.HTTPProbeDef{Path: "/healthz", Port: qdrantHTTPPort},
			PeriodSeconds:    5,
			FailureThreshold: 10000, // как у STT: реальный потолок — внешнее ожидание запуска
			TimeoutSeconds:   5,
		}},
	}
	qdrant.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: memoryLimit}}

	ragApp.AddEnv("QDRANT_HOST", appdef.AppQdrant)
	ragApp.AddEnv("QDRANT_GRPC_PORT", fmt.Sprintf("%d", grpcPort))
	ragApp.AddDependsOn(appdef.AppQdrant)

	// The assistant ships with citeck.ai.rag.enabled=false, so without this flag
	// a user who starts rag still gets no RAG tools in ai.
	if aiApp, ok := ctx.Applications[appdef.AppAi]; ok && !ctx.DetachedApps[appdef.AppAi] {
		aiApp.AddEnv("CITECK_AI_RAG_ENABLED", "true")
	}
}
```

- [ ] **Шаг 5: вызвать генератор**

В `internal/namespace/generator.go` — сразу после `generateSttSidecar(ctx)`:

```go
	generateQdrant(ctx)
```

- [ ] **Шаг 6: прогнать тесты**

Запуск: `go test ./internal/namespace/ -run TestQdrant -v` — PASS.
Затем `make check` — PASS целиком.

- [ ] **Шаг 7: коммит**

```bash
git add internal/appdef/appdef.go internal/bundle/resolver.go \
        internal/namespace/generator_qdrant.go internal/namespace/generator_qdrant_test.go \
        internal/namespace/generator.go
git commit -m "feat(namespace): generate qdrant for the rag webapp, gated on rag itself"
```

### Задача 6: changelog 2.11.0

**Файлы:**
- Создать: `changelog/2.11.0/{en,ru,zh,es,de,fr,pt,ja}.md`
- Изменить: `changelog/index.json`

- [ ] **Шаг 1: прочитать правила**

Прочитать `changelog/AGENTS.md` целиком и написать текст от **чистого диффа** против `v2.10.0`,
а не по списку коммитов.

- [ ] **Шаг 2: написать восемь файлов**

Содержание (по-русски, остальные локали — перевод того же):
- Поддержка RAG: приложение `rag` появляется на enterprise-бандлах выключенным; при запуске
  автоматически поднимается Qdrant, а ассистенту включается доступ к базе знаний.
- `dependsOn` у веб-приложений теперь задаётся конфигурацией.
- Приложение с остановленной зависимостью больше не стартует «мимо» неё, а ждёт и показывает,
  чего именно ждёт. **Изменение поведения.**
- Образ, запиненный в бандле, больше не перебивается конфигурацией; подменить его на своём стенде
  можно через `citeck edit <app>`. **Изменение поведения.**

- [ ] **Шаг 3: зарегистрировать релиз**

Добавить в `changelog/index.json`: `{ "version": "2.11.0", "date": "<дата коммита YYYY-MM-DD>" }`.

- [ ] **Шаг 4: проверить**

Запуск: `go test ./internal/update/ -run Changelog -v` — PASS.

- [ ] **Шаг 5: коммит**

```bash
git add changelog/
git commit -m "docs(changelog): release notes for 2.11.0 in all 8 locales"
```

---

## Фаза 3 — 1.x

### Задача 7: rag и qdrant в Kotlin-лончере

**Файлы (ветка `release/1.4.2` от тега `v1.4.1` в том же репозитории):**
- Изменить: `src/main/kotlin/ru/citeck/launcher/core/namespace/AppName.kt`
- Изменить: `src/main/kotlin/ru/citeck/launcher/core/workspace/WorkspaceConfig.kt:18, 92-100`
- Изменить: `src/main/kotlin/ru/citeck/launcher/core/namespace/gen/NamespaceGenerator.kt:87, 110`
- Создать: `src/test/kotlin/ru/citeck/launcher/core/namespace/gen/NamespaceGeneratorRagTest.kt`

**Интерфейсы:**
- Потребляет: ничего из фаз 1-2 (ветки независимы).
- Производит: `NamespaceGenerator.generateQdrant(context: NsGenContext)`.

- [ ] **Шаг 1: создать ветку**

```bash
git checkout -b release/1.4.2 v1.4.1
```

- [ ] **Шаг 2: написать падающий тест**

Создать `NamespaceGeneratorRagTest.kt` по образцу `NamespaceGeneratorAiTest.kt` (тот же
`createContext`-подход и `NamespaceGeneratorTestFixture`):

```kotlin
package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorRagTest {

    private val ragImage = "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"
    private val qdrantImage = "qdrant/qdrant:v1.14.1"

    private fun createContext(
        detachedApps: Set<String> = emptySet(),
        withRagInBundle: Boolean = true
    ): NsGenContext {
        val bundleApps = mutableMapOf<String, BundleDef.BundleAppDef>(
            AppName.QDRANT to BundleDef.BundleAppDef(qdrantImage)
        )
        if (withRagInBundle) {
            bundleApps[AppName.RAG] = BundleDef.BundleAppDef(ragImage)
        }
        val context = NsGenContext(
            namespaceConfig = NamespaceConfig.DEFAULT,
            bundle = BundleDef(
                key = BundleKey("1.0.0"),
                applications = bundleApps,
                citeckApps = emptyList()
            ),
            workspaceConfig = WorkspaceConfig(
                imageRepos = emptyList(),
                bundleRepos = emptyList(),
                webapps = listOf(WorkspaceConfig.AppConfig(AppName.RAG))
            ),
            files = HashMap(),
            detachedApps = detachedApps
        )
        if (withRagInBundle) {
            context.getOrCreateApp(AppName.RAG).withImage(ragImage)
        }
        return context
    }

    @Test
    fun `qdrant is generated and wired when rag is active`() {
        val context = createContext()
        NamespaceGenerator.generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]
        assertThat(qdrant).isNotNull
        assertThat(qdrant!!.image).isEqualTo(qdrantImage)

        val rag = context.applications[AppName.RAG]!!
        assertThat(rag.environments["QDRANT_HOST"]).isEqualTo(AppName.QDRANT)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("6334")
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `qdrant is not generated without rag`() {
        val context = createContext(withRagInBundle = false)
        NamespaceGenerator.generateQdrant(context)
        assertThat(context.applications[AppName.QDRANT]).isNull()
    }

    @Test
    fun `qdrant is not generated when rag is detached`() {
        val context = createContext(detachedApps = setOf(AppName.RAG))
        NamespaceGenerator.generateQdrant(context)
        assertThat(context.applications[AppName.QDRANT]).isNull()
    }
}
```

Способ чтения env и `dependsOn` у билдера подсмотреть в `NamespaceGeneratorAiTest.kt` и привести к
нему — API билдера в 1.x свой.

- [ ] **Шаг 3: убедиться, что тест падает**

Запуск: `./gradlew test --tests '*NamespaceGeneratorRagTest*'`
Ожидание: компиляция падает — нет `AppName.RAG`.

- [ ] **Шаг 4: добавить константы и props**

`AppName.kt`, в секцию `// enterprise`:

```kotlin
    const val RAG = "rag"
    const val QDRANT = "qdrant"
```

`WorkspaceConfig.kt`, рядом с `SttSidecarProps`:

```kotlin
    class QdrantProps(
        val memoryLimit: String = "1g",
        val grpcPort: Int = 6334
    ) {
        companion object {
            val DEFAULT = QdrantProps()
        }
    }
```

и поле в конструкторе `WorkspaceConfig`: `val qdrant: QdrantProps = QdrantProps.DEFAULT`.

- [ ] **Шаг 5: написать генератор**

`NamespaceGenerator.kt`, рядом с `generateSttSidecar`:

```kotlin
    internal fun generateQdrant(context: NsGenContext) {
        val ragApp = context.applications[AppName.RAG] ?: return
        if (context.detachedApps.contains(AppName.RAG)) {
            return
        }

        val props = context.workspaceConfig.qdrant
        val image = context.bundle.applications[AppName.QDRANT]?.image?.takeIf { it.isNotBlank() }
            ?: return

        context.getOrCreateApp(AppName.QDRANT)
            .withImage(image)
            .addVolume("qdrant_storage:/qdrant/storage")
            .withKind(ApplicationKind.THIRD_PARTY)
            .withStartupCondition(
                StartupCondition(
                    probe = AppProbeDef(http = HttpProbeDef("/healthz", 6333))
                )
            )
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef(props.memoryLimit)
                )
            )

        ragApp.addEnv("QDRANT_HOST", AppName.QDRANT)
            .addEnv("QDRANT_GRPC_PORT", props.grpcPort.toString())
            .addDependsOn(AppName.QDRANT)

        val aiApp = context.applications[AppName.AI]
        if (aiApp != null && !context.detachedApps.contains(AppName.AI)) {
            aiApp.addEnv("CITECK_AI_RAG_ENABLED", "true")
        }
    }
```

Вызвать в `generate()` сразу после `generateSttSidecar(context)` (строка 87).

- [ ] **Шаг 6: включить rag в набор регенерации**

`NamespaceGenerator.kt:110` — добавить `AppName.RAG` в `setOf(...)`, возвращаемый как
`dependsOnDetachedApps`. Без этого Start на rag не вызовет `RegenerateNsCmd` и qdrant не появится.

- [ ] **Шаг 7: прогнать тесты**

Запуск: `./gradlew test --tests '*NamespaceGeneratorRagTest*'` — PASS.
Затем `./gradlew build` — PASS (ktlint в том числе).

- [ ] **Шаг 8: коммит**

```bash
git add src/main/kotlin src/test/kotlin
git commit -m "feat(namespace): optional rag webapp with its qdrant vector store"
```

### Задача 8: подготовка релиза 1.4.2

**Файлы:** `build.gradle.kts:18`, `CHANGELOG.md`

- [ ] **Шаг 1: поднять версию**

В `build.gradle.kts:18` — `version = "1.4.2"`.

- [ ] **Шаг 2: написать секцию changelog**

В начало `CHANGELOG.md` добавить секцию с заголовком ровно `# Release 1.4.2` (workflow вырезает её
по точному совпадению с версией тега, иначе в релизе будет «No description»):

```markdown
# Release 1.4.2

- Поддержка RAG: на enterprise-бандлах появляется выключенное приложение `rag`. При его запуске
  автоматически поднимается векторная база Qdrant, а AI-ассистенту включается доступ к базе знаний.
```

- [ ] **Шаг 3: собрать дистрибутив локально**

Запуск: `./gradlew packageDist`
Ожидание: артефакт `citeck-launcher_1.4.2_<os>.<ext>` собран без ошибок.

- [ ] **Шаг 4: коммит**

```bash
git add build.gradle.kts CHANGELOG.md
git commit -m "Release 1.4.2"
```

- [ ] **Шаг 5: остановиться и спросить**

Тег `v1.4.2` **не создавать и не пушить** без явного разрешения: пуш тега запускает публикацию
GitHub Release. Показать пользователю собранный артефакт и спросить, пушить ли.

---

## Фаза 4 — конфигурация и проверка

### Задача 9: объявить qdrant в enterprise-бандлах

**Файлы:**
- Изменить: `launcher-public-workspace/enterprise/2026.2.yaml`,
  `launcher-public-workspace/enterprise-rc/2026.3-RC2.yaml`, `.../2026.3-RC3.yaml`
- Изменить: `docker-compose-kit/ecos-enterprise/develop/2026.3-RC3/values.yaml` и соседние
  enterprise-бандлы, где есть `EcosRagApp`

- [ ] **Шаг 1: найти все бандлы с rag**

```bash
grep -rln "EcosRagApp" launcher-public-workspace docker-compose-kit
```

- [ ] **Шаг 2: добавить запись в каждый из них**

Сразу после блока `EcosRagApp`, тем же отступом:

```yaml
qdrant:
  image:
    repository: qdrant/qdrant
    tag: v1.14.1
```

Ключ — `qdrant` в нижнем регистре: не-вебаппы объявляются в бандлах каноническим именем
(как соседний `stt-sidecar`), а не алиасом.

- [ ] **Шаг 3: проверить резолв образа**

Убедиться, что `qdrant` не совпадает ни с одним `imageRepos[].id` в обоих workspace-конфигах
(иначе `resolveImageURL` перепишет ссылку в реестр). Сейчас там только `core` и `enterprise`.

- [ ] **Шаг 4: коммит в каждом репозитории**

```bash
git commit -am "Add qdrant image to enterprise bundles"
```

### Задача 10: объявить rag в workspace-конфигах

**Файлы:**
- Изменить: `launcher-public-workspace/workspace-v1.yml`
- Изменить: `launcher-workspaces` (ветка `core`) `workspace-v1.yml`

- [ ] **Шаг 1: добавить вебапп**

В конец списка `webapps:` (после записи `ai`):

```yaml
  - id: rag
    aliases: [ 'EcosRagApp' ]
    defaultProps:
      heapSize: 512m
      memoryLimit: 1200m
```

- [ ] **Шаг 2: сделать rag выключенным по умолчанию**

В `namespaceTemplates[].detachedApps` шаблона `default` добавить `rag`. В публичном конфиге секции
`detachedApps` сейчас нет — создать её со списком из одного `rag`. В enterprise-конфиге
(`launcher-workspaces@core`) — дописать `rag` к существующему списку
(`onlyoffice, edi, integrations, content, attorneys, ai, stt-sidecar`).

**`qdrant` в `detachedApps` не добавлять** — он существует только вместе с включённым rag и должен
стартовать автоматически.

- [ ] **Шаг 3: не коммитить сразу**

Этот шаг выкатывается **последним**, после того как 1.4.2 разойдётся по пользователям (см. §4
спеки). Подготовить изменение, показать пользователю и дождаться отмашки на коммит и пуш.

### Задача 11: живая проверка

- [ ] **Шаг 1: собрать 2.x**

```bash
make build && ls -la dist/bin/citeck-server
```

- [ ] **Шаг 2: поднять неймспейс на enterprise-бандле**

Использовать локальные копии конфигов с правками из задач 9-10 (workspace-репозиторий можно
подсунуть через `WorkspaceRepoOpts`/локальный путь, чтобы не пушить незакоммиченное). Доступ к
`harbor.citeck.ru` у пользователя уже настроен.

- [ ] **Шаг 3: проверить исходное состояние**

```bash
dist/bin/citeck-server status
```
Ожидание: `rag` присутствует со статусом STOPPED, `qdrant` в списке **отсутствует**.

- [ ] **Шаг 4: запустить rag**

```bash
dist/bin/citeck-server start rag
```
Ожидание: неймспейс регенерируется, появляется `qdrant`, он выходит в RUNNING первым, `rag`
проходит через DEPS_WAITING и затем RUNNING.

- [ ] **Шаг 5: проверить обвязку**

```bash
dist/bin/citeck-server exec rag -- env | grep QDRANT
dist/bin/citeck-server exec ai  -- env | grep CITECK_AI_RAG_ENABLED
dist/bin/citeck-server exec rag -- curl -sf localhost:8614/management/health
```
Ожидание: `QDRANT_HOST=qdrant`, `QDRANT_GRPC_PORT=6334`, `CITECK_AI_RAG_ENABLED=true`,
health отвечает. Точный синтаксис `exec` уточнить по `citeck exec --help`.

- [ ] **Шаг 6: проверить обратный ход**

```bash
dist/bin/citeck-server stop rag
dist/bin/citeck-server status
```
Ожидание: `qdrant` исчезает из списка, его контейнер удалён.

- [ ] **Шаг 7: скриншот UI**

Снять скриншот списка приложений в веб-UI до и после старта rag (Playwright, см.
`AGENTS.md` → «Visual screenshots via Playwright»).

- [ ] **Шаг 8: отчитаться честно**

Записать в отчёт, что индексация и семантический поиск **не проверялись** — `CTK_OPENAI_API_KEY`
не предоставлялся. Проверены: гейт, регенерация по toggle, порядок старта, env, `dependsOn`,
health контейнеров.
