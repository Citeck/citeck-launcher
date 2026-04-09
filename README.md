![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[Русская версия](README.ru.md)

Citeck Launcher manages Citeck namespaces and Docker containers. It is a single Go binary (~14 MB) that serves as both CLI and daemon.

## Quick Start

Prerequisites: Docker (running).

```bash
curl -fsSL https://raw.githubusercontent.com/Citeck/citeck-launcher/release/2.1.0/install.sh | bash
```

The install script downloads the latest release for your platform and installs to `/usr/local/bin/`. The wizard sets up the namespace and starts the platform.

To **upgrade** an existing installation, run the same one-liner — the script detects the installed version, prompts to update, stops the daemon, and replaces the binary (a backup is kept at `/usr/local/bin/citeck.bak`, restorable via `bash install.sh --rollback`).

### Offline Install

```bash
# Download both the binary and the workspace ZIP, then:
citeck install --workspace /path/to/workspace.zip
```

The `--workspace` flag extracts bundle repos locally so no internet is needed during startup.

## Features

- **Interactive installer** with TLS auto-detection (Let's Encrypt / self-signed / custom cert)
- **i18n** with 8 languages: English, Russian, Chinese, Spanish, German, French, Portuguese, Japanese
- **Real-time updates** via SSE events (app status, resource usage)
- **Volume snapshots** with export/import (ZIP + tar.xz)
- **Let's Encrypt** integration with auto-renewal (domains and IP addresses)
- **Self-healing runtime** with liveness probes, restart tracking, and pre-restart diagnostics
- **Shell completion** for bash, zsh, fish, powershell

## CLI Usage

```
citeck install [--workspace <zip>]      Interactive setup wizard (offline with --workspace)
citeck start [app] [-d]                 Start daemon/namespace (-d = detach, don't wait)
citeck stop [app] [-d]                  Stop namespace (-d = detach, don't wait)
citeck restart [app]                    Restart an app or the entire namespace
citeck status [--watch]                 Show namespace status
citeck health                           Health check (exit 0=healthy, 1=unhealthy)
citeck reload                           Reload config and regenerate containers
citeck upgrade [ref] [--list|--dry-run] Upgrade bundle version
citeck logs [app] [--follow]            Stream logs (daemon if no app)
citeck exec <app> -- <command>          Execute command in container
citeck apply <file> [--dry-run]         Apply namespace config
citeck diff -f <file>                   Diff running config vs file
citeck snapshot list|export|import      Manage volume snapshots (auto stop/start)
citeck clean [--images] [--execute]     Clean orphaned resources / prune images
citeck cert generate|status|letsencrypt Server TLS certificates

citeck workspace import|update          Import or update workspace repos
citeck diagnose                         Run diagnostics
citeck validate                         Validate namespace config
citeck completion bash|zsh|fish         Generate shell completion
citeck setup [setting]                  Configure settings (TUI menu or by ID)
citeck setup history                    Show config change history
citeck setup rollback [id]              Rollback a config change
citeck uninstall                        Remove systemd service and config
```

Global flags: `--host`, `--tls-cert`, `--tls-key`, `--server-cert`, `--insecure`, `--format json`.

## Configuration

### daemon.yml

Controls the daemon server. Located at `$CITECK_HOME/conf/daemon.yml`.

```yaml
locale: en                      # Language: en, ru, zh, es, de, fr, pt, ja
server:
  listen: ":7088"
reconciler:
  interval: 30
docker:
  pullConcurrency: 4
```

### namespace.yml

Defines the namespace. Located at `$CITECK_HOME/conf/namespace.yml`.

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

## License

See [LICENSE](LICENSE).
