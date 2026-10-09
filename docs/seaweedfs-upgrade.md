# Управляемые обновления SeaweedFS

SeaweedFS объявляется типом зависимости workspace, аналогично POSTGRES и
QDRANT. Для стандартного контейнера достаточно имени и типа; образ задаёт бандл:

```yaml
additionalApps:
  - name: seaweedfs
    type: SEAWEEDFS
    settings:                     # необязательные параметры weed mini
      admin.ui: true
      s3.readerCacheSizeMB: 64
      s3.concurrentUploadLimitMB: 128
    environments:                 # доступ и bucket — настройки workspace
      AWS_ACCESS_KEY_ID: ${secret:s3-access-key}
      AWS_SECRET_ACCESS_KEY: ${secret:s3-secret-key}
      S3_BUCKET: ecos-content
```

`settings` — YAML-мапа скалярных значений: числа, boolean и строки. Launcher
собирает отсортированную команду `mini -key=value`; shell не используется.
Настройки задаются без начального дефиса. Сложенные объекты и массивы в settings
отклоняются. `cmd` и `volumes` для SEAWEEDFS не принимаются: команда и том
принадлежат Launcher. Произвольные контейнеры продолжают использовать прежние поля.

Дефолты типизированного контейнера:

| Параметр | Дефолт |
|---|---|
| Данные / адрес узла | `/data`, `127.0.0.1`; через settings не переопределяются |
| Том | `<имя>2:/data` или текущее поколение из pin; `volumeBase` — необязательный stem |
| Startup | S3 `/healthz:8333`, period 5 s, threshold 60, timeout 5 s |
| Liveness | S3 `/healthz:8333`, threshold 7, timeout 5 s |
| Resources / stop | память 1g, `GOMEMLIMIT=512MiB`, stopTimeout 15 s |
| Desktop порты | S3 localhost:8333; admin localhost:23646 только при `admin.ui: true` |
| Mini | telemetry false, volumeSizeLimitMB 256, disableHttp true, webdav false, admin.ui false, iceberg/lance ports 0 |

Явные probes/resources/stopTimeout/environment и desktop host mappings остаются
необязательными переопределениями. Env и settings не мутируются при генерации.
Admin UI по умолчанию отключён, включается через `settings.admin.ui`. Настройки
доступа/JWT и bucket задаются явно и могут ссылаться на namespace secrets.
Базовые флаги подходят также для миграционного источника 4.47; настройки,
появившиеся в 4.48 (`s3.readerCacheSizeMB` и другие), являются явным tuning
workspace и должны подходить версии, удержанной pin. Во время миграции порты
временных контейнеров не публикуются, сервер backend-порты не публикует.

Сам `mini` сохраняет флаги в `/data/mini.options`. Его сохранённое значение
может переопределить argv, равный vendor default: возврат такой настройки к
дефолту требует удаления именно её сохранённой записи перед restart. Launcher
не удаляет mini.options автоматически. Таблица выше описывает генерируемый
профиль и новый том, а не переписывание внутренней конфигурации старого store.
Credentials передаются через env и не сохраняются туда как CLI flags.

Имя настраиваемое. Образ называет `dependencies.<name>` бандла; `image` записи
workspace остаётся fallback по общим правилам. `volumeBase` по умолчанию равен
имени. Первая генерация сохраняет существующий том `<volumeBase>2`; миграция
переводит namespace на `<volumeBase>3`, затем на последующие поколения.
Смена версии, включая patch и downgrade, удерживается pin на существующих
данных. Свежий namespace получает версию бандла без миграции. Pin засеивается
из существующего контейнера/тома, фиксируется после успешного запуска и
сохраняется в обычном состоянии namespace.

Обновление запускается через существующий механизм:

```bash
citeck deps
citeck deps upgrade seaweedfs
citeck deps rollback seaweedfs
```

UI показывает ту же зависимость и её копирующий план. План останавливает
namespace, проверяет место для копии, скачивает образ, копирует данные с
read-only исходного тома, запускает старую версию на копии и фиксирует inventory,
затем запускает новую версию на этой же копии. Перед commit проверяются ссылки
на chunks (`fs.verify`, без prune) и полные метаданные `/buckets`: имена,
каталоги, версии объектов, inline-данные, chunk refs, криптографические параметры
и настройки bucket. Protobuf-поля нормализуются по порядку; исключены только
время последнего доступа и его наносекунды. Размер metadata export ограничен
64 MiB, неполный экспорт или отсутствие подтверждения CLI отклоняются.

