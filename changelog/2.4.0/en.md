## Highlights

- **Kotlin 1.x → Go 2.x migration is now pure Go.** The embedded
  `h2-export.jar` (and the `tools/h2-export/` Gradle subproject + the JRE
  download path that fed it) is gone — `internal/h2migrate/mvstore.go`
  reads `storage.db` directly: chunk header → layout map → meta map →
  user-data maps, stripping H2 `TransactionStore` `VersionedValue`
  wrappers (`varLong(operationId) || value`) on user maps and
  decompressing LZF pages. Fixed four correlated bugs in the previous
  pure-Go fallback (chunk header field semantics, meta-map lookup via
  layout, data-tree root lookup via layout, LZF extended back-ref byte
  order) that previously caused silent zero-data returns on real H2
  files. Desktop binary shrinks ~1.1 MB.
- Migration now preserves every map Kotlin writes: workspaces,
  namespaces (10 `NamespaceConfig` fields), encrypted secrets blob,
  launcher state, per-namespace runtime state (`manualStoppedApps`,
  `editedApps` with `ApplicationDef` Kotlin→Go translator,
  `editedAndLockedApps`, `changedRuntimeFiles`, `cachedBundle` with
  `BundleDef` translator), workspace state for **all** workspaces (not
  just the active one), and git-repo `lastSync`/commit hash.
- One-shot atomic `storage.db → storage.db.kotlin-bak` backup runs
  before the first migration; `storage.db` itself stays byte-identical
  (read-only contract verified against the developer's real
  `~/.citeck/launcher/storage.db`).
- Filesystem-fallback safety net (used when the H2 reader cannot open
  the file at all) now emits Kotlin-parity default authentication
  (`BASIC` + `admin/fet`) instead of a stub config.

## SQLite schema

- v3: `Secret.Username` column (BASIC_AUTH / REGISTRY_AUTH split — no
  more `username:password` packing; PAT-style colons in `GIT_TOKEN`
  values no longer truncate).
- v4: per-workspace `selected_ns` JSON map (folds the legacy global
  `namespace_id` row); switching desktop workspace restores the
  previous selection instead of clearing.
- v5: `git_repo_state` table; preserves `lastSync` / commit hash across
  daemon restarts.

## Multi-workspace (desktop mode)

- Bundle resolver honours per-workspace `RepoURL` / `Branch` /
  `PullPeriod` / `AuthType` instead of the hardcoded default bundles
  repo. Secondary workspaces with custom git remotes are now reachable
  from normal startup / wizard flows (not only from "Force Update").
- `LauncherState.NamespaceID` becomes `SelectedNs map[wsID]nsID`;
  switching workspaces preserves the per-workspace last namespace.
- Second desktop instance dials the existing daemon's
  `/desktop/focus` Unix-socket route and exits 0; the active window
  comes forward on Linux / Windows instead of erroring out on the
  single-instance lock.

## Parity gaps closed

- Desktop-mode plain volume sources are scoped to
  `citeck_volume_{orig}_{ns}_{ws}` with `LabelOrigName` / `Namespace` /
  `Workspace` labels (case-insensitive workspace label lookup),
  matching the Kotlin contract. The Volumes UI in desktop mode now
  lists via Docker volume API + `/system/df`; delete via `VolumeRemove`.
- License canonical date serializer emits `YYYY-MM-DD` for midnight UTC
  and full RFC3339 otherwise; round-trip accepts both. Kotlin-signed
  licenses with midnight-UTC dates verify cleanly under Go.
- `Secret.Username` field for BASIC_AUTH / REGISTRY_AUTH; legacy
  on-disk and SQLite rows auto-split on load / migration; passwords
  containing `:` no longer truncated.
- Detached Alfresco excluded from `proxyTarget` calculation.
- 3-retry fallback to a locally-present image when pull fails
  (matches Kotlin `RETRIES_COUNT_FOR_EXISTING_IMAGE`).
- `exec ` prefix on shell-form init actions for PID / signal parity
  with Kotlin's `AppStartAction`.
- CloudConfig response now flattens nested maps depth-first with
  `.`-joined keys and `[idx]` bracket notation for lists (Kotlin
  `buildFlattenedMap` parity).
- Volume content hash recomputes on user file edits via
  `GenerateOpts.EditedFileOverlay`; `SetFileEdited` enqueues a
  regenerate so edits take effect without manual reload.
- `goroutine-dump.txt` added to `system-dump` ZIP; tray "Dump System
  Info" has an in-flight guard, opens the folder, and shows a result
  dialog.
- Dead `AppInitAction.Trigger` field removed.

## UI

- Unified `ErrorModal` + `showError()` store — 5 call sites migrated
  off ad-hoc error UI.
- `WorkspaceSelector` component (dropdown + create / edit / delete
  dialog) on Welcome; auto-collapses to nothing in server mode (the
  `/api/v1/workspaces` route returns 404 when `requireDesktop` is in
  effect).
- Welcome namespace context menu gained an "Edit" entry.
- `SnapshotsDialog` gained an "Open NS Directory" button.

## Removed

- `tools/h2-export/` Gradle subproject (Java exporter that produced
  the embedded JAR).
- `internal/h2migrate/embedded/h2-export.jar` (~1 MB) and the
  surrounding `embed_desktop.go` / `embed_server.go` shims.
- `internal/h2migrate/jarmigrate.go` + tests (~850 LOC) and all
  JRE-download / java-binary discovery logic.

## Desktop installers

- Native desktop installers for **Windows, macOS, and Linux**, built in CI on each
  `v*.*.*` tag and attached to the GitHub Release: `.deb` + `.rpm` (amd64, arm64),
  `.msi` (amd64, arm64), and `.dmg` (Intel + Apple Silicon). Artifacts are named
  `citeck-desktop_<version>_<os>_<arch>.<ext>` with `.sha256` sidecars; the server
  tarballs and `install.sh` ship in the same release.
- **Clean upgrade over the legacy 1.x desktop app — no leftover executables.** The
  `.deb`/`.rpm` reuse the package name `citeck-launcher` (dpkg/dnf in-place replace),
  the Windows `.msi` reuses 1.x's `UpgradeCode` (major upgrade auto-removes 1.x), and
  the macOS `.app` keeps the `citeck-launcher.app` bundle name (Finder replaces in
  place). User data is never touched — it is preserved for the 1.x → 2.x migration.
- Desktop build output renamed to `citeck-launcher`. Packaging via nfpm (deb/rpm),
  WiX (msi), and `.app` + `hdiutil` (dmg). Code-signing / notarization steps are
  scaffolded in the workflow but disabled until signing secrets are configured.
- Windows desktop builds are CGO-free (added a Windows `availableDiskSpace` via
  `GetDiskFreeSpaceExW`); the release matrix covers Windows amd64 + arm64.
