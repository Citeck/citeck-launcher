export interface ActionResultDto {
  success: boolean
  message: string
  // Set only by POST /daemon/shutdown?leave_running=true, and only when that
  // detach could not write the namespace state. The detach still happened (the
  // containers are running, hence success: true) but the record the NEXT daemon
  // adopts them with is stale. Nothing in the web UI issues that request — the
  // desktop wrapper and `citeck install` do — it is declared here because the
  // field is part of the shared result shape.
  stateSaveError?: string
}

export interface AppDto {
  name: string
  status: string
  statusText?: string
  image: string
  cpu: string
  memory: string
  /** 0..100; 0 when no memory limit is configured. */
  memoryPercent?: number
  /** True when memory usage >= 80% of the configured limit. */
  memoryWarning?: boolean
  /** True when memory usage >= 95% of the configured limit. */
  memoryCritical?: boolean
  /** True when the container hit its CPU quota in the latest stats sample. */
  cpuThrottled?: boolean
  kind: string
  ports?: string[]
  edited?: boolean
  locked?: boolean
  restartCount?: number
  editedFilesCount?: number
  /** 1-based init-container step currently running; only set while STARTING with an active init phase. */
  initStep?: number
  /** Total init containers; only set while the init phase is active. */
  initTotal?: number
  /** Short name of the running init step (init image basename without registry/tag). */
  initName?: string
  /**
   * Dependencies holding this app in DEPS_WAITING; absent for every other
   * status. Structure rather than a finished sentence: the daemon has no
   * reader and therefore no language, and both halves of the sentence are
   * already translated here — see lib/waitingForDeps.ts.
   */
  waitingFor?: WaitingDepDto[]
  /**
   * True when this app's DEPS_WAITING hold traces back — through however many
   * links — to a dependency the user DETACHED. Such an app is settled, not
   * pending: nothing will release it until that dependency is started again,
   * which is why the daemon reports such a namespace as STALLED — a problem
   * that will not resolve itself — rather than leaving it STARTING forever.
   * The decision is the daemon's; it needs the detach set and
   * a walk of the dependency graph, so readers take the flag rather than
   * re-deriving it from waitingFor.
   */
  held?: boolean
  // True when the operator switched this app OFF (detach). Attaching it back
  // is the one per-app lifecycle action the daemon carries out while the
  // namespace is stopped, so the row's start button stays live for it.
  detached?: boolean
}

/** One dependency an app is held on: its name, and the status it is in. */
export interface WaitingDepDto {
  app: string
  status: string
}

export interface AppFileDto {
  path: string
  edited?: boolean
}

export interface RestartEventDto {
  ts: string
  app: string
  reason: string
  detail: string
  diagnostics?: string
}

export interface LinkDto {
  name: string
  url: string
  icon?: string
  order: number
  category?: string
  categoryKey?: string
  description?: string
  descriptionKey?: string
  alwaysEnabled?: boolean
  // Custom workspace-config link: the daemon already computed `disabled` from
  // its dependsOn app status, so the UI must not re-gate it on namespace-wide
  // running state. Links whose deps are absent are omitted by the daemon.
  custom?: boolean
  disabled?: boolean
  dependsOn?: string[]
}

