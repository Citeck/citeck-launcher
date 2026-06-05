![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

**English** · [Русский](readme/README.ru.md) · [中文](readme/README.zh.md) · [Español](readme/README.es.md) · [Deutsch](readme/README.de.md) · [Français](readme/README.fr.md) · [Português](readme/README.pt.md) · [日本語](readme/README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**Install and run a full Citeck platform — as a desktop app on your computer, or with a single command on a server.**

Citeck Launcher is the official installer and container manager for the **Citeck** low-code BPM/ECM platform. A single ~24 MB binary works as a command-line tool, a background daemon, and a cross-platform desktop app — running every Citeck service (Keycloak, PostgreSQL, RabbitMQ, and the Citeck web apps) as a Docker container and grouping them into isolated namespaces. Which apps and versions to run is defined by a **bundle** (for example, a Community or Enterprise release).

[Citeck](https://github.com/Citeck) is an open-source platform for building business applications, combining **no-code, low-code, and pro-code** approaches to manage content and processes. In practice, you use it to **manage documents and records (ECM), automate business processes and approval workflows with a built-in BPMN designer, and build internal apps — portals, CRM, case management — with little or no code**, with user accounts, roles, and permissions built in. It's a self-hosted alternative to proprietary ECM/BPM suites, suitable for everyone from business analysts to developers.

The **Community** edition is fully open source and free — it covers the platform's core functionality and is designed to be friendly to extensions of any kind. For more demanding setups, the commercial **Enterprise** edition adds professional support and extra enterprise features. This launcher installs either edition. For questions or a consultation, [get in touch with the Citeck team](https://www.citeck.ru/contacts/).

## Desktop or server?

There are two ways to run it — pick the one that matches **where** you want Citeck to run:

| | 🖥 **Desktop app** | 🖧 **Server (CLI)** |
|---|---|---|
| For | Your own computer | A Linux server / VM (usually over SSH) |
| Install | Download an installer, click through the wizard | One `curl … \| bash` command |
| Interface | Native app window (GUI) | Terminal — `citeck` CLI + setup wizard (TUI) |
| Start here | [Desktop App](#desktop-app) | [Server Install](#server-install) |

> **Heads-up:** the `curl … | bash` quick start and the `citeck` CLI in this README are for **server installs**. On your own computer, run Citeck through the **Desktop app** — everything there is done from the UI.

**Requirements:** Docker; about **16 GB** RAM for the Community edition (**24 GB** for Enterprise's ~24 services); and tens of GB of disk for images.

## Desktop App

The **desktop application** runs Citeck on your own Windows, macOS, or Linux machine — a regular app window, no command line needed. Citeck keeps running in the background even after you close the window.

Desktop installers are attached to each [GitHub release](https://github.com/Citeck/citeck-launcher/releases) — download the one for your platform:

| OS | File | Arch |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Each installer has a `.sha256` sidecar for verification. Your data is preserved across upgrades.

## Server Install

> **For a Linux server or VM** — run these steps on the server, over SSH.

Prerequisites: a Linux host with Docker running.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

The install script downloads the latest release for your platform and installs to `/usr/local/bin/`. The wizard then sets up the namespace and starts the platform.

> **Important:** `citeck install` is an **interactive TUI wizard** and requires a real terminal. The wizard prints the generated admin password **once** at the end — copy and save it, as it can't be recovered after closing the screen. If you lose it, reset it via `citeck setup admin-password` (see the [commands reference](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)).

To **upgrade** an existing server install, run the same one-liner — the script detects the installed version, prompts to update, stops the daemon, and replaces the binary (a backup is kept at `/usr/local/bin/citeck.bak`, restorable via `citeck install --rollback`).

## Features

- **Interactive installer** with TLS auto-detection (Let's Encrypt / self-signed / custom cert)
- **i18n** with 8 languages: English, Russian, Chinese, Spanish, German, French, Portuguese, Japanese
- **Real-time updates** via SSE events (app status, resource usage)
- **Volume snapshots** with export/import (ZIP + tar.xz)
- **Let's Encrypt** integration with auto-renewal (domains and IP addresses)
- **Self-healing runtime** with liveness probes, restart tracking, and pre-restart diagnostics
- **Shell completion** for bash, zsh, fish, PowerShell

## CLI Usage (server mode)

These commands manage a **server-mode** install over the CLI. (In desktop mode the same operations are available from the app's UI.)

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

Global flags: `--format (text|json)`, `--yes/-y`.

## Documentation

- **Server mode:** [Launcher server-mode docs](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) — install, configuration (`daemon.yml` / `namespace.yml`), and the [commands reference](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).
- **Desktop app:** [Launcher desktop-mode docs](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html).

## License

Citeck Launcher is open source under the **LGPL-3.0** license — see [LICENSE](LICENSE).
