# Rollback: Go 2.x desktop launcher → Kotlin 1.x launcher

> Status: documented 2026-05-27, based on a non-destructive audit of the Go
> 2.x desktop launcher against a Kotlin 1.x data directory. The audit
> confirmed that running Go 2.x against an existing Kotlin home dir leaves
> the Kotlin H2 store (`storage.db`) byte-identical to its pre-launch state.

## 1. Overview

The Go 2.x rewrite is designed to coexist with Kotlin 1.x on a shared data
directory rather than to replace it in place. Kotlin's primary store
(`storage.db`, H2 MVStore) is opened **read-only** by the pure-Go migrator
(`internal/h2migrate/mvstore.go` — file opened with `os.Open` which maps to
`O_RDONLY`); all Go state is written to a separate SQLite file
(`launcher.db`). The workspace directory (`ws/<wsId>/`) — git checkouts,
snapshots ZIPs, bundle caches, runtime files — uses the same on-disk
formats in both versions.

The practical consequence: a user who installs Go 2.x, decides they prefer
1.x, can uninstall the Go binary and reinstall Kotlin without losing
workspaces, namespaces, secrets, or license. Docker containers may be
recreated on first start (see §5), but volume data is preserved.

This rollback story is the inverse of `docs/porting/10-2x-status-and-porting-checklist.md`
§6 (byte-exact contract). The Go rewrite intentionally avoids destructive
edits to Kotlin-owned files so that rollback stays cheap.

## 2. What's preserved across a Go-then-Kotlin cycle

The following on-disk artifacts are either untouched by Go 2.x or written
in a format that both versions agree on. After rollback, Kotlin reads them
as if Go had never run.

### `storage.db` (Kotlin H2 MVStore)

- Location: `<launcher-home>/storage.db` (plus `storage.db.tempFile` on
  some builds — H2 internal).
- Owner: Kotlin 1.x.
- Go 2.x access: **read-only**. The pure-Go MVStore reader
  (`internal/h2migrate/mvstore.go::OpenMVStore`) opens the file via
  `os.Open` (read-only) and reconstructs map entries by walking chunks and
  B-tree pages without holding any write handle; no Go code path writes to
  this file.
- Contents preserved across rollback: workspaces, namespaces, secrets
  (encrypted), license, per-workspace state (`launcher!state`,
  `workspace-state!{wsId}`), and the full `NamespaceConfig` blob (10
  fields — see §6).

### `ws/<wsId>/repo/` (bundle git checkout)

- Format: plain `git` working copy (managed via go-git in Go, JGit in
  Kotlin). Both libraries produce on-disk-compatible repositories.
- Kotlin and Go both `pull` on demand; neither rewrites history.
- After rollback: Kotlin re-uses the working copy as-is.

### `ws/<wsId>/snapshots/*.zip`

- Format: ZIP archive containing a tar.xz of named Docker volumes plus a
  manifest. Identical wire format in both versions.
- Used by: snapshot import/export.
- After rollback: Kotlin lists, imports, and exports them unchanged.

### `ws/<wsId>/bundles/` (bundle cache)

- Format: extracted bundle YAML + bundle artifacts.
- Refreshed by both versions on bundle update; format is the public
  bundle contract documented in `docs/porting/01`.

### `ws/<wsId>/ns/<nsId>/rtfiles/` (per-namespace runtime files)

- These are the files materialized for container bind-mounts (configs,
  init scripts, etc.). They are **regenerated on every start** by the
  generator (`NamespaceGenerator.kt` in Kotlin, `internal/namespace/generator.go`
  in Go).
- Format compatibility doesn't strictly matter: a rollback-then-start
  rewrites the directory.
- Self-healing: if Go-written files differ from what Kotlin would produce,
  Kotlin's next start replaces them.

### Docker volumes (label-based discovery)

- Both versions label namespace volumes with `citeck.launcher.namespace`,
  `citeck.launcher.workspace`, and `citeck.launcher.origName`
  (see `internal/docker/client.go` and Kotlin `DockerConstants.kt`).
- After rollback: Kotlin's `VolumesRepo` finds the same volumes via the
  same labels.
- Volume data is **independent of container lifetime** — even if
  containers are recreated (§5), the data on the named volume survives.

## 3. What Go writes that Kotlin ignores

These files are Go-only. Kotlin never reads them; leaving them on disk
after rollback is harmless (they sit alongside the Kotlin data dir).

