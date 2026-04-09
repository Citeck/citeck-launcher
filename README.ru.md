![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English version](README.md)

Citeck Launcher управляет пространствами имён Citeck и Docker-контейнерами. Один Go-бинарник (~14 МБ) выступает и как CLI, и как демон.

## Быстрый старт

Требования: Docker (запущен).

```bash
curl -fsSL https://raw.githubusercontent.com/Citeck/citeck-launcher/release/2.1.0/install.sh | bash
```

Скрипт скачивает последний релиз для вашей платформы и устанавливает в `/usr/local/bin/`. Мастер установки настроит пространство имён и запустит платформу.

Для **обновления** уже установленной версии выполните тот же one-liner — скрипт определит установленную версию, спросит подтверждение, остановит демон и заменит бинарник (бэкап сохраняется в `/usr/local/bin/citeck.bak` и восстанавливается через `bash install.sh --rollback`).

### Офлайн-установка

```bash
# Скачайте и бинарник, и workspace ZIP, затем:
citeck install --workspace /path/to/workspace.zip
```

Флаг `--workspace` распаковывает репозитории бандлов локально — интернет при запуске не нужен.

## Возможности

- **Интерактивный установщик** с авто-определением TLS (Let's Encrypt / самоподписанный / свой сертификат)
- **Обновление бинарника без простоя** — `install.sh` заменяет бинарь демона, не останавливая контейнеры платформы; новый демон подхватывает их по deployment-hash (k8s-style restart control plane)
- **8 языков интерфейса**: английский, русский, китайский, испанский, немецкий, французский, португальский, японский
- **Обновления в реальном времени** через SSE (статус приложений, потребление ресурсов)
- **Снапшоты томов** с экспортом/импортом (ZIP + tar.xz), авто-остановка/запуск
- **Let's Encrypt** с авто-обновлением (домены и IP-адреса)
- **Самовосстановление** с liveness-пробами, отслеживанием перезапусков и диагностикой
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
citeck cert generate|status|letsencrypt Серверные TLS-сертификаты

citeck workspace import|update          Импорт/обновление workspace
citeck diagnose                         Диагностика
citeck validate                         Проверка конфигурации
citeck completion bash|zsh|fish         Автодополнение
citeck setup [настройка]                Настройка (TUI-меню или по ID)
citeck setup history                    История изменений конфигурации
citeck setup rollback [id]              Откат изменения
citeck uninstall                        Удаление
```

Глобальные флаги: `--host`, `--tls-cert`, `--tls-key`, `--server-cert`, `--insecure`, `--format json`.

## Конфигурация

### daemon.yml

Настройки демона. Расположен в `$CITECK_HOME/conf/daemon.yml`.

```yaml
locale: ru                      # Язык: en, ru, zh, es, de, fr, pt, ja
server:
  listen: ":7088"
reconciler:
  interval: 30
docker:
  pullConcurrency: 4
```

### namespace.yml

Описание пространства имён. Расположен в `$CITECK_HOME/conf/namespace.yml`.

```yaml
id: default
name: Citeck
bundleRef: "community:2026.1"
authentication:
  type: KEYCLOAK
  users: ["admin"]
proxy:
  host: example.com
  port: 443
  tls:
    enabled: true
    letsEncrypt: true
```

## Лицензия

См. [LICENSE](LICENSE).
