# Ревью feature/content-storage-s3

Дата ревью: 2026-10-06. Последующее изменение контракта: 2026-10-07. Репозиторий: citeck-launcher.

После ревью владелец уточнил контракт: используем штатное S3-хранилище, секреты и endpoint через env; единственная remote mutation — default config с чтением до и после, без условной записи. Прежние выводы о создании записей и синхронизации ключа относятся к удалённой реализации. Защиты WAL, epoch и data barrier сохранены. Повтор ошибок теперь имеет собственный таймер. Актуальное поведение описано в docs/content-storage.md; проверка ниже относится к завершённому ревью до этого изменения.

Проверен весь диапазон `ecb8616af5c554dc2b2c7168d88660ab26a8bb7a..34fe30917cfd5ea9dd93798fe8ce5fbe54e006f6`: 17 коммитов, 130 файлов, 13 536 изменённых строк, плюс исправления текущего ревью. База — merge-base с master; прежнее состояние ревью не ограничивало охват. Все исходные изменённые файлы учтены в списках покрытия трёх ревьюеров и контроллера.

Применён Claude-скилл `review-changes`: первый проход по областям, проверка каждого замечания, исправления, регрессии и выполненные мутации, затем независимый общий проход и повторная проверка последнего исправления. Sonnet/Opus недоступны в runtime: использованы доступные агенты, без имитации этих моделей. Повторный общий проход по найденным дефектам: HIGH/MEDIUM/LOW отсутствуют, `ready_to_finalize: yes`. Дополнительная оценка сопровождаемости 2026-10-07 приведена ниже: отсутствие блокеров не означает полного отсутствия архитектурного долга.

## Находки и решения

Таблица сохраняет пути и номера строк на момент завершения ревью. После уточнения S3-контракта часть модулей сокращена или удалена; актуальные изменения описаны в последнем разделе. Исходные доказательства — под `.tmp/review-content-storage-s3/` в корне solution. Все находки — `introduced`.

| # | Фаза | Итер | Severity | Origin | File:Line | Проблема | Резолюция |
|---|------|------|----------|--------|-----------|----------|-----------|
| 1 | 1 | 1 | HIGH | introduced | `internal/daemon/content_storage_provision_connection.go:218` | Ошибка сохранения pending marker игнорировалась: после создания storage и падения демона незавершённое переключение читалось как выбор администратора | Исправлено: успешная запись состояния обязательна до ECOS; неизменённый marker повторно сохраняется при задолженности |
| 2 | 1 | 1 | HIGH | introduced | `internal/daemon/content_storage_provision_connection.go:572` | Snapshot мог начаться после чтения или между remote writes, а подключение продолжало менять ECOS | Исправлено: каждая mutation проверяет платформу и удерживает RW barrier против snapshot/migration/rollback на время одного HTTP-запроса |
| 3 | 1 | 1 | HIGH | introduced | `internal/daemon/content_storage_provision.go:1` | Новый модуль на 1057 строк превышал ограничение скилла и совмещал планирование задач и протокол подключения | Исправлено ограничение размера: разделён на модули 539 и 587 строк; транспорт и owned-record sync отдельно. Это не полное разделение scheduler и runner, см. архитектурную оценку ниже |
| 4 | 1 | 1 | MEDIUM | introduced | `internal/daemon/content_storage_test.go:303` | Проверка восстановления искала текст вызовов в исходнике и оставалась зелёной при отключении поведения | Исправлено: настоящий `loadNamespace` читает persisted state; тест проверяет восстановленные marker и secret-write record |
| 5 | 1 | 1 | HIGH | introduced | `internal/daemon/routes_admin_password.go:200` | A→B→C без recreate скрывало предупреждение о контейнере, всё ещё принимающем A | Исправлено: поля ADMIN_PASSWORD объявленного additionalApp сопоставляются с deployed env/cmd и новым паролем; прежний эвристический fallback сохранён для приложений без метаданных |
| 6 | 1 | 1 | HIGH | introduced | `internal/cli/setup/undo.go:154` | Undo смены бандла оставлял S3, но мог вернуть бандл без content/provider, обходя availability gate | Исправлено: read-only проверка target bundle у демона до записи; поддерживаемый undo работает, недоступный daemon/неподдерживаемый bundle не меняет файл и историю |
| 7 | 1 | 1 | HIGH | introduced | `web/src/components/AppTable.tsx:77` | Открытое подтверждение stop при одинаковом namespace id в другом workspace могло остановить новый provider | Исправлено: подтверждение учитывает workspace и namespace |
| 8 | 1 | 1 | MEDIUM | introduced | `web/src/components/ContentStorageBanner.tsx:119` | Скрытие progress для A/default скрывало баннер B/default | Исправлено: ключ dismissal — workspace/namespace pair |
| 9 | 2 | 1 | HIGH | introduced | `internal/daemon/content_storage_provision.go:309` | Отложенная оценка старого active snapshot после Reset отменяла уже начатую задачу нового namespace; последний qualifying event терялся | Исправлено: epoch снимается до чтения active и проверяется под mutex до adopt; Reset повышает epoch под тем же mutex |