| Path | Purpose | Kotlin's view |
|---|---|---|
| `launcher.db`, `launcher.db-wal`, `launcher.db-shm` | SQLite store (desktop mode) | Ignored; Kotlin uses `storage.db` (H2) |
| `conf/daemon.yml` | Go daemon config | Ignored |
| `conf/webui-ca/`, `conf/webui-tls/` | Go mTLS / web UI certs | Ignored |
| `run/daemon.sock` | Go daemon Unix socket | Ignored |
| `run/desktop.pid` | Go single-instance lock | Ignored |
| `log/daemon.log`, `log/daemon.log.*` | Go daemon logs (rotated) | Ignored (Kotlin writes `logs/logfile.log`) |
| `data/` | Go bundle / repo cache (separate from Kotlin's `ws/`) | Ignored |
| `ws/<wsId>/ns/<nsId>/namespace.yml` | Go-only stub written by the migrator for human inspection | Ignored — Kotlin reads namespace config from H2, not from this file |

The Go-only files can be left in place (Kotlin won't trip over them) or
removed manually if disk space is a concern. They are not required for
Kotlin to function.

## 4. Procedure

The following steps assume a Linux/macOS install with the standard data
directory (`~/.citeck/launcher/`). Windows uses
`%LOCALAPPDATA%\Citeck\launcher` and the equivalent uninstaller; the
overall flow is the same.

1. **Stop the Go desktop launcher.**
   - Right-click the tray icon → Quit, or close the main window.
   - Verify no `citeck-desktop` process remains:
     `ps aux | grep citeck-desktop` (should print only the grep itself).
   - If a stale `run/desktop.pid` is present, delete it manually
     (it would only matter if you re-ran Go 2.x).

2. **Uninstall the Go binary.**
   - **Do NOT** delete the data directory.
   - **Do NOT** run `citeck uninstall --delete-data` — that command wipes
     the entire home dir, including `storage.db`.
   - Acceptable: `rm /usr/local/bin/citeck-desktop` (or wherever the
     binary was installed); or use the platform-specific uninstaller if
     one was shipped.
   - On Linux with systemd: `sudo systemctl stop citeck`
     `sudo systemctl disable citeck` then remove the unit. The data
     directory at `/opt/citeck/` (server mode) or `~/.citeck/launcher/`
     (desktop mode) stays.

3. **Reinstall Kotlin 1.x.**
   - Use the official 1.x installer / Compose Desktop bundle from the
     v1.3.x tag.
   - The installer does not overwrite `storage.db`; first launch finds
     the existing file and uses it.

4. **Launch Kotlin and verify.**
   - The Kotlin window opens to the Welcome screen.
   - Confirm all workspaces appear (count matches the pre-Go state).
   - Confirm all namespaces appear under each workspace.
   - Open a namespace; the apps list appears with the same configuration
     (incl. detached apps, locked apps, edited configs — see §6).
   - Unlock the secrets store with the original master password.

5. **(Optional) Start a namespace.**
   - First start after rollback may recreate containers — see §5.
   - Volume data on disk is preserved; only the container process
     identity changes.

## 5. Container hash drift (known behavior)

Each version computes a deployment hash per container from the inputs to
`docker create` (image digest, env vars, mounts, labels, etc.). If Go's
hash differs from Kotlin's for the same logical container, Kotlin will
treat the running container as stale and recreate it on the next start.

**Why this can happen:**

- Independent hash implementations. Go (`GetHashInput` in
  `internal/namespace/generator.go`) and Kotlin
  (`AppDef.getHashInput`) compute the same hash for identical inputs by
  design, but small generator-side differences (e.g. environment-variable
  ordering, optional label values, init-script formatting) can produce
  different hashes for containers that are functionally identical.
- Container labels added by one version that the other doesn't write.

**Practical impact on rollback:**

- The first `Start` or `Reload` in Kotlin after rollback may recreate
  some containers. This is normal — it is the same mechanism that runs
  on any version upgrade.
- **Volume data is preserved.** Docker named volumes are not tied to
  container identity; the new container mounts the existing volume.
- **In-flight in-memory state is lost.** Stateful apps that hold state in
  memory (e.g. a worker mid-job) lose that state when the container is
  recreated. This is acceptable for the apps that ship with Citeck — they
  are designed to recover from container restarts.

The same drift behavior applies in the forward direction (Kotlin → Go).
The user accepts this trade-off when crossing between major versions.

## 6. What Go didn't migrate from Kotlin (informational)

The 2026-05-27 deep parity audit (see `docs/porting/REMAINING.md` items
B1–B3) identified five categories of Kotlin-only namespace state that
Go 2.x does **not** yet surface in its UI. **These categories are not
destroyed by Go**: the H2 store is read-only from Go's perspective, so
the fields remain on disk and reappear automatically after rollback to
Kotlin.

| Category | Kotlin field | Stored in H2 under |
|---|---|---|
| Per-app detach state | `manualStoppedApps` | `entities/{wsId}!namespace` → namespace blob |
| Locked ApplicationDefs | `editedAndLockedApps` | same |
| Edited ApplicationDefs | `editedApps` | same |
| Edited mounted files | `changedRuntimeFiles` | same |
| Full NamespaceConfig | 10 fields (webapps / proxy / pgAdmin / mongodb overrides etc.) | same |

After rollback to Kotlin:
- Detached apps re-appear as detached (won't be started automatically).
- Locked apps re-appear with their lock state.
- Edited ApplicationDefs / mounted files re-appear with the user's edits.
- Any NamespaceConfig override the user set in 1.x is honored again.

The Go 2.x rewrite simply did not surface these editing UIs in all cases
during the migration. Items B1, B2, B3 in `docs/porting/REMAINING.md`
track the work to bring them to parity. *Footnote: at the time this
document was written, fixes for B1/B2/B3 may already be landing in a
parallel session — check `git log` and the `REMAINING.md` head before
assuming the gaps still exist.*

## 7. Things that can go wrong

### 🔴 `citeck uninstall --delete-data` (server-mode CLI)

This command wipes the entire home directory, including `storage.db`.
**Never run it if you intend to roll back to Kotlin.** Use the plain
uninstaller (binary removal only) or skip the CLI uninstall entirely.

### 🟡 Manual edits to `storage.db`

H2 MVStore is an open binary format, but it is not designed for external
editing. If a user opens `storage.db` with the H2 Console or an MVStore
inspector and writes back, the file can be corrupted — Kotlin will fail
to open it on next launch. The Go migrator only reads, so it never
introduces this risk; the warning applies to any third-party tool that
claims to "fix" an H2 file.

If `storage.db` is corrupted, restore from a snapshot
(`ws/<wsId>/snapshots/`) or from the user's own backup. Kotlin has no
built-in repair tool for H2 corruption.

### 🟡 Workspaces with `BASIC` auth type

The 2026-05-27 audit (item I1) noted that Go's git client does not yet
handle the `BASIC` workspace auth type for repo URLs; Kotlin does. If a
workspace was created in Kotlin with BASIC auth and the user attempted
to use it under Go, Go would fail to pull. After rollback to Kotlin,
BASIC auth works again — the workspace metadata in `storage.db` is
intact.

### 🟡 Custom-cased workspace IDs

Audit item I2: when a workspace ID contains mixed-case characters, the
case-handling of derived container / network names diverges between
Kotlin and Go in edge cases. After rollback, Kotlin will recreate
containers/networks with its expected casing on the next start (this is
a sub-case of §5's container hash drift). Volume data is preserved.

### 🟢 Leftover Go-only files

`launcher.db`, `conf/daemon.yml`, `log/daemon.log`, `data/`, the
`namespace.yml` stubs under `ws/<wsId>/ns/<nsId>/`, and the run-time
sockets/PID files are not read by Kotlin. They can be safely left in
place or removed manually. Not a problem either way.

## 8. Verification checklist after rollback

After completing §4 and launching Kotlin, verify:

- [ ] Kotlin launcher window opens to the Welcome screen.
- [ ] All workspaces are visible in the workspace picker (count matches
      pre-migration).
- [ ] All namespaces are visible under each workspace.
- [ ] The secrets store unlocks with the original master password.
- [ ] License (if installed) appears in the License view and reports as
      valid.
- [ ] At least one namespace's apps list renders with the expected
      configuration (detached, locked, edited markers visible).
- [ ] `citeck start` (or the Update & Start button) brings up the apps;
      containers may be recreated (§5) but the namespace reaches a
      RUNNING state.
- [ ] Docker volume data is intact for apps that hold persistent state
      (e.g. a Postgres volume still has its database).

If any item fails, see §9.

## 9. Support note

If something is missing after rollback:

1. **Verify `storage.db` exists and is nonzero.**
   ```
   ls -lh ~/.citeck/launcher/storage.db
   ```
   Expected: a file of at least a few hundred KB. A missing or
   zero-byte file means the rollback procedure deleted too much — restore
   from a backup or snapshot.

2. **Check the Kotlin launcher log.**
   ```
   tail -200 ~/.citeck/launcher/logs/logfile.log
   ```
   Look for H2 open errors, NPEs on workspace/namespace load, or secret
   decryption failures. Common causes: incompatible Kotlin version
   (downgrading too far past the version that wrote the file), corrupted
   H2 (see §7), or wrong master password.

3. **Restore from a snapshot if available.**
   - Snapshots live at `~/.citeck/launcher/ws/<wsId>/snapshots/*.zip`.
   - Open the Kotlin launcher, navigate to the namespace, and use the
     snapshot import dialog. The wire format is shared with Go 2.x, so
     snapshots created in either version can be imported in either
     version.

4. **Re-run the Go migrator (read-only).**
   - The Go migrator never writes to `storage.db`, so running
     `citeck migrate --dry-run` against the file is safe and can confirm
     whether the H2 store is parseable.
   - Output: a summary of workspaces, namespaces, and secret counts. If
     the migrator reports a parse error, the H2 file is damaged
     independently of any Go-side action.

The Citeck team treats "Go 2.x corrupted my Kotlin store" as a release
blocker. If reproducible, file an issue with the daemon log
(`log/daemon.log` if available), the migrator output, and — if possible —
a `storage.db` snapshot taken before the Go 2.x run.
