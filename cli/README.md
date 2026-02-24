# Citeck CLI

Headless CLI tool for running Citeck on servers without a GUI.

## Requirements

- Docker
- Linux x64

Java is **not required** — the CLI binary includes an embedded JRE.

## Quick Start

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-install.sh | sudo bash -s install
```

The install script downloads the tar.gz archive (with embedded JRE), verifies
its SHA-256 checksum, extracts to `/opt/citeck`, creates a symlink at
`/usr/local/bin/citeck`, and runs `citeck install`.

If the matching archive file is placed next to the script, it will be used
directly instead of downloading (useful for air-gapped environments).

After installation any call to the script is forwarded to the installed binary,
e.g. `./citeck-install.sh status` works like `citeck status`.
To upgrade, download a newer `citeck-install.sh` and run it — it will detect
the version mismatch, stop the running daemon, install the new version, and
restart the daemon via systemd if the service is enabled.

## Commands

| Command | Description |
|---------|-------------|
| `sudo citeck install` | Interactive setup wizard |
| `sudo citeck uninstall` | Uninstall platform |
| `citeck start [--foreground]` | Start platform (auto-starts daemon if needed) |
| `citeck stop [--shutdown]` | Stop platform (`--shutdown` also stops daemon) |
| `citeck status [--watch] [--apps]` | Show status (`--watch` streams events) |
| `citeck reload` | Reload configuration from YAML files |

### install

Interactive wizard that creates configuration files and sets up the platform:

1. Loads workspace configuration (from git or local cache)
2. Display name, authentication type (Basic / Keycloak), users
3. Server hostname (localhost / auto-detect public IP / manual)
4. TLS configuration (self-signed / Let's Encrypt / existing certs)
5. Server port (default: 443 if TLS enabled, 80 otherwise)
6. PgAdmin (enabled by default)
7. Bundle selection (interactive menu from workspace repos)
8. Snapshot selection (if available in workspace config)
9. Firewall configuration (detects ufw/firewalld, opens required ports)
10. Systemd service installation (`citeck.service`, enabled on boot)

For Let's Encrypt, the wizard first validates the configuration against the
**staging API** to verify DNS and port 80 accessibility without risking
production rate limits. If staging succeeds, a production certificate is
requested. If it fails, the user is returned to the TLS configuration menu.

### start

Starts the daemon and namespace. If the daemon is already running, sends a
start command via Unix socket. In `--foreground` mode, the daemon runs in the
current process; otherwise it forks a background process.

### stop

Stops the namespace. With `--shutdown`, also stops the daemon process.

### status

Shows namespace status, bundle reference, and optionally a table of
applications with their status, image, CPU, and memory usage.
`--watch` streams real-time status change events via WebSocket.

### reload

Reloads workspace configuration (with git pull if available), reloads the
static bundle if configured, and reloads the namespace configuration from disk.

### uninstall

Full reverse of installation. Requires root.

1. Stops the running namespace and shuts down the daemon
2. Stops, disables, and removes the `citeck` systemd service
3. Prompts to close firewall ports (reads port from `namespace.yml`)
4. Prompts for data cleanup with typed confirmation:
   - Keep all data (default)
   - Delete configuration only (`/opt/citeck/conf/`)
   - Delete all platform data (`/opt/citeck/`)
5. Removes the `/usr/local/bin/citeck` symlink

## Architecture

### Daemon

The CLI runs a background daemon process that manages the platform lifecycle.
CLI commands communicate with the daemon via a Unix domain socket
(`/run/citeck/daemon.sock`).

```
citeck start ──► DaemonLifecycle.start()
                  │
                  ├── DaemonServices.init()    (Docker, Git, Bundles)
                  ├── NamespaceConfigManager    (load config, import snapshot)
                  ├── DaemonServer              (HTTP API on Unix socket)
                  └── CertRenewalService        (Let's Encrypt auto-renewal)
```

### HTTP API

The daemon exposes a REST API over the Unix socket. All paths are prefixed
with `/api/v1`.

#### Daemon

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/daemon/status` | Daemon status (running, PID, uptime, version) |
| POST | `/api/v1/daemon/shutdown` | Graceful shutdown |

#### Namespace

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/namespace` | Namespace config and app statuses |
| POST | `/api/v1/namespace/start` | Start namespace |
| POST | `/api/v1/namespace/stop` | Stop namespace |
| POST | `/api/v1/namespace/reload` | Reload configuration |

#### Events

| Method | Path | Description |
|--------|------|-------------|
| WS | `/api/v1/events` | Real-time status change stream |

Event types: `ns_status_changed`, `app_status_changed`.

### Services

| Service | Purpose |
|---------|---------|
| `DaemonServices` | Top-level container: Docker API, Git, Bundles, Actions |
| `NamespaceConfigManager` | Loads namespace config, resolves bundles, imports snapshots |
| `DaemonServer` | HTTP/WebSocket API on Unix socket (Ktor CIO) |
| `CertRenewalService` | Checks certificate expiry every 12h, renews at 50% of validity period |
| `AcmeClient` | RFC 8555 ACME client for Let's Encrypt (HTTP-01 challenge) |

## Directory Structure

All files are stored under the home directory, determined by the `citeck.home`
system property (defaults to `/opt/citeck`).

```
/opt/citeck/
├── conf/
│   ├── namespace.yml           # Platform settings
│   └── tls/                    # TLS certificates
│       ├── server.crt          # Self-signed certificate
│       ├── server.key          # Self-signed key
│       ├── fullchain.pem       # Let's Encrypt certificate
│       └── privkey.pem         # Let's Encrypt key
├── data/
│   ├── acme/                   # Let's Encrypt account data
│   │   ├── account-key.json
│   │   └── account-url.txt
│   ├── bundles/                # Git-cloned bundle repos
│   ├── volumes/                # Docker volumes
│   ├── workspace/              # Workspace git repo clone
│   ├── snapshots/              # Downloaded snapshots + import markers
│   ├── rtfiles/                # Runtime file storage
│   └── runtime.yml             # Runtime state
└── log/
    └── daemon.log

/run/citeck/daemon.sock         # Unix domain socket
```

Directories are created automatically by `citeck install`.

## Configuration

### namespace.yml

Platform settings: authentication, port, TLS, bundle reference.

```yaml
id: default
name: "Production"
bundleRef: "community:4.12.0"
snapshot: ""
authentication:
  type: BASIC
  users: [admin]
citeckProxy:
  port: 443
  host: "citeck.example.com"
  tls:
    enabled: true
    certPath: "/opt/citeck/conf/tls/fullchain.pem"
    keyPath: "/opt/citeck/conf/tls/privkey.pem"
    letsEncrypt: true
pgAdmin:
  enabled: true
webapps:
  gateway:
    heapSize: "1g"
    memoryLimit: "2g"
```

Minimal example (HTTP, localhost):

```yaml
id: default
name: "Dev"
bundleRef: "community:LATEST"
authentication:
  type: BASIC
  users: [admin]
citeckProxy:
  port: 80
pgAdmin:
  enabled: true
```

### Workspace

Workspace configuration (bundle repos, snapshots, webapp defaults) is loaded
from a git repository or provided as a local archive during installation.
Bundles are stored inside the workspace directory and selected interactively
during `citeck install`.

The workspace repo is cloned to `data/workspace/` automatically.
To use a local archive instead, extract it into that directory before running
`citeck install`.

## Building

```bash
./gradlew :cli:dist
```

Output:
- `cli/build/dist/citeck-cli-{version}-linux_x64.tar.gz`
- `cli/build/dist/citeck-install.sh`

### Dependencies

- **CLI framework**: [Clikt](https://github.com/ajalt/clikt) 5.0.3
- **HTTP server/client**: Ktor (CIO engine) — daemon API, ACME, snapshot downloads
- **Core module**: Shared services (Docker, Git, Bundles, Namespace generation)
- **Java target**: 25