В исходных ревью-итерациях ошибочно отброшенных и отложенных замечаний к изменяемому коду не было. Последующая оценка сопровождаемости выделяет отдельные направления улучшения ниже. Замечания линтера к новым исправлениям (shadow/unparam/wrapcheck) исправлены без подавления правил.

## Проверки

Новые регрессии сначала выполнены на дефектном коде (RED), затем на исправленном (GREEN). Выполнены 11 отдельных мутаций: игнорирование WAL error; отключение retry dirty unchanged marker; отключение platform gate; отключение RW barrier; отключение каждого из двух restore calls; отключение ADMIN_PASSWORD metadata; отключение undo availability; отключение workspace в stop confirmation; отключение workspace в dismissal; отключение epoch comparison. Каждая мутация сломала соответствующий тест; все мутации восстановлены из точных резервных копий.

- Scoped Go suites и race suites: прошли.
- Vitest: 41 файл, 445 тестов — прошли.
- Web eslint: прошёл.
- `go vet ./...` и pinned golangci-lint 2.11.4: прошли, `0 issues`.
- Chromium: реальные screenshots dashboard en/ru, provider-stop modal, namespace S3 dialog; проверены изоляция workspace и неизменяемый выбор S3. API в браузерном прогоне изолирован mock; живой ECOS stack не запускался.
- Полный Go `-count=1 -race -cover ./internal/...`: все тесты прошли с адаптацией временного окружения; два export-теста выполнены с каноническим путём отдельно, остальные — с коротким fd alias. Итоговый полный прогон: `check-blocker.log`; ни один тест суммарно не пропущен.
- Coverage floors: прошли (daemon 68.0%, namespace 85.1%, storage 66.4%, appdef 86.8%, jvmattach 80.5%). Для daemon взята консервативная coverage основной группы; отдельно выполненные export-тесты могут только увеличить объединённое покрытие. Первоначальный wrapper выводил первой coverage двух export-тестов (2.0%), что Go driver ошибочно показывал как coverage всего пакета; после изменения порядка измерение основной группы повторено. `coverage-final.txt` объединяет выполненные результаты полных пакетов и повторного daemon-прогона, `coverage-floor-final.log` хранит итог.
- `govulncheck`: 0 reachable findings; passed.
- `deadcode` с desktop,gtk3: 3 известных allowlisted functions, новых нет; passed.
- `make build`: TypeScript + Vite с очисткой web output и server binary; passed. Предупреждение Vite о крупном chunk сохраняется, сборку не блокирует.
- Linux/arm64 server build, Windows/amd64 `go vet ./internal/...`, `go vet -tags integration` для daemon/namespace/tests: passed.
- `pnpm audit --prod --audit-level high`: passed после обновления source-map-js, `No known vulnerabilities found`. Полный gate после обновления прошёл: все 10 шагов, включая audit, сборку и кросс-проверки. Итоговый лог — `.tmp/review-content-storage-s3/check-blocker.log`.

Временные файлы находятся только внутри solution `.tmp`. Для UNIX-socket тестов длинный путь solution превышает лимит pathname, поэтому test harness использует короткий `/proc/<parent-pid>/fd/9` адрес того же каталога. Тесты канонических export paths требуют другой формы пути; harness адаптирует окружение без изменений production кода. Посторонние `.astro/` и `verify-out/` удалены при очистке репозитория; правки, отчёт и доказательства ревью сохранены.

## Блокер npm audit исправлен

