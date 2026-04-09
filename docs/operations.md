# Operator Runbook

## Log Locations

| Log | Path | Rotation |
|---|---|---|
| Daemon log | `$CITECK_HOME/log/daemon.log` | 50 MB, 3 retained (`.1`, `.2`, `.3`) |
| Container logs | Docker json-file driver | 50 MB, 3 files per container |
| systemd journal | `journalctl -u citeck` | System defaults |

## Starting and Stopping

```bash
# Start
sudo systemctl start citeck

# Stop (graceful — stops all containers in order)
sudo systemctl stop citeck

# Or via CLI
citeck stop

# Restart with config reload
citeck reload
```

## Initial Setup (Install Wizard)

Run `citeck install` to configure a new deployment. The wizard is localized (8 languages) and walks through these steps:

1. **Language** — select UI locale (en, ru, zh, es, de, fr, pt, ja)
2. **Welcome** — overview of what will happen (in selected language)
3. **Hostname** — server hostname or IP (default: auto-detected outbound IP)
4. **TLS** — automatic: tries Let's Encrypt (staging check), falls back to self-signed. Works with both domains and IPs.
5. **Port** — platform port (default: 443 for HTTPS, 80 for HTTP)
6. **Authentication** — Keycloak SSO
7. **Release** — multi-level picker: latest per repo at top, "Other version..." for browsing
8. **System service** — systemd unit + optional firewall rule
9. **Start** — launch the platform, shows live progress, final URL + credentials

Every step has a sensible default — the entire wizard can be completed by pressing Enter repeatedly.

Flags: `--offline` skips network checks (LE), `--workspace <zip>` imports bundle archive for air-gapped deployments.

Re-running `citeck install` on an existing deployment skips config prompts and only offers systemd/firewall setup.

## Upgrade Launcher Binary

Run the same one-liner as for a fresh install — `install.sh` is a thin bootstrap that fetches the latest binary, and the binary itself (`citeck install`) detects whether to do a fresh install or an upgrade and handles both transparently:

```bash
curl -fsSL https://raw.githubusercontent.com/Citeck/citeck-launcher/release/2.1.0/install.sh | bash
```

The flow:

1. `install.sh` fetches the newest stable `v2.x` tag from GitHub and compares with the currently installed version. If already on the latest, it just execs `citeck install` on the installed binary (no download).
2. If an upgrade is needed, `install.sh` downloads the new binary to a temp file and execs `<temp-binary> install`.
3. The binary's install command detects it's running from outside `/usr/local/bin/citeck`, reads the currently installed version, and:
   - **Fresh install**: atomically copies itself to `/usr/local/bin/citeck`, re-execs from there so the wizard uses the installed path.
   - **Upgrade**: prompts for confirmation, backs up the current binary to `citeck.bak`, stops the old daemon **without touching platform containers** (v2.1.0+ uses the clean HTTP detach `POST /daemon/shutdown?leave_running=true`; v2.0.0 uses SIGKILL — Docker owns the containers, so they keep running), atomically swaps the binary via `rename(2)`, and starts the new daemon.
4. When the install wizard's systemd unit (`/etc/systemd/system/citeck.service`) is present, the start phase uses `systemctl start citeck`; otherwise it forks a detached daemon directly. For SIGKILL upgrades the unit is also masked before the kill so `Restart=on-failure` doesn't respawn the old binary during the swap window.

