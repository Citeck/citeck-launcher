![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

**English** · [Русский](readme/README.ru.md) · [中文](readme/README.zh.md) · [Español](readme/README.es.md) · [Deutsch](readme/README.de.md) · [Français](readme/README.fr.md) · [Português](readme/README.pt.md) · [日本語](readme/README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**The official way to run Citeck.**

[Citeck](https://github.com/Citeck) is a self-hosted, open-source low-code platform that replaces proprietary ECM/BPM suites. You use it for almost any task involving corporate documents, from contract and purchase approvals to HR processes, an electronic archive, or a corporate portal. You draw each process's route in the built-in BPMN designer and configure document types without code; users, roles, and permissions come out of the box.

Running it by hand means orchestrating a couple of dozen Docker services. Citeck Launcher does that for you: a single ~24 MB binary that installs the platform, runs every service (Keycloak, PostgreSQL, RabbitMQ, and the Citeck web apps) as a Docker container, keeps them healthy, and upgrades them — as a desktop app on your own machine, or from the command line on a server.

<!-- TODO(screenshot): add an English-locale screenshot of the launcher dashboard here, e.g.
     ![Citeck Launcher](docs/img/dashboard.png) -->

**You'll need:** Docker · **16 GB** RAM for the Community edition, **24–32 GB** for Enterprise (~24 services) · **50+ GB** of free disk for images and data. On Windows and macOS, install [Docker Desktop](https://www.docker.com/products/docker-desktop/) first.

## Desktop or server?

Two ways to run it — pick the one that matches **where** you want Citeck to run:

| | 🖥 **Desktop app** | 🖧 **Server (CLI)** |
|---|---|---|
| For | Your own computer | A Linux server / VM (usually over SSH) |
| Install | Download an installer, click through the wizard | One `curl … \| bash` command |
| Interface | Native app window (GUI) | Terminal — `citeck` CLI + setup wizard |
| Start here | [Desktop app](#desktop-app) | [Server install](#server-install) |

> **Heads-up:** the `curl … | bash` quick start and the `citeck` commands in this README are for **server installs**. On your own computer, run Citeck through the **desktop app** — everything there is done from the UI.

## Desktop app

The desktop application runs Citeck on your own Windows, macOS, or Linux machine — a regular app window, no command line needed. Citeck keeps running in the background even after you close the window.

Install Docker Desktop first, then download the installer for your platform from the [latest release](https://github.com/Citeck/citeck-launcher/releases/latest):

| OS | File | Arch |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Each installer has a `.sha256` sidecar for verification. Your data is preserved across upgrades.

## Server install

> **For a Linux server or VM** (amd64 or arm64) — run these steps on the server, over SSH. Prerequisite: Docker is installed and running.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

The script downloads the latest release for your platform, installs it to `/usr/local/bin/citeck`, and then launches the setup wizard (`citeck install`). The wizard is **interactive and needs a real terminal**. It asks you for:

- the **domain name or IP** you'll use to reach the platform in a browser;
- how to **secure the connection** — automatic, Let's Encrypt, a self-signed certificate, your own certificate, or plain HTTP. (Let's Encrypt needs a public DNS name pointing at this host and inbound port 80; if it isn't reachable, the wizard falls back to a self-signed certificate.)
- whether to deploy **demo data**, and whether to install a **systemd service**.

### First run: what to expect

**It takes a while — that's normal.** The launcher pulls several GB of Docker images, then the platform itself needs roughly **10–15 minutes** to come up: the services start in dependency order, and Keycloak imports its realm on first start. Watch the apps flip to `RUNNING` one by one:

```bash
citeck status -w
```

When everything is up, the wizard prints your access details:

```
Citeck is ready!

Open in browser:  https://<the domain you entered>/
Login:            admin / <generated password>
```

Two things to know about that screen:

- **The admin password is shown once.** Copy it — it can't be recovered afterwards. If you lose it, reset it with `citeck setup admin-password`.
- **With a self-signed certificate your browser will warn you.** That's expected — click *Advanced* → *Proceed*.

If something still looks stuck after ~20 minutes, start with `citeck diagnose` (add `--fix` to let it repair what it can) and `citeck logs <app>`.

### Upgrading the launcher

Run the same one-liner again — the script detects the installed version, prompts to update, stops the daemon, and replaces the binary. The previous binary is kept at `/usr/local/bin/citeck.bak` and restorable with `citeck install --rollback`. Your data is preserved.

## Concepts

Three words that show up throughout the CLI and the docs:

- **Namespace** — one isolated instance of the platform (its own containers, volumes, and data). Nothing to do with Linux or Kubernetes namespaces; it's a launcher concept. A typical server runs exactly one.
- **Bundle** — which apps and which versions make up a platform release, e.g. a Community or an Enterprise release. `citeck upgrade <bundle:version>` switches between them.
- **Workspace** — where those definitions come from (normally a Git repository, or an offline `.zip` for air-gapped installs).

## Everyday commands (server mode)

In desktop mode the same operations live in the app's UI.

```bash
citeck status -w                 # watch the namespace and every app
citeck logs <app> -f             # stream logs (no app = the daemon's own log)
citeck stop <app>                # stop an app — and keep it stopped across restarts
citeck start <app>               # start it again (re-attach)
citeck reload                    # apply config changes, recreate only what changed
citeck snapshot export <name>    # back up all volumes (stops the platform, then restarts it)
citeck upgrade <bundle:version>  # switch to another platform version
citeck diagnose --fix            # health checks with optional auto-repair
citeck setup                     # change settings (admin password, TLS, email, resources…)
citeck edit <app>                # edit an app's definition, kubectl-edit style
```

Note that `citeck stop <app>` **detaches** the app: it stays stopped across restarts and reloads until you run `citeck start <app>`. That's also the way to free memory on a small host — detaching a few optional apps saves several GB.

Global flags: `--format (text|json)` for scripting, `--yes/-y` to skip confirmations, `-d/--detach` to return immediately instead of waiting. Full reference: `citeck --help` or the [commands reference](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).

## What you get

- **Self-healing runtime** — liveness probes restart crashed services, and the launcher records why they crashed
- **Backup and restore** — export all volumes to a single archive, import them back on this host or another one
- **HTTPS out of the box** — Let's Encrypt with auto-renewal (domains *and* IP addresses), or your own certificate
- **Live status and logs** — resource usage and streaming logs for every service, in the desktop app or the CLI
- Localized in 8 languages, with shell completion for bash, zsh, fish, and PowerShell

## Editions

The **Community** edition is fully open source and free, and covers the platform's core functionality. The commercial **Enterprise** edition adds professional support and additional features; installing it requires a license key issued by Citeck. This launcher installs either one.

## Security model

We'd rather say this up front: **the server-mode daemon controls Docker, so treat its API as root-equivalent on the host** (`citeck exec`, for example, runs commands inside containers). That is why the safe option is the default.

- **The CLI** talks to the daemon over a Unix socket restricted to the daemon's user (mode 0600).
- **The launcher's own Web UI is disabled by default in server mode** — the CLI/TUI is the supported server interface. When you enable it (`server.webui.enabled: true` in `daemon.yml`), the daemon also listens on a TCP port. A localhost bind serves the full API with browser-CSRF protection only — that is **not** authentication, so any local user or process that can reach the port gets full control. Enable it deliberately, and only on a **single-tenant host** whose local users are all trusted with Docker/root-level access. Non-localhost binds require mTLS client certificates.
- **To close that localhost gap**, turn on API token auth: `api_auth.enabled: true` in `daemon.yml`. Every `/api` request over TCP then needs `Authorization: Bearer <token>` (or the browser session cookie issued by `GET /auth/session?token=…`). The token comes from `api_auth.token`, or is auto-generated into `conf/api-token` (mode 0600) on startup. `citeck ui` prints — and opens — an authenticated link. Static UI assets stay public; only the API is gated. The Unix socket, the desktop app, and mTLS clients are unaffected.

(This section is about the *launcher's* admin UI, not the Citeck platform UI you log into after installing.)

## Documentation

- **Server mode:** [install and configuration](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) (`daemon.yml` / `namespace.yml`) and the [commands reference](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)
- **Desktop app:** [desktop-mode docs](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)
- **Release notes:** [CHANGELOG.md](CHANGELOG.md)

## Development

Built from Go (daemon + CLI) and React (embedded web UI); the desktop app wraps the same UI in a Wails webview. Prerequisites, build targets, and the full local check gate (`make check`) are documented in [AGENTS.md](AGENTS.md).

## License and contact

Citeck Launcher is open source under the **LGPL-3.0** license — see [LICENSE](LICENSE).

For questions, Enterprise licensing, or a consultation, [get in touch with the Citeck team](https://www.citeck.ru/contacts/).