export interface NamespaceDto {
  id: string
  name: string
  status: string
  bundleRef: string
  bundleError?: string
  // Why the namespace's state is not reaching the store (a full disk, a
  // permission change, a damaged SQLite file), or absent when the last write
  // landed. Actions that produce such a write — detaching an app, saving an app
  // config, editing a mounted file — all SUCCEED on a refused write, because
  // the action itself succeeded; only the record of it was lost. Rendered by
  // StateWriteBanner. Self-healing: the daemon derives it from its live
  // failure streak, so the first write that lands clears it.
  stateWriteError?: string
  apps: AppDto[]
  links?: LinkDto[]
  // Host CPU core count from the daemon (runtime.NumCPU). Caps the aggregate
  // CPU progress bar at hostCpus*100% — Docker per-container stats span all
  // cores, so per-app 100% caps were wrong by a factor of N (N apps × 100
  // is unrelated to the host's actual capacity).
  hostCpus?: number
  // True while an Update & Start pass has been accepted but has not yet reached
  // the runtime (reloadMu wait, git pull, bundle resolve, file generation).
  // None of that changes `status` or app statuses, so this is the only signal
  // that the click is being acted on. Not a status — don't render it as one.
  updating?: boolean
  // Why the last Update & Start pass for this namespace did not happen (a git /
  // resolve / generate failure, or a refusal because it was mid-stop). Without
  // it a failed pass looks exactly like a successful one: the spinner stops and
  // nothing changed. `updateErrorAt` (epoch ms) identifies the occurrence so it
  // is shown once and not re-raised on remount or reconnect.
  updateError?: string
  updateErrorAt?: number
  // Infrastructure images (postgres, rabbitmq, …) the generator held back
  // because applying them to this namespace's existing data would be a
  // breaking change. Recomputed on every load and reload.
  dependencyUpgrades?: DependencyUpgradeDto[]
  // Set when this namespace's bundle repo has a version above the one it runs.
  // `requiresLauncher` present means we cannot switch to it yet — the next move
  // is updating the launcher, not opening the settings dialog.
  newerBundle?: { version: string; requiresLauncher?: string }
  // Set while a dependency migration runs for THIS namespace, so a client that
  // connects or reloads mid-way still sees it (the deps_migration_* events
  // only reach clients already listening).
  dependencyMigration?: DependencyMigrationDto
  // Why an interrupted migration's journal is still open with no migration
  // running (a crash whose restart-recovery rollback failed): the version is
  // frozen and every start of this namespace is refused until it succeeds.
  // Carried here because that recovery runs at load time — it emits no
  // deps_migration_* event and produces no result.
  dependencyRollbackPending?: string
}

/** One infrastructure dependency of the active namespace. */
export interface DependencyDto {
  id: string
  app: string
  /** What the DATA runs on (the pin), or the last generated image when unpinned. */
  currentImage: string
  currentVersion?: string
  /** What the user would move to: the held-back upgrade, else the candidate. */
  targetImage: string
  targetVersion?: string
  /** up-to-date | pending-minor | upgrade-available | upgrade-blocked |
   *  requires-launcher-update */
  status: string
  /** Explains a status a fixed label cannot: today, why a vendor-forbidden
   *  pair is blocked and which intermediate version to take. Empty for every
   *  other status, so it can be rendered unconditionally.
   *
   *  It arrives already translated into the locale this client sends on every
   *  request (X-Citeck-Locale): the daemon builds it as a locale key and
   *  renders it at the boundary, so the status LABEL and the sentence behind
   *  it are in the same language. Do NOT try to translate it here. */
  statusDetail?: string
  /** Whether THIS launcher ships a migration plan for the dependency at all. */
  migratable: boolean
  /** The "go back to what this dependency ran on before the last migration"
   *  offer; absent when there is nothing to go back to. */
  rollback?: DependencyRollbackDto
}

/** Offer to put one dependency back on the image AND the data-volume
 *  generation it ran on before its last completed migration.
 *
 *  Absent when there is nothing to go back to (nothing has migrated it, or it
 *  has already been rolled back — a rollback clears its own target, since
 *  there is no roll-forward). Present with `available: false` and a `problem`
 *  when the pin names a target the launcher cannot use, which is a state the
 *  launcher actively creates by telling the operator they may reclaim the
 *  retained volume. */
export interface DependencyRollbackDto {
  toImage: string
  toVersion?: string
  /** The RETAINED volume the rollback would run on — the one the migration
   *  copied from and never wrote to. */
  volume: string
  /** The volume the namespace runs on today. A rollback keeps it and never
   *  reads it again, so everything written since the migration becomes
   *  unreachable: that is the whole content of the confirmation. */
  frozenVolume: string
  /** When the migration being undone finished (epoch ms), 0 when the one
   *  result slot no longer holds it. */
  migratedAt?: number
  available: boolean
  /** Why an existing target cannot be used. Empty when `available`. */
  problem?: string
}