В исходном диапазоне S3 уязвимая `source-map-js@1.2.1` уже присутствовала в базе `ecb8616`; она блокировала `pnpm audit` по [GHSA-68fv-2mgg-jv7q](https://github.com/advisories/GHSA-68fv-2mgg-jv7q), High. Блокер исправлен: в `web/package.json` добавлен override `source-map-js: >=1.2.2`, а lockfile и установленное дерево используют только 1.2.2. Обновлены все три пути: @tailwindcss/node, css-tree и postcss. Остальные версии зависимостей не менялись, advisory не подавляется. После установки `pnpm audit --prod --audit-level high` отвечает `No known vulnerabilities found`.

## Контракты и ограничения

- WAL marker должен быть durable до remote side effects; retry flag в памяти сам по себе не восстанавливает решение после падения.
- Проверка active snapshot до mutex требует invalidation generation: отмена старого job не отменяет ещё не зарегистрированную старую evaluation.
- Барьер данных нужен на каждую mutation, но не на 15-минутное ожидание готовности; обычные lifecycle requests и update pass сохраняют прежнюю конкуренцию.
- S3 provider и его image — внешняя конфигурация. В локальном sibling `launcher-public-workspace` main на `a33a286` S3 blocks отсутствуют; эталонный fixture не доказывает публикацию во внешнем workspace. Для появления варианта S3 необходимо публиковать согласованные workspace blocks и image в target bundle. Другие member repositories не изменялись.
- Проверка admin-password metadata относится к env/cmd текущих raw additionalApps workspace; если источник удалён либо приложение вообще не объявлено там, остаётся прежнее best-effort сравнение с предыдущим паролем.

Коммитов, push и публикаций в ходе ревью не выполнялось. Глобальные Claude review-state и memory вне solution не изменялись; итог и доказательства сохранены внутри solution и в этой документации.

## Архитектурная оценка и локальный рефакторинг — 2026-10-07

Блокирующего архитектурного дефекта после исправлений не выявлено. Основные границы выбраны правильно: bundle читает конфигурацию, namespace определяет доступность и генерацию, runtime владеет persisted state, daemon выполняет внешнее подключение, HTTP-транспорт отделён интерфейсом, UI отображает DTO. Замечания ниже закрыты локальным рефакторингом 2026-10-07:

1. **Writers namespace config согласованы.** Обычные CLI setup и undo используют один writer. Online-запись проходит через daemon `PUT /config` и общий write gate; offline-запись использует чистую transition policy из namespace. Смена бандла на S3 без daemon отклоняется. Отказ online-записи не включает fallback на файл. Исходная конфигурация сверяется с активной сохранённой конфигурацией перед записью. Семантика отдельного reload для обычных правок и undo сохранена.
2. **Connection runner отделён от scheduler.** `csConnectionRunner` не содержит `*Daemon`, job или полного runtime. Протокол ECOS получает неизменяемые настройки, read-only историю и узкие callbacks. Epoch, latch, cancellation, single-flight, таймер повтора, проверки текущего job и persistence остаются у scheduler. Независимый тест runner проверяет WAL → mutation → readback → confirmation и отсутствие записи при отказе WAL.
3. **Общая long-op policy перенесена в `longop.go`.** S3 использует правила владельца блокировки, а не определяет их для общего механизма.
4. **Go reason → message mapping общий.** Builder `msg.Message` находится в namespace; CLI и daemon только применяют локаль. Проверка всех восьми locale assets охватывает перенесённый builder.

**Замечание на будущее:** admin-password warning разбирает raw additionalApps env/cmd. При расширении видов credential rotation стоит передавать происхождение полей из генератора. Новых видов ротации этот рефакторинг не добавляет; существующее проверенное поведение сохранено.

Проверки рефакторинга: регрессии writer/undo, transition policy и независимого runner прошли в полном uncached Go race/coverage прогоне. Полный локальный gate **10/10 PASS**, golangci-lint **0 issues**, production build и cross-platform checks прошли; итоговый лог — `.tmp/s3-refactor/check.log`. Серверные live-тесты ниже относятся к реализации до этого локального рефакторинга; повторных действий на сервере в рамках этой задачи нет.

## Уточнение подключения S3 — 2026-10-07

По решению владельца используется встроенное `content/storage@content-storage-s3`
с bucket `ecos-content`. Endpoint и ключи передаются в ecos-content через штатные
`ECOS_ENDPOINT_*` и `ECOS_SECRET_*` env, как в Helm ECOS. Удалены создание трёх
дополнительных записей, синхронизация ключей/endpoint и состояние `contentStorageSecretWrite`.
При смене ключа генерация обновляет окружение обоих контейнеров; их hash меняется.
DB-генерация остаётся прежней. Bucket в workspace необязателен; другое имя отклоняется.

Единственная мутация — config хранилища по умолчанию. Значение перечитывается
непосредственно перед записью и после неё; новый выбор другого хранилища
сохраняется. Условная запись явно отклонена владельцем. Поэтому внешнее изменение
между последним чтением и записью не исключается атомарно. Неподтверждённая
мутация не объявляется успешной. WAL обязателен до записи; подтверждение и
история переключения сохраняются вместе. Epoch и data barrier сохранены.

Ошибки повторяются автоматически по таймеру через пять минут. Таймер переживает
занятый snapshot и остановленную платформу, отменяется при Reset/shutdown;
устаревший callback не затрагивает новый таймер или namespace. Ручной повтор
лишь ускоряет попытку. Старые выводы о синхронизации ECOS secret к новой реализации
не относятся; общий долг согласования config writers сохраняется.

Новые регрессии проверяют единственную мутацию, env, hash при смене ключа,
ручной выбор перед записью, несовпадение после неё, отсутствие unresolved secret,
повтор без событий, повтор при занятой платформе и отмену таймера. Проверены
скриншоты Chromium (EN/RU), остановка провайдера и S3 edit; API изолированным mock.
Живой ECOS-стек в этой проверке не запускался.


Итог проверки новой реализации: полный локальный gate **10/10 PASS**,
`0 issues` у golangci-lint; все Go-тесты выполнены без кэша с race/coverage,
namespace 84,9%, daemon 67,5%, все пороги соблюдены. Vitest: 41 файл / 445 тестов;
audit без известных уязвимостей. Сборка сервера, linux/arm64, windows/amd64 vet
и integration-tag vet прошли. Окончательный лог:
`.tmp/s3-default/check.log` в корне solution. Использован прежний проверенный
harness для длинного пути solution: короткий TMPDIR/GOTMPDIR указывает в solution,
два export-теста исполняются отдельной группой с каноническим путём; тесты не пропущены.
`git diff --check` чист. Скриншоты: `.tmp/s3-default/*.png`; Vite после проверки
остановлен. Коммитов, push и публикаций нет.


## Живой прогон — 2026-10-07

Последующая проверка на тестовом сервере завершена: данные через ECOS и
физический S3, ошибки/автоповторы, перезапуски, смена S3-ключа, admin-пароля,
BASIC → Keycloak и ручного default проверены.
Подробности и ограничения конфигурации: [живой прогон](content-storage-s3-live.md).

## Формат параметров генерации секретов — 2026-10-07

По решению владельца основной YAML-формат унифицирован с константами:

```yaml
secrets:
  - id: observer-db
    value: observer
  - id: seaweedfs-s3-key
    generated:
      length: 40
  - id: seaweedfs-jwt
    generated:
      length: 48
```

`id` задаёт имя для обоих видов; `generated` — mapping параметров.
`generated: {}` использует прежнюю длину по умолчанию 40; границы 16..128
сохранены. Прежний scalar-формат остаётся читаемым, YAML-вывод использует
основной формат. Существующие секреты namespace не меняются и не генерируются
заново из-за изменения записи. Ошибочная запись отбрасывается отдельно от
workspace; конфликты `value`/`generated`, неверный id, длина и форма параметров
покрыты тестами. Fixture и операторские примеры переведены на основной формат.

Совместимость со старым Launcher описана заново: без поддержки генерации он
видит новый `id` как константу с пустым значением, поэтому такой workspace
требует версии с поддержкой генерируемых секретов. Прежнее утверждение о
пропуске записи справедливо только для legacy scalar-формата.

Локальная проверка: **10/10 PASS**, uncached Go race/coverage, golangci-lint **0 issues**, 445 web-тестов и production/cross-platform build checks; лог — `.tmp/secrets-format/check.log`. Серверная конфигурация
и завершённые live-тесты при этой правке не изменялись.

## Managed SeaweedFS и desktop local-content — 2026-10-07

Выбранный `type: SEAWEEDFS` включает управление pin и поколениями томов в
общем механизме dependencies, без отдельного dependency-блока. Обновление
выполняется на копии с проверкой metadata/данных; исходный том сохраняется
для штатного отката. Поддерживаются настроенные имена хранилищ.

Desktop публикует S3 на localhost:8333 (явное переназначение сохраняется),
server backend-порт остаётся закрытым. Для content вне Docker добавлен явный
`citeck describe content --local-env`: native environment для IDE использует
фактический опубликованный адрес; обычный describe сохраняет маскирование.

Финальный gate: **10/10 PASS**, **0 lint issues**, uncached Go race/coverage,
**41 файл / 446 Vitest-тестов**, build/cross-platform/integration checks.
Настоящий Docker подтвердил 4.47 → 4.48, SHA256/readback бинарного и UTF-8
объектов, чтение старой версии, неизменность исходного тома и откат через API.
Отдельный JVM на хосте подтвердил ECOS Env providers + MinIO через localhost;
полный content microservice/platform этим probe не запускался. UI проверен
в Chromium с mock API и скриншотами. Все три локальных probe-контейнера удалены,
порт 8333 освобождён, доказательства сохранены. Исходники не развёртывались;
коммитов и push нет. Подробности и пути логов: [SeaweedFS](../seaweedfs-upgrade.md).

После дополнительного запроса владельца выполнен тестовый deployment и
прогон на сервере, включая настоящий TUI. Исправлено обрезание длинного
пояснения Select; повторный gate **10/10 PASS**. Итоговая сборка
`dev-20261007-120301`, данные проверены (18 ECOS/15 физических S3-файлов),
стенд RUNNING/S3 connected/KEYCLOAK. Инцидент с изоляцией дополнительного
server daemon, восстановление и все доказательства описаны в
[дополнительном live-прогоне](content-storage-s3-live.md#дополнительный-seaweedfstui-прогон).

## Desktop: настройка S3 в Advanced — 2026-10-07

По решению владельца редко используемый выбор способа хранения content
перенесён с General («Основное») в Advanced («Дополнительно») диалога
создания и редактирования namespace. Вкладка появляется при наличии этой
настройки или legacy MongoDB. На General можно создать namespace с default DB;
намеренно выбранный S3 сохраняется при смене вкладок и отправляется при
сохранении. Существующее S3-хранилище показано в Advanced только для чтения.
Правила доступности, необратимости и проверки смены bundle сохранены.

Проверка: 14 тестов диалога, общий gate **10/10 PASS**, **0 lint issues**,
**41 файл / 446 web-тестов**. В настоящем Chromium на текущем UI с mock API
пройдены все 8 локалей: General без S3, Advanced с выбором, сохранение выбора
после возврата в General и отправка `S3`, read-only существующего хранилища.
Скриншоты General/Advanced/read-only сохранены и русские экраны просмотрены.
Доказательства в `.tmp/s3-advanced` корня solution: `focused.log`, `check.log`,
`ui.log`, `<locale>-general/advanced/readonly.png`. Preview server остановлен;
`make build-desktop` прошёл, обновлён `dist/bin/citeck-launcher` с новым
embedded UI (лог `desktop-build.log`). Изменения остаются незакоммиченными.

## SEAWEEDFS: дефолтный контейнер и YAML settings — 2026-10-08

По решению владельца Launcher владеет data volume/generation и стандартной
командой `mini`, а также предоставляет default startup/liveness probes,
resources/stopTimeout и desktop ports. `volumeBase` необязателен и по умолчанию
равен имени. `settings` — YAML-мапа скаляров (числа/boolean/строки), из которой
команда строится в порядке ключей, отдельными argv без shell. `cmd`/`volumes`
для SEAWEEDFS отклоняются; фиксированные dir/ip/service ports нельзя менять
через settings. Произвольные контейнеры сохраняют прежние поля.

Базовый профиль совместим с 4.47: новые флаги 4.48 остаются явным tuning.
Admin UI по умолчанию выключен; `admin.ui: true` включает его и default
desktop port 23646, S3 port 8333 публикуется по умолчанию. Server и временные
migration containers ничего не публикуют. Явные overrides probes/resources,
env, stopTimeout и host mappings сохраняются. Генерация не меняет env/settings
workspace. Закрыта коллизия typed SeaweedFS с самим собой при образе из
`applications` бандла: store не становится JVM webapp.

Reference fixture и `launcher-public-workspace/local-s3-test/workspace-v1.yml`
сокращены до type/name, optional settings и access/bucket env. Общий
`contentStorage.s3` сохранён как стандартный namespace-internal провайдер.
Проверка реального public workspace production parser прошла без отброшенных
частей; настройки провайдера совпадают с reference fixture.

На текущем коде реальная локальная Docker-проверка 4.47 → 4.48 + API rollback
прошла (34,54 s), включая binary/UTF-8/versioned readback и неизменность томов.
Bootstrap PUT bucket учитывает только точный `BucketAlreadyOwnedByYou`, если
`S3_BUCKET` уже создал его при запуске; другие ошибки не принимаются.
Отдельный host JVM с настоящими ECOS Env providers/MinIO прочитал и записал
объект через localhost:8333 контейнера, созданного из компактной декларации;
SHA256 совпал, default HTTP probes вернули 200. Это не boot всей платформы.
Доказательства: `.tmp/seaweed-defaults/focused.log`, `focused-extra.log`,
`public-workspace.log`, `integration.log`, `desktop-native.log` корня solution.
Disposable контейнер и его данные удалены, порт освобождён; remote deployment
этой фазы не выполнялся. Все изменения остаются незакоммиченными.

Окончательный gate на этом коде: **10/10 PASS**, **0 lint issues**, Go tests
без кэша с race/coverage, **41 файл / 446 web-тестов**, production build,
linux/arm64 build и Windows/integration vet. Лог:
`.tmp/seaweed-defaults/check.log`. Предыдущие промежуточные логи не являются
финальным результатом; bootstrap bucket race исправлена в тестовом harness.
`make build-desktop` также прошёл (`desktop-build.log` в том же каталоге).

### Внешний S3: активная доработка после live-проверки (2026-10-08)

Владелец запросил завершить внешний режим: default storage, bucket и region.
Проверка bytecode установленного ECOS content 1.7.0 показала: S3ConfigurationDto
содержит только endpoint/bucketName; оба MinIO client builder не задают region.
Явный region требует изменения ECOS content; решение о включении этого проекта
в solution запрошено у владельца. До решения продолжается независимая работа
над подключением внешнего S3 в Launcher. Текущий native-only/ONLY default-config
Records mutation контракт сохраняется; provider-derived записи не создаются.

Независимая часть доработки (ещё не FINAL): scheduler/retry/status теперь
учитывают `namespace.s3`, без зависимости от managed app и без наследования
имени workspace provider; выбор администратора сохраняется. Bucket применяется
через сгенерированный read-only mount встроенного `content-storage-s3.yml`
(тот же native id и endpoint); credentials остаются только в env providers.
Изменение bucket меняет volume-content hash и пересоздаёт content. Дополнительные
Records mutations не добавлены: по-прежнему только default-config. Focused tests
для external без managed provider, admin default, platform readiness, artifact,
секретов и bucket/hash — PASS. Live-проверка через настоящий TUI подтвердила
автоматический LOCAL → native S3 и нестандартный bucket при остановленном
managed backend. После copyback/restore/cleanup — 23 ECOS-файла и 20 физических
S3-файлов с совпадением хэшей. Добавлена защита от удаления external S3 с
возвратом в БД (API/offline/undo/TUI); отказ Remove и S3 label проверены в
реальном TUI с изолированным config и без второго daemon. Подробности:
`docs/reviews/content-storage-s3-live.md`, `.tmp/external-completion/`.
Открыта только часть про явный region: ожидается решение владельца.

Владелец 2026-10-08 подтвердил доработку ECOS content, потребовал non-nullable
`region: String = ""`, затем поручил hotfix по `citeck-release` и новую задачу EPT.
ECOS content добавлен в solution (чистый Release1.7.0); регион передаётся обоим
клиентам, blank/trim сохраняют auto-detection. `mvn clean verify` — 50/50 PASS;
Launcher region artifact/hash tests и 10/10 gate PASS. Release preflight0stops,
master/origin9f1499f1.7.0, develop1.8.0-SNAPSHOT, предполагаемый hotfix1.7.1.
Подготовленный ECOS diff сохранён в stash и solution scratch до синхронизации
веток; изменения Launcher/public остаются незакоммиченными. EPT MCP сообщил
Session expired; создание новой задачи/ID пока блокирует code commit релиза.
Проверяется безопасный PKCE вход с отдельным состоянием внутри solution.
