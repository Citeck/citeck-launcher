![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · **Русский** · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/ru/latest/index.html)

**Установите и запустите полную платформу Citeck — как десктопное приложение на своём компьютере или одной командой на сервере.**

Citeck Launcher — это официальный установщик и менеджер контейнеров для low-code BPM/ECM-платформы **Citeck**. Единый бинарник размером ~24 МБ работает как инструмент командной строки, фоновый демон и кроссплатформенное десктопное приложение, запуская каждый сервис Citeck (Keycloak, PostgreSQL, RabbitMQ и веб-приложения Citeck) как Docker-контейнер и группируя их в изолированные неймспейсы. Какие приложения и версии запускать, задаёт **bundle** (например, релиз Community или Enterprise).

[Citeck](https://github.com/Citeck) — это open-source платформа для создания бизнес-приложений, объединяющая подходы **no-code, low-code и pro-code** для управления контентом и процессами. На практике её используют, чтобы **управлять документами и записями (ECM), автоматизировать бизнес-процессы и маршруты согласования с помощью встроенного дизайнера BPMN, а также создавать внутренние приложения — порталы, CRM, управление обращениями — практически без кода или вообще без него**, со встроенными учётными записями пользователей, ролями и правами доступа. Это self-hosted альтернатива проприетарным ECM/BPM-системам, подходящая для всех — от бизнес-аналитиков до разработчиков.

Редакция **Community** полностью открытая и бесплатная — включает основной функционал платформы и спроектирована максимально дружелюбно к расширениям любого вида. Для более требовательных сценариев коммерческая редакция **Enterprise** добавляет профессиональную поддержку и дополнительный enterprise-функционал. Лаунчер устанавливает любую из редакций. По любым вопросам или за консультацией — [свяжитесь с командой Citeck](https://www.citeck.ru/contacts/).

## Десктоп или сервер?

Есть два способа запуска — выберите тот, что соответствует тому, **где** вы хотите запустить Citeck:

| | 🖥 **Десктопное приложение** | 🖧 **Сервер (CLI)** |
|---|---|---|
| Для кого | Ваш собственный компьютер | Linux-сервер / ВМ (обычно по SSH) |
| Установка | Скачать установщик, пройти мастер | Одна команда `curl … \| bash` |
| Интерфейс | Нативное окно приложения (GUI) | Терминал — CLI `citeck` + мастер настройки (TUI) |
| Начать здесь | [Десктопное приложение](#десктопное-приложение) | [Установка на сервер](#установка-на-сервер) |

> **Обратите внимание:** быстрый старт `curl … | bash` и CLI `citeck` в этом README предназначены для **установок на сервер**. На своём компьютере запускайте Citeck через **Десктопное приложение** — там всё делается из интерфейса.

**Требования:** Docker; около **16 GB** RAM для редакции Community (**24 GB** для ~24 сервисов Enterprise); и десятки ГБ диска под образы.

## Десктопное приложение

**Десктопное приложение** запускает Citeck на вашем компьютере с Windows, macOS или Linux — обычное окно приложения, без командной строки. Citeck продолжает работать в фоне даже после закрытия окна.

Установщики для десктопа прикладываются к каждому [релизу на GitHub](https://github.com/Citeck/citeck-launcher/releases) — скачайте файл для своей платформы:

| ОС | Файл | Архитектуры |
|----|------|-------------|
| Windows | `citeck-desktop_<версия>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<версия>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<версия>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

К каждому установщику прилагается файл `.sha256` для проверки. Ваши данные сохраняются при обновлениях.

## Установка на сервер

> **Для Linux-сервера или ВМ** — выполняйте эти шаги на сервере, по SSH.

Требования: хост с Linux и запущенным Docker.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Скрипт установки скачивает последний релиз для вашей платформы и устанавливает в `/usr/local/bin/`. Затем мастер настроит неймспейс и запустит платформу.

> **Важно:** `citeck install` — это **интерактивный TUI-мастер**, требующий настоящего терминала. В конце мастер **один раз** выводит сгенерированный пароль администратора — обязательно скопируйте и сохраните его, после закрытия экрана его нельзя будет восстановить. Если потеряли, сбросьте через `citeck setup admin-password` (см. [справочник команд](https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server/commands.html)).

Для **обновления** уже установленной серверной версии выполните тот же one-liner — скрипт определит установленную версию, спросит подтверждение, остановит демон и заменит бинарник (бэкап сохраняется в `/usr/local/bin/citeck.bak` и восстанавливается через `citeck install --rollback`).

## Возможности

- **Интерактивный установщик** с авто-определением TLS (Let's Encrypt / самоподписанный / свой сертификат)
- **i18n** с 8 языками: английский, русский, китайский, испанский, немецкий, французский, португальский, японский
- **Обновления в реальном времени** через SSE-события (статус приложений, потребление ресурсов)
- **Снапшоты томов** с экспортом/импортом (ZIP + tar.xz)
- **Let's Encrypt** с авто-обновлением (домены и IP-адреса)
- **Самовосстанавливающийся рантайм** с liveness-пробами, отслеживанием перезапусков и диагностикой перед перезапуском
- **Автодополнение** для bash, zsh, fish, PowerShell

## Использование CLI (серверный режим)

Эти команды управляют установкой в **серверном режиме** через CLI. (В десктопном режиме те же операции доступны из интерфейса приложения.)

```
citeck install [--workspace <zip>]        Interactive setup wizard (offline with --workspace)
citeck start [app] [-d|--detach]          Start daemon/namespace (--detach = don't wait)
citeck stop [app...] [-d|--detach]        Stop namespace or app(s) (--detach = don't wait)
citeck restart [app] [-d|--detach]        Restart an app or the entire namespace (waits by default)
citeck reload [--dry-run] [-d|--detach]   Reload config and regenerate changed containers
citeck status [-w|--watch]                Show namespace status
citeck describe <app>                     Show container details (image, ports, env, volumes)
citeck health                             Health check (exit 0=healthy, 1=daemon down, 8=unhealthy)
citeck diagnose [--fix] [--dry-run]       Run diagnostics (with optional auto-fix)
citeck logs [app] [-f|--follow]           Stream logs (daemon if no app)
citeck exec <app> -- <command>            Execute command in container
citeck update [-f|--file <zip>]           Pull workspace/bundle defs (or import from ZIP)
citeck upgrade [bundle:version] [--yes]   Switch to a different bundle version
citeck snapshot list|export|import|delete Manage volume snapshots (auto stop/start)
citeck config view|validate|edit          Show, check, or edit namespace.yml
citeck setup [setting]                    Configure settings (TUI menu or by ID)
citeck setup history                      Show config change history
citeck clean [--force] [--volumes] [--images]  Clean orphaned resources / prune images
citeck dump-system-info [--full]          Collect diagnostics ZIP (status, logs, docker inspect, journalctl)
citeck version [--short]                  Show version info
citeck completion bash|zsh|fish           Generate shell completion
citeck uninstall [--delete-data]          Remove systemd service, binary, and (optionally) data
```

Глобальные флаги: `--format (text|json)`, `--yes/-y`.

## Документация

- **Серверный режим:** [документация по серверному режиму лаунчера](https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server.html) — установка, конфигурация (`daemon.yml` / `namespace.yml`) и [справочник команд](https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server/commands.html).
- **Десктопное приложение:** [Документация по десктопному режиму](https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher.html).

## Лицензия

Citeck Launcher — open-source проект под лицензией **LGPL-3.0** — см. [LICENSE](../LICENSE).