/** Live progress of the running migration. `percent` is the STEP's own
 *  sub-progress (0 = indeterminate), never an overall percentage. */
export interface DependencyMigrationDto {
  id: string
  step: string
  stepIndex: number
  stepCount: number
  percent?: number
  message?: string
  /** "" (absent) = a migration, "rollback" = a rollback. The two share this
   *  channel, so only the title differs. */
  kind?: string
  /** The running plan's ACTUAL step list, in order — including repeats. A
   *  multi-hop copy upgrade repeats ids (pre-upgrade/start-new/post-upgrade
   *  once per rung, and an intermediate rung's own stop shares "stop-new"
   *  with the plan's FINAL cleanup step), so a row is positioned by
   *  stepIndex/stepCount against THIS list, never by matching an id — only
   *  the daemon that built the plan knows which occurrence is which. Absent
   *  from an older daemon; the dialog falls back to its own hardcoded
   *  single-hop vocabulary then. */
  stepIds?: string[]
}

/** Verdict of the last migration, kept until the next one replaces it. */
export interface DependencyMigrationResultDto {
  id: string
  from: string
  to: string
  finishedAt: number
  success: boolean
  error?: string
  /** Volume the previous data was left in (success only) — never deleted. */
  oldVolume?: string
  /** "" (absent) = a migration, "rollback" = a rollback. They share the one
   *  result slot a namespace has. */
  kind?: string
}

export interface DependenciesDto {
  items: DependencyDto[]
  migration?: DependencyMigrationDto
  lastResult?: DependencyMigrationResultDto
  /** Non-empty when an interrupted migration's rollback has not succeeded:
   *  the launcher retries it at every start, the version is frozen meanwhile,
   *  and no new migration is accepted. */
  rollbackPending?: string
}

export interface DependencyUpgradeDto {
  id: string
  app: string
  from: string
  to: string
  /** This launcher can move THIS PAIR: it ships a plan for the dependency and
   *  the pair is one the plan accepts. */
  migratable: boolean
  /** Non-empty when the hop is refused by the DEPENDENCY'S OWN vendor — a
   *  refusal a newer launcher would not lift — carrying the sentence that says
   *  so, including the intermediate version to take first when one exists.
   *  It is the discriminator the upgrades banner splits on and is NOT derivable
   *  from `migratable`, which is false for a blocked pair too. */
  blocked?: string
  /** The held-back candidate is OLDER than what the data runs on: not an
   *  upgrade at all, so the upgrades banner leaves it out entirely. Not
   *  derivable from the two fields above — a backwards hold reports
   *  `migratable: false` with no `blocked`, which is the shape of "a newer
   *  launcher is needed". */
  bundleOlder?: boolean
}

export interface DependencyMigrateRequestDto {
  /** Confirms deleting a target volume that already exists (a leftover from an
   *  earlier attempt). Without it the migration refuses. */
  replaceExistingVolume: boolean
}

/** Result of GET …/dependencies/{id}/preflight (Go: api.PreflightResult).
 *  Problems block the migration; warnings need an explicit confirmation. */
export interface PreflightResult {
  ok: boolean
  /** Arrays on the wire, `| null` here by the dto_drift_test convention: a Go
   *  slice marshals as null when it is nil, and this pair has been nil on the
   *  happy path. Read them as `?? []`. */
  problems: string[] | null
  warnings: string[] | null
  from: string
  to: string
  dataSizeBytes: number
  requiredHostBytes: number
  requiredVolumeBytes: number
  /** Free space where the dump is written (the host). */
  freeHostBytes: number
  /** Free space where the data volumes live — the Docker VM's disk on a
   *  macOS/Windows desktop, which is NOT the host's. */
  freeVolumeBytes: number
  /** The dump and the data volumes are on ONE filesystem (the ordinary server
   *  layout). The two coexist until the migration commits, so what has to fit
   *  there is requiredTotalBytes, not either half alone. */
  sharedFilesystem: boolean
  /** What that one filesystem must have free: the two halves added up. 0 when
   *  sharedFilesystem is false, where a sum across two disks means nothing —
   *  read sharedFilesystem, never the zero. */
  requiredTotalBytes: number
  existingTargetVolume?: ExistingVolume
  wasRunning: boolean
  /** The space checks actually ran. It replaces the old
   *  `requiredHostBytes > 0` discriminator, which stopped being true the
   *  moment a plan appeared that writes no host file at all: a copy upgrade
   *  legitimately needs zero bytes on the host, and rendering a refused
   *  preflight's zeros verbatim reads as a namespace with no data and a full
   *  disk, printed above the real reason. */
  spaceChecked: boolean
}

