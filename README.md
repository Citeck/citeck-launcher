![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

Citeck Launcher manages Citeck ECOS namespaces and Docker containers. It is a single Go binary (~14 MB) that serves as both CLI and daemon, with an embedded React Web UI.

## Quick Start

### Prerequisites

- Docker (running)

### Install

Download the latest binary from the [releases page](https://github.com/Citeck/citeck-launcher/releases), then run:

```bash
chmod +x citeck
sudo mv citeck /usr/local/bin/

# Interactive install wizard (creates config, optional systemd service)
citeck install
```

The install wizard guides you through language selection, namespace setup, TLS, port configuration, and optional systemd service.

### Start

```bash
# Foreground (for development or debugging)
citeck start --foreground

# As a systemd service (if installed via wizard)
sudo systemctl start citeck
```

The Web UI is available at `http://127.0.0.1:7088` by default.

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
citeck start [--foreground] [--no-ui]   Start daemon and namespace
citeck stop                             Stop namespace and daemon
citeck status [--watch]                 Show namespace status
citeck health                           Health check (exit code 0/1)
citeck reload                           Reload config and regenerate containers
citeck logs <app> [--follow]            Stream app logs
citeck exec <app> -- <command>          Execute command in container
citeck restart <app>                    Restart an app
citeck apply <file>                     Apply namespace config
citeck diff                             Diff running config vs file
citeck snapshot list|export|import      Manage volume snapshots
citeck cert generate|list|revoke        Manage mTLS client certificates
citeck diagnose                         Run diagnostics
citeck config show|edit                 View/edit namespace config
citeck completion bash|zsh|fish         Generate shell completion
citeck install                          Interactive setup wizard
citeck uninstall                        Remove systemd service and config
```

Global flags: `--host`, `--tls-cert`, `--tls-key`, `--server-cert`, `--insecure`, `--output json`.

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

# Go only (skip web rebuild)
make build-fast

# Tests
go test -race ./internal/...
cd web && npx vitest run

# Lint
golangci-lint run
cd web && npm run lint
```

Requires: Go 1.22+, Node.js 20+.

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

Defines the ECOS namespace. Located at `$CITECK_HOME/conf/namespace.yml`.

```yaml
id: default
name: My Namespace
bundleRef: "community/2025.12"
authentication:
  type: BASIC                   # or KEYCLOAK
  users: ["admin:admin"]
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

## License

See [LICENSE](LICENSE).
