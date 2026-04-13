![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[Русская версия](README.ru.md)

Citeck Launcher manages Citeck namespaces and Docker containers. It is a single Go binary (~24 MB) that serves as both CLI and daemon.

> **Full documentation:** https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html (Russian: https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server.html)

## Quick Start

Prerequisites: Docker (running).

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

The install script downloads the latest release for your platform and installs to `/usr/local/bin/`. The wizard sets up the namespace and starts the platform.

> **Important:** The `citeck install` command is an **interactive TUI wizard** and requires a real terminal. The wizard prints the generated admin password **once** at the end — make sure to copy and save it, as you won't be able to recover it after closing the screen. If you lose it, reset it via `citeck setup admin-password` (see the [commands reference](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)). Pressing `Ctrl+C` before the final "write configuration" step exits without making changes; if interrupted later, check `/opt/citeck/conf/` for partial state.
>
> Automated / non-interactive install is a future feature — please file an issue if you need it.

To **upgrade** an existing installation, run the same one-liner — the script detects the installed version, prompts to update, stops the daemon, and replaces the binary (a backup is kept at `/usr/local/bin/citeck.bak`, restorable via `bash install.sh --rollback`).

### Offline Install

For servers without internet access, download both the binary and the workspace
archive beforehand:

1. **Binary:** from the [releases page](https://github.com/Citeck/citeck-launcher/releases).
2. **Workspace archive:** from [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   (Releases section, or "Download ZIP" button). This archive contains the bundle
   definitions that the launcher would normally fetch from git.

Then on the target server:

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

The `--workspace` flag extracts bundle repos locally so no internet is needed during startup.
To update workspace later from a new archive without reinstalling: `citeck update -f <zip>`.

## Features

- **Interactive installer** with TLS auto-detection (Let's Encrypt / self-signed / custom cert)
- **CLI and daemon upgrades** — `install.sh` swaps the daemon binary while platform containers keep running; the new daemon adopts them via deployment-hash matching (k8s-style control-plane restart). Apps whose deployment hash is unchanged keep running with zero downtime; apps whose spec hash changed in the new version are recreated (typical downtime 1–5 min per app, Java services longer). Run `citeck reload --dry-run` after the binary swap to preview which containers will be recreated.
- **i18n** with 8 languages: English, Russian, Chinese, Spanish, German, French, Portuguese, Japanese
- **Real-time updates** via SSE events (app status, resource usage)
- **Volume snapshots** with export/import (ZIP + tar.xz)
- **Let's Encrypt** integration with auto-renewal (domains and IP addresses)
- **Self-healing runtime** with liveness probes, restart tracking, and pre-restart diagnostics
- **Shell completion** for bash, zsh, fish, powershell

## CLI Usage

```
citeck install [--workspace <zip>]        Interactive setup wizard (offline with --workspace)
citeck start [app] [-d|--detach]          Start daemon/namespace (--detach = don't wait)
citeck stop [app] [-d|--detach]           Stop namespace (--detach = don't wait)
citeck restart [app] [--wait]             Restart an app or the entire namespace
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
citeck version [--short]                  Show version info
citeck completion bash|zsh|fish           Generate shell completion
citeck uninstall [--delete-data]          Remove systemd service, binary, and (optionally) data
```

Global flags: `--format (text|json)`, `--yes/-y`.

## Configuration

See the [configuration reference](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/configuration.html) for `daemon.yml` and `namespace.yml` details.

## License

See [LICENSE](LICENSE).
