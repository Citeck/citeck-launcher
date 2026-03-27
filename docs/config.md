# Configuration Reference

## daemon.yml

Controls the daemon server. Located at `$CITECK_HOME/conf/daemon.yml`.

```yaml
server:
  webui:
    enabled: true              # Enable/disable Web UI TCP listener
    listen: "127.0.0.1:8088"   # Listen address (0.0.0.0 enables mTLS)

reconciler:
  interval: 60                 # Reconciliation interval (seconds)
  livenessPeriod: 30000        # Container liveness check period (ms)

docker:
  pullConcurrency: 4           # Max concurrent image pulls
  stopTimeout: 10              # Container stop timeout (seconds)
```

### Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `server.webui.enabled` | bool | `true` | Enable Web UI |
| `server.webui.listen` | string | `127.0.0.1:8088` | TCP listen address. Set to `0.0.0.0:8088` for remote access (requires mTLS) |
| `reconciler.interval` | int | `60` | Seconds between reconciliation loops |
| `reconciler.livenessPeriod` | int | `30000` | Milliseconds between liveness checks |
| `docker.pullConcurrency` | int | `4` | Max concurrent image pulls |
| `docker.stopTimeout` | int | `10` | Seconds to wait for container stop before kill |

---

## namespace.yml

Defines the ECOS namespace. Located at `$CITECK_HOME/conf/namespace.yml`.

```yaml
id: default
name: My ECOS
snapshot: ""                    # Snapshot ID for auto-import on first start
template: ""                    # Template ID for namespace creation

bundleRef: "community/2025.12"  # Bundle reference (repo/version)

authentication:
  type: BASIC                   # BASIC or KEYCLOAK
  users:                        # Users for BASIC auth (user:password)
    - "admin:admin"

proxy:
  host: localhost               # External hostname
  port: 443                     # External port
  image: ""                     # Custom proxy image (optional)
  tls:
    enabled: false              # Enable HTTPS
    certPath: ""                # Custom TLS cert path
    keyPath: ""                 # Custom TLS key path
    letsEncrypt: false          # Use Let's Encrypt (requires public hostname)

pgAdmin:
  enabled: true                 # Enable pgAdmin container
  image: ""                     # Custom image (optional)

mongodb:
  image: ""                     # Custom MongoDB image (optional)

webapps:                        # Per-webapp overrides
  emodel:
    enabled: true
    image: "custom/emodel:1.0"  # Override image
    environments:               # Extra environment variables
      JAVA_OPTS: "-Xmx2g"
    heapSize: "2g"
    memoryLimit: "3g"
    debugPort: 5005
    serverPort: 0               # Auto-assigned
    springProfiles: ""
    javaOpts: ""
    cloudConfig: {}             # Spring Cloud Config overrides
    dataSources: {}             # Custom data sources
```

### Authentication

| Type | Description |
|---|---|
| `BASIC` | HTTP basic auth. Users defined as `user:password` pairs |
| `KEYCLOAK` | Keycloak OIDC. Launcher manages a Keycloak container |

### TLS Modes

| Mode | Config |
|---|---|
| No TLS | `tls.enabled: false` |
| Self-signed | `tls.enabled: true` (auto-generated) |
| Let's Encrypt | `tls.enabled: true`, `tls.letsEncrypt: true` |
| Custom cert | `tls.enabled: true`, `tls.certPath: /path/cert.pem`, `tls.keyPath: /path/key.pem` |

### Bundle Reference Format

```
bundleRef: "repo-id/version"
```

Examples: `community/2025.12`, `enterprise/2025.12`.

The launcher resolves bundles from git repositories configured in the workspace config.

---

## Environment Variables

| Variable | Description |
|---|---|
| `CITECK_HOME` | Base directory (default: `/opt/citeck` or `~/.citeck`) |
| `CITECK_HOST` | Remote daemon host:port (for CLI) |
| `CITECK_TLS_CERT` | Client cert path (auto-discovered) |
| `CITECK_TLS_KEY` | Client key path (auto-discovered) |

---

## File Layout

### Server Mode (default)

```
/opt/citeck/                    # CITECK_HOME
├── conf/
│   ├── daemon.yml              # Daemon config
│   ├── namespace.yml           # Namespace config
│   ├── jwt-secret              # JWT secret (auto-generated)
│   ├── webui-ca/               # Client CA certs (mTLS)
│   ├── webui-tls/              # Server cert/key (mTLS)
│   └── tls/                    # Let's Encrypt or custom certs
├── data/
│   ├── bundles/                # Cloned bundle repos
│   └── acme/                   # ACME state
├── volumes/
│   └── default/                # Namespace volumes
│       ├── volumes/            # App bind-mount data
│       ├── snapshots/          # Exported snapshots
│       └── appfiles/           # Extracted embedded files
├── log/
│   └── daemon.log              # Daemon log (rotated: .1, .2, .3)
└── run/
    └── daemon.sock             # Unix socket
```

### Desktop Mode (`--desktop`)

```
~/.citeck/
├── launcher.db                 # SQLite database (secrets, state)
├── workspaces/
│   └── default/
│       └── namespaces/
│           └── default/
│               └── namespace.yml
└── ... (same structure as server)
```