/** A target volume that is already there — size and version are what let the
 *  user tell a leftover apart from somebody else's data. */
export interface ExistingVolume {
  name: string
  sizeBytes: number
  /** What the data itself says it is: PostgreSQL's PG_VERSION content, or
   *  "empty" when the volume holds no such file, or "" for a dependency whose
   *  data carries no version marker at all (RabbitMQ, ZooKeeper). "" is NOT
   *  "empty" — such a volume can be full of data — and a renderer must tell
   *  the three apart rather than print a version the data never claimed. */
  version: string
}

export interface HealthDto {
  status: string   // "healthy" | "degraded" | "unhealthy"
  healthy: boolean
  checks: HealthCheckDto[]
}

export interface HealthCheckDto {
  name: string
  status: string
  message: string
}

export interface DaemonStatusDto {
  running: boolean
  pid: number
  uptime: number
  version: string
  workspace: string
  socketPath: string
  desktop: boolean
  locale?: string
  theme?: string
}

export interface EventDto {
  type: string
  seq: number
  timestamp: number
  namespaceId: string
  appName: string
  before: string
  after: string
  /** Pull progress 0..100. Only present on `pull_progress` events. */
  percent?: number
  /** Human-readable progress phase ("Pulling: 234mb 50%"). Only present on `pull_progress`. */
  phase?: string
  /** 1-based progress index. Present on `snapshot_progress` (volume) and `app_init_step` (init step; absent = phase done). */
  current?: number
  /** Progress total. Present on `snapshot_progress` (volumes) and `app_init_step` (init containers). */
  total?: number
  /** Monitored filesystem path. Present on `disk_low` / `disk_ok` only. */
  path?: string
  /** Free bytes on the monitored filesystem. Present on `disk_low` / `disk_ok` only. */
  freeBytes?: number
  /** Low-disk threshold in bytes. Present on `disk_low` / `disk_ok` only. */
  thresholdBytes?: number
  /** The running migration's real step list — see
   *  DependencyMigrationDto.stepIds. Present on `deps_migration_start` /
   *  `deps_migration_progress` once the plan exists. */
  stepIds?: string[]
}

export interface AppInspectDto {
  name: string
  containerId: string
  image: string
  status: string
  state: string
  ports: string[] | null
  volumes: string[] | null
  env: string[] | null
  labels: Record<string, string> | null
  network: string
  restartCount: number
  startedAt: string
  uptime: number
}

export interface AppImageDto {
  ref: string
  present: boolean
  pulling?: boolean
  pullError?: string
  id?: string
  repoDigests?: string[]
  size?: number
  os?: string
  architecture?: string
  created?: string
}

// Phase E1: Welcome Screen
export interface NamespaceSummaryDto {
  id: string
  workspaceId: string
  name: string
  status: string
  bundleRef: string
}

export interface QuickStartDto {
  name: string
  template: string
  snapshot?: string
  // Resolved bundle ref ("repo:key") — Kotlin showed this as the QS button
  // subtitle. Falls back to template when the daemon couldn't resolve it.
  bundleRef?: string
}

// Phase E3: Namespace creation
export interface NamespaceCreateDto {
  name: string
  authType: string
  users?: string[]
  host: string
  port: number
  tlsEnabled: boolean
  tlsMode?: string
  pgAdminEnabled: boolean
  bundleRepo: string
  bundleKey: string
  workspaceId?: string
  snapshot?: string
  template?: string
  masterPassword?: string
  useDefaultPassword?: boolean
}

export interface BundleInfoDto {
  repo: string
  versions: string[]
}

