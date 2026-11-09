# Offline recovery of a saved file edit

Launcher refuses unsafe runtime files and refuses to replace a saved edit with a
template when the edit cannot be replayed safely. This also applies during boot:
a legacy edit with no generated baseline, an unavailable conflict fallback, or a
failed materialization can prevent the selected namespace and daemon from loading.
A normal `citeck edit <app> --file <path> --reset` needs a running daemon.

## Reset one saved edit while the daemon is stopped

1. Stop the daemon **and quit the desktop wrapper** (otherwise its supervisor
   may restart the daemon). Stop/disable any service restart loop while repairing.
   Containers need not be stopped; this command does not contact Docker, start a
   second daemon, change detach intent, or start/stop any application.
2. Use this installation's exact `CITECK_HOME` and `CITECK_RUN`. For a desktop
   profile also specify `--desktop`, the workspace ID and namespace ID. Do not
   point the command at another user's installation.
3. Copy the runtime key from the error, without `./`, and preview:

   ```sh
   # Server (boot's fixed storage key is default, regardless of YAML id):
   citeck recover-file app/emodel/props/application-launcher.yml --namespace default

   # Desktop (run the server/CLI binary with access to the desktop profile):
   citeck recover-file app/emodel/props/application-launcher.yml \
     --desktop --workspace default --namespace my-namespace
   ```

4. Repeat with `--apply` to explicitly confirm resetting **only that saved file
   edit**. `--format json` returns `workspaceId`, `namespaceId`, `file`, `applied`
   and, after applying, `backup`.
5. Restart Launcher when ready. The next successful generation writes the current
   bundle's template for this file. A key no longer generated remains absent; the
   operator's old content is in the backup, not silently discarded.

The command requires an existing profile and namespace state and a saved modern
or legacy edit for that exact key. It never creates a new database or migrates a
schema, never discards SQLite WAL files as a repair (normal SQLite close may
checkpoint WAL), and refuses pending snapshot/dependency
recovery. Other edits, unknown JSON fields, dependency pins, retained generations,
detached apps and namespace configuration are preserved.

A lifetime profile lock excludes a current daemon or another recovery. Reserving
the usual daemon socket also excludes older daemons. An existing socket causes a
refusal, even if stale: first establish that no process is listening and no
supervisor can start one before removing a stale socket. Never change `CITECK_RUN`
just to bypass this refusal. Old daemons started with a different run directory
cannot participate in the new profile lock: ensure they are stopped too.

## Backup and failure semantics

Before changing state, the command creates a private directory under
`<home>/.file-recovery/`, **outside runtime bind mounts**. `state.json` holds the
complete original namespace state. `manifest.json` identifies the exact storage
target, key and original-entry location. Regular file bytes are also copied to
`original` in this private directory, without buffering the whole file in memory.

The original entry is first atomically renamed to a single
`.citeck-quarantine-<random>` leaf in its pinned parent. No intermediate directory
name is re-resolved for this rename. Its original mode/ACL is preserved, and the
manifest records its location before moving it. A symlink/FIFO stays as that
quarantined entry: its target is never followed or read. Only this already-visible
entry remains in the runtime mount, **never the full state or other apps' edits**.
Directories and unsafe parent paths are refused, never recursively removed.

Backup files are synced before the state write. POSIX directory entries are
synced too; Windows does not provide this POSIX directory-fsync guarantee here.
This is not a cross-file transactional filesystem snapshot or a promise of
power-loss atomicity on every filesystem. Do not remove the backup/quarantine
until the recovery is verified.

Backups can contain passwords and personal data. Keep the directory private; do
not add it to Git or attach it to tickets without redaction. It is deliberately
outside the diagnostic ZIP's collected sources. Keep it until the recovery is
verified, then remove it and the manifest's quarantined original explicitly. No automatic backup pruning deletes the
only copy of the original edit.

If saving state fails, the command attempts to restore the original entry and
retains the state backup. If restoration also fails, **do not restart Launcher**
until the entry/state have been reconciled with the backup. A successful reset
is not proof that a currently running container has applied different content;
observe the next normal Launcher start/reload.

## Filesystem failures without a saved edit

`recover-file` is intentionally not a generic filesystem-delete tool. For an
unmodified generated file with wrong ownership, a read-only filesystem, unsafe
parent, or a directory in the way, repair the filesystem condition while Launcher
is stopped. Preserve any operator content first. Do not weaken no-follow checks
or silently run definitions whose generated files could not be written. Retry
normal startup after correcting the precise path named in the error.