The upgrade is zero-downtime — the platform containers keep serving traffic throughout. If the stop path fails (e.g., SIGKILL couldn't kill the daemon within 30 seconds), the installer reports the error and leaves the old binary in place; re-running the installer retries the flow.

### Rollback

```bash
bash install.sh --rollback
```

`install.sh --rollback` delegates straight to `citeck install --rollback` (no shell logic). That command restores `/usr/local/bin/citeck` from the `.bak` file created during the last upgrade, stops the current daemon via the same preserve-platform path, and starts the restored binary. Containers keep running across the swap. Only one backup is kept (the immediately preceding version).

### Offline / local-binary upgrade

```bash
bash install.sh --file ./citeck_2.1.0_linux_amd64
```

Skips GitHub and execs `./citeck_2.1.0_linux_amd64 install` directly. All the lifecycle detection happens in the binary.

## Upgrade Bundle Version

Change the Citeck platform version (bundle) without reinstalling:

```bash
# List available versions
citeck upgrade --list

# Upgrade to a specific version
citeck upgrade community:2026.1
```

This updates `bundleRef` in `namespace.yml` and triggers a reload. Only containers whose images changed will be recreated (smart regenerate via deployment hash comparison).

The same operation is available in the Web UI via the upgrade button in the Dashboard sidebar.

## Offline Deployment

Deploy without internet access:

1. **Prepare** (on a machine with internet):
   - Clone the workspace repo and bundle repos
   - Package them into a ZIP: `workspace.zip`
   - Pre-pull Docker images and export them: `docker save -o images.tar <images...>`

2. **Deploy** (on the target machine):
   ```bash
   # Import workspace + create namespace
   citeck install --workspace /path/to/workspace.zip

   # Load Docker images
   docker load -i images.tar

   # Start (offline — no git pull)
   citeck start
   ```

In server mode, the resolver always operates offline (no auto-pull). The `--offline` flag is implicit.

## Image Cleanup

After upgrading, old Docker images may remain. Clean them up:

```bash
# Prune dangling (unused) images
citeck clean --images

# Also clean orphaned containers + volumes
citeck clean --images --volumes --execute
```

## Backup / Restore via Snapshots

### Export

```bash
# Interactive: prompts for output directory, auto-stops/starts namespace
citeck snapshot export

# Specify output directory
citeck snapshot export --dir /mnt/backup/

# Non-interactive (default dir, auto-stop/start, no prompts)
citeck snapshot export --yes
```

If the namespace is running, the CLI will ask to stop it, export volumes, then start it back. If already stopped, it exports directly without starting afterward.

### Import

```bash
# Stop namespace first
citeck stop

# Import snapshot
citeck snapshot import my-namespace_2026-04-07_12-00-00.zip

# Start
citeck start
```

### List

```bash
citeck snapshot list
```

## mTLS Certificate Management

Required when Web UI listens on non-localhost (`0.0.0.0`).

### Generate Client Certificate

```bash
citeck webui cert --name admin
# Output: cert and key paths
```

### List Certificates

```bash
citeck webui list
```

### Revoke Certificate

```bash
citeck webui revoke --name admin
```

Certificate changes take effect immediately (dynamic CA pool reload).

### Using Client Certificate

```bash
curl --cert /path/client.crt --key /path/client.key \
  --cacert $CITECK_HOME/conf/webui-tls/server.crt \
  https://your-host:7088/api/v1/daemon/status
```

## Debugging Startup Failures

### Daemon won't start

```bash
# Check if another instance is running
pgrep -a citeck

# Check socket
ls -la /run/citeck/daemon.sock

# Run in foreground with debug logging
citeck start --foreground
# Then via socket:
curl --unix-socket /run/citeck/daemon.sock -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"level":"debug"}' http://localhost/api/v1/daemon/loglevel
```

### Container won't start

```bash
# Check app status
citeck status

# View app logs
citeck logs <app-name> --follow

# Inspect container details
curl --unix-socket /run/citeck/daemon.sock http://localhost/api/v1/apps/<name>/inspect

# Run diagnostics
citeck diagnose
```

### Health check

```bash
citeck health
# Exit code 0 = healthy, 1 = degraded/unhealthy

# Or via API
curl --unix-socket /run/citeck/daemon.sock http://localhost/api/v1/health
```

## Self-Healing & Restart Diagnostics

The daemon monitors all running containers with liveness probes. When a service becomes unhealthy (3 consecutive probe failures at 30s intervals = 90s), the daemon:

1. Captures a thread dump (Java apps: `jcmd 1 Thread.print`) and last 500 log lines
2. Saves diagnostics to `$VOLUMES/diagnostics/<app>/<timestamp>.txt`
3. Restarts the container
4. Records a restart event (visible in Web UI → Restart Events panel)

### View restart events

```bash
curl --unix-socket /run/citeck/daemon.sock http://localhost/api/v1/namespace/restart-events
```

### View diagnostics file

```bash
curl --unix-socket /run/citeck/daemon.sock \
  'http://localhost/api/v1/diagnostics-file?path=<path-from-event>'
```

### Disable liveness for a specific app

In `namespace.yml`:
```yaml
webapps:
  emodel:
    livenessDisabled: true
```

### Disable liveness globally

In `daemon.yml`:
```yaml
reconciler:
  livenessEnabled: false
```

Diagnostics files older than 7 days are automatically cleaned up.

## System Dump

Collect full diagnostic bundle:

```bash
curl --unix-socket /run/citeck/daemon.sock \
  'http://localhost/api/v1/system/dump?format=zip' > system-dump.zip
```

Contents: `system-info.json`, `namespace.yml`, `daemon.yml`, `daemon-logs/`, `logs/<app>.log`.

## Prometheus Monitoring

```bash
curl --unix-socket /run/citeck/daemon.sock http://localhost/api/v1/metrics
```

Key metrics:
- `citeck_build_info{version="..."}` — build version
- `citeck_apps_running` / `citeck_apps_total` — app counts
- `citeck_apps_failed` — failed apps (alert on > 0)
- `citeck_namespace_status{status="RUNNING"}` — namespace state
- `citeck_uptime_seconds` — daemon uptime

## Common Error Codes

| Code | Meaning | Action |
|---|---|---|
| `NOT_CONFIGURED` | No namespace.yml found | Run `citeck install` or create config |
| `APP_NOT_FOUND` | App name doesn't exist | Check `citeck status` for available apps |
| `SNAPSHOT_IN_PROGRESS` | Another snapshot op running | Wait and retry |
| `INVALID_CONFIG` | Config parse error | Fix namespace.yml syntax |
| `NAMESPACE_RUNNING` | Op requires stopped namespace | Run `citeck stop` first |
| `CSRF_MISSING` | Missing X-Citeck-CSRF header | Add header to POST/PUT/DELETE requests |

## Secret Rotation

Secrets are stored in `$CITECK_HOME/conf/secrets/` (server mode) or `launcher.db` (desktop).

```bash
# Via CLI (if implemented)
citeck secret create --id gitlab-token --value "new-token"

# Via API
curl --unix-socket /run/citeck/daemon.sock -X POST \
  -H 'Content-Type: application/json' \
  -d '{"id":"gitlab-token","name":"GitLab","type":"token","value":"new-value"}' \
  http://localhost/api/v1/secrets
```

After rotating, run `citeck reload` to apply.

## Let's Encrypt

Certificates are auto-obtained and auto-renewed when:
- `proxy.tls.enabled: true`
- `proxy.tls.letsEncrypt: true`
- `proxy.host` is a public hostname (not `localhost`)
- Port 443 is accessible from the internet

Certs are stored in `$CITECK_HOME/data/acme/`. Renewal runs automatically; on success, the proxy container is restarted.