Commit атомарно меняет pin и поколение, сохраняет исходный том и предложение
отката. Ошибка до commit включает общий journalled rollback: временные
контейнеры/созданный том убираются, исходные данные не меняются. Прерывание
процесса восстанавливается общим механизмом migration journal. Штатный откат
переключает pin обратно на сохранённый исходный том и оставляет новое поколение
на диске; записи, сделанные после обновления, в старом томе отсутствуют.

## Область поддержки

Поддерживается один локальный `weed mini` с единственным томом `/data`,
фиксированными внутренними портами и `-ip=127.0.0.1`. Дополнительные data mounts,
init actions/containers, альтернативные dir/ip и переназначение service ports
отклоняются. Не меняйте имя тома в workspace: поколением владеет Launcher.
Используйте стандартные numeric tags (`4.48`, `4.48.1`), а не `latest`, digest,
`large_disk` или enterprise-варианты. Forward-обновления внутри 4.x разрешены;
переход на другой major и downgrade через upgrade не предлагаются. Нижняя
граница ручного выбора образа — 4.48; она не запрещает миграцию существующих
данных 4.47. Filer backend должен оставаться локальным в `/data`; внешние
хранилища metadata и распределённый cluster этой схемой не покрываются.

В [FAQ SeaweedFS](https://github.com/seaweedfs/seaweedfs/wiki/FAQ#upgrade)
указана forward-совместимость при отсутствии специальных замечаний; CLI может
меняться. Сохранённый исходный том и проверка копии защищают данные от неудачной
попытки, а не утверждают совместимость любого будущего релиза. Флаги workspace
должны подходить и удержанной старой версии. Старый Launcher, не понимающий
SEAWEEDFS, не знает её pin/generation: после миграции не переключайтесь на такой
бинарь; откат выполняется командой deps в поддерживающей версии.

## Desktop и проверка

Desktop публикует S3 на localhost:8333, даже если порт не перечислен в записи;
явная host-port mapping сохраняется. Server ничего для SeaweedFS не публикует.
Для запуска content из IDE используйте [экспорт native environment](content-storage.md#обновление-seaweedfs-и-локальный-запуск-content).

Локальная интеграция на настоящем Docker проверяет 4.47 → 4.48, SHA256 бинарного
1 MiB и UTF-8 объектов, чтение старой версии S3-объекта, неизменность исходного
тома и штатный rollback с сохранением нового тома. Отдельный JVM-процесс на
хосте использует настоящие ECOS Env providers и MinIO client из content image
для записи/чтения через localhost; SHA256 совпадает. Логи:
`.tmp/seaweed-upgrade/integration.log`, `desktop-native.log`, полный gate —
`check.log`; UI screenshot — `dependencies-confirm.png`.

Итог на 2026-10-07: полный локальный gate **10/10 PASS**, golangci-lint —
**0 issues**, Go race/coverage без кэша, Vitest — **41 файл / 446 тестов**,
production build и cross-platform/integration проверки прошли. Настоящий
Docker-прогон 4.47 → 4.48 и откат через API завершились успешно; 4.49 в живом
прогоне не использовалась. JVM-доказательство проверяет отдельный host-процесс
с ECOS Env providers и MinIO, а не запуск всего content microservice/platform.
UI проверен в Chromium с mock API; скриншот подтверждения просмотрен.

После проверки удалены только локальные disposable-контейнеры
`citeck-seaweed-upgrade-probe`, `citeck-seaweed-desktop-probe` и
`citeck-content-inspect`; localhost:8333 освобождён. Логи, скрипты, JAR и
скриншоты сохранены в `.tmp/seaweed-upgrade` корня solution. Изменения остаются
незакоммиченными на `feature/content-storage-s3`, HEAD `34fe309`;
push, публикация и deployment не выполнялись.

## Дополнительный тестовый сервер и TUI

После локальной фазы владелец разрешил изменения на тестовом сервере
45.15.158.227 и попросил проверить TUI. Итоговая сборка
`dev-20261007-120301` установлена в `/usr/local/bin/citeck` с сохранением
прежнего бинарника в solution scratch сервера. SHA256 установленного и
локального бинарников совпадает:
`b5cf530c6219891690dc1ebc563e96267bab9738676576c8fadb3efcd671812e`.
Это тестовый deployment по дополнительному разрешению, не публикация релиза.

Baseline подтвердил 17 прежних файлов через ECOS и 14 физических S3-файлов.
Реальный Docker integration test 4.47 → 4.48 + rollback прошёл на сервере
(50,84 s): binary/UTF-8/versioned readback, исходный том не изменился, rollback
выполнен штатным API. Основной store остаётся на 4.48; его workspace получил
`type: SEAWEEDFS`/`volumeBase`, и dependency/pin виден после service restart.
В основной namespace добавлен ещё один бинарный файл 1 MiB через ECOS:
итог — 18 читаемых файлов, 15 физических S3-файлов, SHA256 совпадают.

Во время подготовки TUI отдельный server daemon с другим
`CITECK_HOME`/`CITECK_RUN` всё равно использовал Docker namespace `default`:
server namespace не изолируется через `namespace.yml id`. Это привело к
остановке/замене контейнеров основного стенда; исходные тома не удалялись.
Дополнительный daemon остановлен, основной systemd сервис перезапущен.
Все прежние файлы повторно проверены после восстановления. Итог: systemd
active, namespace RUNNING (13 приложений), S3 connected, исходный KEYCLOAK,
том `/opt/citeck/data/runtime/default/volumes/seaweedfs2`, backend не опубликован.
Для параллельных server-daemon тестов нужен отдельный Docker engine; текущий
integration harness безопаснее, поскольку явно задаёт уникальный Docker nsID.

TUI проверен через настоящий tmux: основной setup menu, запрет S3 → DB,
выбор DB/S3 на disposable конфигурации, diff и его отмена, отмена смены языка,
Back/Escape, пустой port input с сохранением default и ошибка диапазона.
Сохранённые настройки после отмены не меняются. Найдено обрезание длинного
пояснения Select по ширине терминала; исправлено с обработкой resize и
регрессионным тестом (40/80/120 cells, UTF-8, без потери текста). Реальный
экран при 80/120 колонках и screenshot проверены: предупреждение о
необратимости полностью видно. `deps` показывает SeaweedFS 4.48;
upgrade без новой версии и rollback без предыдущей корректно отказывают.
Server `describe content --local-env` отказывает без выдачи credentials.
Полный gate после исправления TUI — **10/10 PASS**, **0 lint issues**,
uncached Go race/coverage, **41 файл / 446 web-тестов**, build/cross-platform.

Доказательства в `.tmp/seaweed-live` корня solution: `check.log`,
`integration.log`, `final-deploy.log`, `final-state.log`, `final-read.log`,
`final-s3.log`, `tui.log`, `tui-*.txt/html/png`. TUI content-selection proof
использует disposable DB-конфигурацию и только просмотр/отмену, не повторное
переключение основного namespace из DB. Коммитов и push нет.
Собственные tmux TUI/daemon-сессии завершены, integration containers/scratch
и disposable тома/сгенерированные секреты TUI home удалены. Основной systemd
сервис намеренно оставлен активным; backup и доказательства сохранены.

### TUI на всех восьми языках

По дополнительному запросу владельца на той же установленной сборке
`dev-20261007-120301` выполнен настоящий tmux-прогон для `en`, `ru`, `zh`,
`es`, `de`, `fr`, `pt`, `ja`: **8/8 PASS**. Для каждой локали проверены setup
menu, полный S3 hint при 80/120 колонках и resize, DB/S3 labels, diff и отмена,
Back/Escape, пустой port input с default, ошибочный формат и диапазон порта,
список языков, SeaweedFS в deps и локализованные upgrade/rollback refusals,
запрет S3 → DB. Настройки после отмены побайтово неизменны; ни один проверенный
экран не показывает необработанные ключи переводов. Новых дефектов не найдено.

Сохранены 48 PNG (6 экранов на локаль), исходные ANSI/HTML и plain-text
captures. Экран выбора content просмотрен для всех локалей, включая CJK;
границы всех 48 screenshot layouts проверены автоматически. Проверки
`go test -count=1 ./internal/i18n ./internal/cli/prompt` также прошли.
Исходники и установленный бинарник при этом прогоне не менялись; полный gate
предыдущей сборки остаётся **10/10 PASS**.

Доказательства: `.tmp/tui-locales/verify.log`, `visual-check.log`,
`final-state.log`, `<locale>/*.txt/html/png`, `verify.py`, `screenshots.mjs`
в корне solution. Все восемь disposable config homes и tmux-сессия удалены.
Основные namespace.yml/daemon.yml побайтово не изменились; штатный сервис
active, основной namespace RUNNING/S3 connected.

Для повторения Docker-теста соберите `tests/seaweed-client` (CGO_ENABLED=0),
задайте путь в `CITECK_SEAWEED_TEST_CLIENT`, затем запустите integration-tag test
`TestIntegration_SeaweedCopyUpgradeAndRollback`. Под rootless Docker нужен
`unshare --user --map-auto --map-root-user`, как для остальных dependency tests.
TMPDIR/GOTMPDIR должны указывать в scratch текущей рабочей копии. JVM probe —
`tests/seaweed-client/NativeEnvProbe.java`, classpath из content image.