// Phase F1: Secrets
export interface SecretMetaDto {
  id: string
  name: string
  type: string
  scope: string
  // The registry/git host this secret authenticates against; the host-filtered
  // picker uses it so a credential is reused per host. Optional — older daemons
  // and host-agnostic secrets omit it.
  host?: string
  createdAt: string
  // For BASIC_AUTH / REGISTRY_AUTH; surfaced so the write-only edit form can
  // prefill it. Optional — older daemons omit it.
  username?: string
}

export interface SecretCreateDto {
  id: string
  name: string
  type: string
  // For BASIC_AUTH / REGISTRY_AUTH only; mirrors Kotlin AuthSecret.Basic so
  // passwords containing ':' round-trip untouched.
  username?: string
  value: string
  scope?: string
  host?: string
}

// Write-only partial update (PUT /secrets/{id}). Empty/absent field = keep
// the existing value — in particular an empty `value` keeps the old secret
// value, so the edit form never needs to (and never does) display it.
export interface SecretUpdateDto {
  name?: string
  scope?: string
  username?: string
  value?: string
  host?: string
}

// Maps an image-registry host to a stored REGISTRY_AUTH secret for the active
// workspace (POST /registry-bindings). An empty secretId removes the binding.
export interface RegistryBindingDto {
  host: string
  secretId: string
}

// Phase F2: Diagnostics
export type DiagnosticsStatus = 'ok' | 'warn' | 'warning' | 'error'

export interface DiagnosticCheckDto {
  name: string
  status: DiagnosticsStatus
  message: string
  fixable: boolean
}

export interface DiagnosticsDto {
  checks: DiagnosticCheckDto[]
}

export interface DiagFixResultDto {
  fixed: number
  failed: number
  message: string
}

// Phase F3: Snapshots
export interface SnapshotDto {
  name: string
  createdAt: string
  size: number
}

// Multi-workspace (desktop-only). Endpoints return 404 in server mode.
//
// `repoPullPeriod` is an ISO 8601 duration string (e.g. "PT2H" = 2 hours).
// `authType` is "NONE" (default) or "TOKEN". With TOKEN the daemon resolves
// the repo token from `secretId` (a reusable GIT_TOKEN secret shared across
// workspaces) and falls back to the legacy per-workspace "ws:{id}:repo"
// secret when no secretId is linked. Optional fields on create/update get
// Kotlin-parity defaults when omitted.
export interface WorkspaceDto {
  id: string
  name: string
  repoUrl: string
  repoBranch: string
  repoPullPeriod?: string
  authType?: string
  /** Id of the linked GIT_TOKEN secret ('' / absent = none, legacy lookup). */
  secretId?: string
  active: boolean
  namespaces: number
}

export interface WorkspaceCreateDto {
  id?: string
  name: string
  repoUrl: string
  repoBranch?: string
  repoPullPeriod?: string
  authType?: string
  secretId?: string
}

// Partial-update semantics: absent field = unchanged. `secretId` keeps that
// rule and adds an explicit unlink sentinel: '' = unlink the secret.
export interface WorkspaceUpdateDto {
  name?: string
  repoUrl?: string
  repoBranch?: string
  repoPullPeriod?: string
  authType?: string
  secretId?: string
}

export interface UpdateStatusDto {
  currentVersion: string
  latestVersion?: string
  available: boolean
  lastCheckAt?: string
  error?: string
  applyError?: string
  applying: boolean
  /**
   * The last staging attempt hit a signature classification (the release has
   * no .sig, or it does not verify under the key this binary embeds — e.g.
   * after a signing-key rotation). Auto-install would keep failing; the UI
   * offers a calm manual-download path via releasesUrl instead.
   */
  manualUpdateRequired?: boolean
  manualUpdateReason?: 'signature_missing' | 'signature_mismatch' | string
  releasesUrl?: string
}

export interface ReleaseNoteDto {
  version: string
  date: string
  markdown: string
}

export interface AppConfigDto {
  content: string
  baseline: string
}

export interface AppFileContentDto {
  content: string
  baseline: string
}

export interface WorkspaceConfigDto {
  content: string
  baseline: string
}
