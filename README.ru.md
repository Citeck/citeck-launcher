![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English version](README.md)

Citeck Launcher управляет пространствами имён Citeck и Docker-контейнерами. Один Go-бинарник (~18 МБ) выступает и как CLI, и как демон со встроенным React Web UI.

## Быстрый старт

```bash
curl -fsSL https://get.citeck.com | sh
```

Скачивает лончер, запускает мастер установки и стартует платформу. Web UI откроется на `http://127.0.0.1:7088`.

### Ручная установка

Требования: Docker (запущен).

```bash
# Скачать с GitHub Releases
curl -fsSL -o citeck https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck_linux_amd64
chmod +x citeck && sudo mv citeck /usr/local/bin/

# Установить и запустить
citeck install && citeck start
```

### Офлайн-установка

```bash
# Скачайте и бинарник, и workspace ZIP, затем:
citeck install --workspace /path/to/workspace.zip
```

Флаг `--workspace` распаковывает репозитории бандлов локально — интернет при запуске не нужен.

### Запуск

```bash
# На переднем плане (для отладки)
citeck start --foreground

# Как systemd-сервис (если настроено через мастер)
sudo systemctl start citeck
```

Web UI доступен по адресу `http://127.0.0.1:7088` (по умолчанию).

## Возможности

- **Web UI в стиле Lens** с выдвижной панелью, нижней панелью с вкладками (логи, конфиг) и изменяемым размером
- **8 языков интерфейса**: английский, русский, китайский, испанский, немецкий, французский, португальский, японский
- **Обновления в реальном времени** через SSE (статус приложений, потребление ресурсов)
- **Полный каталог сервисов** виден даже когда пространство имён остановлено
- **Просмотрщик логов** с виртуальным скроллом (50K строк), regex-поиском, фильтрацией по уровню, стримингом
- **Снапшоты томов** с экспортом/импортом (ZIP + tar.xz), авто-остановка/запуск
- **mTLS** для безопасного удалённого доступа к Web UI (клиентские сертификаты, .p12 для браузера)
- **Let's Encrypt** с авто-обновлением
- **Автодополнение** для bash, zsh, fish, powershell

## Команды CLI

```
citeck install [--workspace <zip>]      Мастер установки (офлайн с --workspace)
citeck start [app] [-d]                 Запуск демона/пространства (-d = в фоне)
citeck stop [app] [-d]                  Остановка (-d = в фоне)
citeck restart [app]                    Перезапуск приложения или всего пространства
citeck status [--watch]                 Статус пространства имён
citeck health                           Проверка здоровья (exit 0=ok, 1=проблема)
citeck reload                           Перечитать конфиг и пересоздать контейнеры
citeck upgrade [ref] [--list|--dry-run] Обновить версию бандла
citeck logs [app] [--follow]            Логи (демона или контейнера)
citeck exec <app> -- <команда>          Выполнить команду в контейнере
citeck apply <файл> [--dry-run]         Применить конфигурацию
citeck diff -f <файл>                   Показать отличия от текущего конфига
citeck snapshot list|export|import      Снапшоты томов (авто-остановка/запуск)
citeck clean [--images] [--execute]     Очистка сирот / удаление образов
citeck webui cert|list|revoke           Сертификаты доступа к Web UI (.p12)
citeck cert generate|status|letsencrypt Серверные TLS-сертификаты
citeck self-update [--check|--file]     Обновить бинарник лончера (с откатом)
citeck workspace import|update          Импорт/обновление workspace
citeck diagnose                         Диагностика
citeck validate                         Проверка конфигурации
citeck completion bash|zsh|fish         Автодополнение
citeck uninstall                        Удаление
```

Глобальные флаги: `--host`, `--tls-cert`, `--tls-key`, `--server-cert`, `--insecure`, `--format json`.

## Архитектура

### Go-демон (`internal/`)

| Пакет | Назначение |
|---|---|
| `cli/` | Команды CLI (Cobra) |
| `daemon/` | HTTP-сервер, API, SSE-события, middleware |
| `namespace/` | Парсинг конфигов, генерация контейнеров, runtime state machine, reconciler |
| `docker/` | Обёртка Docker SDK (контейнеры, образы, exec, логи, пробы) |
| `bundle/` | Определения бандлов и их разрешение из git-репозиториев |
| `git/` | Git clone/pull через go-git (чистый Go) |
| `config/` | Пути файловой системы, конфиг демона |
| `storage/` | Интерфейс Store + FileStore (сервер) + SQLiteStore (десктоп) |
| `snapshot/` | Экспорт/импорт снапшотов томов |
| `tlsutil/` | Утилиты TLS (самоподписанные, клиентские сертификаты, CA pool) |
| `acme/` | ACME/Let's Encrypt клиент + авто-обновление |
| `client/` | DaemonClient (Unix socket + mTLS TCP) |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Встраивается в Go-бинарник через `go:embed`.

### Сборка из исходников

```bash
make build          # Полная сборка (Go + React)
make test           # Go + web тесты
make test-race      # Go с детектором гонок
make lint           # Go (golangci-lint) + web (eslint)
```

Требуется: Go 1.22+, Node.js 20+, pnpm.

## Безопасность

- **Unix socket**: полный доступ (только CLI)
- **mTLS TCP** (не-localhost): полный доступ, аутентификация клиентским сертификатом
- **Localhost TCP**: ограниченные маршруты + CSRF-заголовок (`X-Citeck-CSRF`)

## Документация

- [Справочник API](docs/api.md) — все REST-эндпоинты
- [Справочник конфигурации](docs/config.md) — daemon.yml, namespace.yml, флаги CLI, файловая структура
- [Руководство оператора](docs/operations.md) — логи, обновление, бэкапы, mTLS, отладка, мониторинг

## Лицензия

См. [LICENSE](LICENSE).
