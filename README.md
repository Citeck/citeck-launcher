![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[Русская версия](README.ru.md)

Citeck Launcher manages Citeck namespaces and Docker containers. It is a single Go binary (~14 MB) that serves as both CLI and daemon, with an embedded React Web UI.

## Quick Start

Prerequisites: Docker (running).

```bash
curl -fsSL -o citeck https://github.com/Citeck/citeck-launcher/releases/download/v2.0.0/citeck_2.0.0_linux_amd64 \
  && chmod +x citeck && sudo mv citeck /usr/local/bin/ && citeck install
```

The install wizard sets up the namespace and starts the platform.

### Offline Install

```bash
# Download both the binary and the workspace ZIP, then:
citeck install --workspace /path/to/workspace.zip
```

The `--workspace` flag extracts bundle repos locally so no internet is needed during startup.

## Features

- **Lens-inspired Web UI** with right overlay drawer, bottom panel with tabs (logs, config), and drag-to-resize
- **i18n** with 8 languages: English, Russian, Chinese, Spanish, German, French, Portuguese, Japanese
- **Real-time updates** via SSE events (app status, resource usage)
- **Full service catalog** visible even when namespace is stopped
- **Log viewer** with virtual scrolling (50K lines), regex search, level filtering, streaming follow
- **Volume snapshots** with export/import (ZIP + tar.xz)
- **mTLS** for secure remote Web UI access (client certificates)
- **Let's Encrypt** integration with auto-renewal
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
citeck webui cert|list|revoke            Manage Web UI access certs (.p12 for browser)
citeck cert generate|status|letsencrypt Server TLS certificates
citeck self-update [--check|--file]     Update launcher binary (with rollback)
citeck workspace import|update          Import or update workspace repos
citeck diagnose                         Run diagnostics
citeck validate                         Validate namespace config
citeck completion bash|zsh|fish         Generate shell completion
citeck uninstall                        Remove systemd service and config
```

Global flags: `--host`, `--tls-cert`, `--tls-key`, `--server-cert`, `--insecure`, `--format json`.

## Architecture

### Go Daemon (`internal/`)

| Package | Purpose |
|---|---|
| `cli/` | Cobra CLI commands |
| `daemon/` | HTTP server, API routes, SSE events, middleware |
| `namespace/` | Config parsing, container generator, runtime state machine, reconciler |
| `docker/` | Docker SDK wrapper (containers, images, exec, logs, probes) |
| `bundle/` | Bundle definitions and resolution from git repos |
| `git/` | Git clone/pull via go-git (pure Go) |
| `config/` | Filesystem paths, daemon config |
| `storage/` | Store interface + FileStore (server) + SQLiteStore (desktop) |
| `snapshot/` | Volume snapshot export/import |
| `tlsutil/` | TLS cert utilities (self-signed, client cert, CA pool) |
| `acme/` | ACME/Let's Encrypt client + auto-renewal |
| `client/` | DaemonClient (Unix socket + mTLS TCP transport) |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`.

**Layout:** IDE-style with left sidebar, app table, right overlay drawer, and bottom panel with tabs.

**Pages:** Dashboard, App Detail, Logs (virtual list), Config Editor, Volumes, Daemon Logs, Welcome, Wizard, Secrets, Diagnostics.

**Components:** LogViewer, ConfigEditor, AppDrawerContent, AppConfigEditor, BottomPanel, RightDrawer, YamlViewer, StatusBadge, NamespaceControls.

**i18n:** 8 languages with lazy-loaded locale files and auto-detection.

### Entry Point

`cmd/citeck/main.go`

## Build from Source

```bash
# Full build (Go binary + React web UI)
make build

# Tests
make test              # Go + web
make test-race         # Go with race detector

# Lint
make lint              # Go (golangci-lint) + web (eslint)
```

Requires: Go 1.22+, Node.js 20+, pnpm.

## Configuration

### daemon.yml

Controls the daemon server. Located at `$CITECK_HOME/conf/daemon.yml`.

```yaml
locale: en                      # UI language: en, ru, zh, es, de, fr, pt, ja
server:
  webui:
    enabled: true
    listen: "127.0.0.1:7088"    # 0.0.0.0 enables mTLS
reconciler:
  interval: 30
docker:
  pullConcurrency: 4
```

### namespace.yml

Defines the namespace. Located at `$CITECK_HOME/conf/namespace.yml`.

```yaml
id: default
name: My Namespace
bundleRef: "community:2026.1"
authentication:
  type: KEYCLOAK
  users: ["admin"]
proxy:
  host: localhost
  port: 443
  tls:
    enabled: false
```

## Security

- **Unix socket**: full access (CLI only)
- **mTLS TCP** (non-localhost): full access, authenticated by client certificate
- **Localhost TCP**: restricted routes + CSRF header (`X-Citeck-CSRF`) required for mutations

## Documentation

- [API Reference](docs/api.md) — all REST endpoints
- [Configuration Reference](docs/config.md) — daemon.yml, namespace.yml, CLI flags, file layout
- [Operator Runbook](docs/operations.md) — logs, upgrade, backup, mTLS, debugging, monitoring

## License

See [LICENSE](LICENSE).
