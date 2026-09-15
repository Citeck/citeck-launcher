package api

import "github.com/citeck/citeck-launcher/internal/msg"

// ActionResultDto is the response for simple action endpoints.
type ActionResultDto struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	// StateSaveError is set only by POST /daemon/shutdown?leave_running=true,
	// and only when that detach could not write the namespace state.
	//
	// The detach still happened — the containers are running, which is why
	// Success stays true — but the record the NEXT daemon adopts them with is
	// the one from the last successful write: detached apps re-attach, per-app
	// config and mounted-file edits are gone, dependency pins revert. After the
	// detach there is no runtime loop left to retry the write, so this response
	// is the only chance the caller has to learn about it, and it is what lets
	// `citeck install` refuse to layer a version change on top of a lost state.
	StateSaveError string `json:"stateSaveError,omitempty"`
}

// AppDto represents an application in the namespace.
type AppDto struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	StatusText string `json:"statusText,omitempty"`
	Image      string `json:"image"`
	CPU        string `json:"cpu"`
	Memory     string `json:"memory"`
	// MemoryPercent is the container's memory usage as a percentage of its
	// configured limit (0..100). Zero when no memory limit is set.
	MemoryPercent float64 `json:"memoryPercent,omitempty"`
	// MemoryWarning is true when memory usage is at or above 80% of the
	// configured limit. Surfaces a "high memory usage" tooltip in the UI.
	MemoryWarning bool `json:"memoryWarning,omitempty"`
	// MemoryCritical is true when memory usage is at or above 95% of the
	// configured limit. Surfaces a "near OOM limit" warning in the UI.
	MemoryCritical bool `json:"memoryCritical,omitempty"`
	// CPUThrottled is true when the container hit its CPU quota in the
	// most recent stats sample (Kotlin parity).
	CPUThrottled     bool     `json:"cpuThrottled,omitempty"`
	Kind             string   `json:"kind"`
	Ports            []string `json:"ports,omitempty"`
	Edited           bool     `json:"edited,omitempty"`
	Locked           bool     `json:"locked,omitempty"`
	RestartCount     int      `json:"restartCount,omitempty"`
	EditedFilesCount int      `json:"editedFilesCount,omitempty"`
	// InitStep is the 1-based index of the init container currently running
	// while the app is STARTING (0 / absent outside the init phase).
	InitStep int `json:"initStep,omitempty"`
	// InitTotal is the app's init container count, set only while the init
	// phase is active so the UI can render "init {step}/{total}".
	InitTotal int `json:"initTotal,omitempty"`
	// InitName is a short human-readable name of the running init step,
	// derived from the init container image's last path segment.
	InitName string `json:"initName,omitempty"`
	// WaitingFor names the dependencies holding this app in DEPS_WAITING, and
	// the status each of them is in. Empty for every other status.
	WaitingFor []WaitingDepDto `json:"waitingFor,omitempty"`
	// Held marks a DEPS_WAITING app whose hold traces back, through however
	// many links, to a dependency the user DETACHED. Nothing in the namespace
	// will release it until the operator starts that dependency again, so a
	// client's wait loop must treat it as terminal — and the namespace itself
	// reports STALLED while any app is in this state.
	//
	// It is a DECISION on the wire rather than something a client re-derives
	// from WaitingFor: the answer needs manualStoppedApps (a detached app and
	// an app that is merely STOPPED are not the same thing) and a walk of the
	// dependency graph, and a second implementation of that in every reader
	// would be a second chance to get it subtly wrong.
	Held bool `json:"held,omitempty"`
}

// WaitingDepDto is ONE dependency an app is held on: its name, and the app
// status it is currently in.
//
// It travels as STRUCTURE, not as the finished "Waiting for: qdrant (Stopped)"
// sentence, because both halves of that sentence are already translated in the
// reader's own asset: the web UI owns `app.status.waitingForDeps` and the
// `status.*` labels it renders on every badge (web/src/locales/*.ts). Rendering
// it in the daemon would either answer the UI in daemon.yml's language, or
// force a second copy of all thirteen status labels into the CLI's JSON asset
// to say what the UI already says. See internal/msg for the shape the daemon
// uses when the sentence is NOT one the reader can already build — there the
// key is the daemon's and only the locale is the reader's.
type WaitingDepDto struct {
	App    string `json:"app"`
	Status string `json:"status"`
}

// AppFileDto describes a single bind-mounted file exposed via the per-app
// file API. Edited=true means the user has modified the file via the Web UI
// — the launcher preserves those edits across reload/regenerate.
type AppFileDto struct {
	Path   string `json:"path"`
	Edited bool   `json:"edited,omitempty"`
}

// AppConfigDto carries the effective app YAML plus the generated baseline so the
// editor can render a per-line change gutter (desktop diff/overlay feature).
type AppConfigDto struct {
	Content  string `json:"content"`
	Baseline string `json:"baseline"`
}

// WorkspaceConfigDto carries the effective workspace-v1.yml plus the pristine
// git baseline so the workspace-config editor can render a per-line change
// gutter — the workspace-scoped counterpart of AppConfigDto.
type WorkspaceConfigDto struct {
	Content  string `json:"content"`
	Baseline string `json:"baseline"`
}

// AppFileContentDto is the file equivalent of AppConfigDto.
type AppFileContentDto struct {
	Content  string `json:"content"`
	Baseline string `json:"baseline"`
}

// RestartEventDto represents a restart event for the API.
type RestartEventDto struct {
	Timestamp   string `json:"ts"`
	App         string `json:"app"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
	Diagnostics string `json:"diagnostics,omitempty"`
}

// DaemonStatusDto reports the daemon's runtime status.
type DaemonStatusDto struct {
	Running    bool   `json:"running"`
	PID        int64  `json:"pid"`
	Uptime     int64  `json:"uptime"`
	Version    string `json:"version"`
	Workspace  string `json:"workspace"`
	SocketPath string `json:"socketPath"`
	Desktop    bool   `json:"desktop"`
	Locale     string `json:"locale,omitempty"`
	Theme      string `json:"theme,omitempty"` // "dark" | "light" — persisted UI theme
}

// LicenseStatusDto summarizes the effective enterprise license for status
// surfaces (the `citeck status` license line and the dashboard indicator).
// Served by GET /api/v1/licenses/status.
//
// Tenant == "" means no license records exist at all (community install).
// Enterprise == false with a non-empty Tenant means license records exist
// but none currently validates (expired / not yet valid / bad signature) —
// the UI renders that as "expired" with real tenant context.
type LicenseStatusDto struct {
	Enterprise   bool   `json:"enterprise"`
	Tenant       string `json:"tenant,omitempty"`
	IssuedTo     string `json:"issuedTo,omitempty"`
	ValidUntil   string `json:"validUntil,omitempty"` // ISO-8601; date-only for midnight-UTC values
	DaysLeft     int    `json:"daysLeft"`             // whole days until ValidUntil (ceil); <= 0 once expired
	ExpiringSoon bool   `json:"expiringSoon"`         // valid but expiring within 14 days
}

// UIPrefsDto is the body of PUT /ui-prefs: user UI preferences persisted
// server-side so a desktop webview localStorage wipe (e.g. after an update)
// doesn't reset them. Empty fields are left unchanged.
type UIPrefsDto struct {
	Theme  string `json:"theme,omitempty"`  // "dark" | "light"
	Locale string `json:"locale,omitempty"` // en, ru, zh, es, de, fr, pt, ja
}

// NamespaceDto represents a namespace with its apps and links.
type NamespaceDto struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	BundleRef   string    `json:"bundleRef"`
	BundleError string    `json:"bundleError,omitempty"`
	Apps        []AppDto  `json:"apps"`
	Links       []LinkDto `json:"links,omitempty"`
	// HostCPUs is the number of CPU cores visible to the daemon process,
	// straight from runtime.NumCPU(). The UI uses it to cap the aggregate
	// CPU progress bar at (HostCPUs * 100)% — Docker per-container stats
	// already span all cores (a container fully using N cores reads as
	// N*100%), so the host total is the only meaningful aggregate ceiling.
	HostCPUs int `json:"hostCpus,omitempty"`
	// Updating is true while an "Update And Start" pass is accepted but has not
	// yet reached the runtime — i.e. during the reloadMu wait, the git pull, the
	// bundle resolve and the runtime-file generation. None of that touches app
	// state, so Status/Apps still read STOPPED (or RUNNING) throughout and the
	// click would otherwise look like it went nowhere for as long as the pull
	// takes. It is NOT a namespace status: the state machine owns those, and
	// faking STARTING here would lie about what the runtime is doing.
	Updating bool `json:"updating,omitempty"`
	// UpdateError carries why the LAST "Update And Start" pass for this namespace
	// did not happen — a doReloadEx failure (git, bundle resolve, generate) or a
	// refusal (the namespace was mid-stop). Without it a failed pass is
	// byte-identical to a successful one from the UI's side: the spinner stops
	// and nothing changed, which is the very "my click went nowhere" the
	// Updating flag exists to prevent. Cleared when the next pass is accepted.
	//
	// UpdateErrorAt (epoch ms) identifies the occurrence so a client can show
	// each failure exactly once and not re-show it on remount or reconnect.
	UpdateError   string `json:"updateError,omitempty"`
	UpdateErrorAt int64  `json:"updateErrorAt,omitempty"`
	// DependencyUpgrades lists the infrastructure images (see internal/deps)
	// the generator held back because applying them to THIS namespace's
	// existing data would be a breaking change. Recomputed by every load and
	// reload; empty on a namespace with no data yet, since the first
	// generation simply adopts the bundle's versions.
	DependencyUpgrades []DependencyUpgradeDto `json:"dependencyUpgrades,omitempty"`
	// DependencyMigration is set while a dependency migration runs for THIS
	// namespace, so a client that connects or reloads mid-way still sees it
	// (the deps_migration_* events only reach clients already listening). It
	// is scoped to the migrating namespace exactly like Updating: the daemon
	// runs one migration at a time, and a namespace switch must not show it
	// on a namespace it has nothing to do with.
	DependencyMigration *DependencyMigrationDto `json:"dependencyMigration,omitempty"`
	// DependencyRollbackPending is why an interrupted migration's journal is
	// still open with NO migration running: the daemon died mid-migration and
	// the rollback its next start attempted did not succeed, so the leftovers
	// it describes are still on the host, the dependency's version is frozen
	// and every start of this namespace is refused.
	//
	// It rides on the ordinary namespace fetch because that recovery happens
	// at LOAD time: it emits no deps_migration_* event and produces no result,
	// so a client that was not already looking at the dependencies dialog had
	// nothing to learn it from. Same wording and same rule as the dependencies
	// route's RollbackPending (empty while a migration IS running, which
	// DependencyMigration above already describes), and scoped to this
	// namespace by construction — the journal belongs to its runtime.
	DependencyRollbackPending string `json:"dependencyRollbackPending,omitempty"`
	// StateWriteError is why this namespace's state is not reaching the store
	// (a full disk, a permission change, a damaged SQLite file), or "" when
	// the last write landed.
	//
	// It is a STATE, not a per-call result, because the mutators it covers —
	// `citeck stop <app>`, `citeck start <app>`, `citeck edit <app>`, the gear
	// editor, a mounted-file edit — all did what they were asked: the
	// container really stopped, the patch really applied to it. Only the
	// RECORD of that was refused, so failing the action would be a lie, and a
	// silent success is how the operator ends up learning about it at the next
	// daemon start, where the app is un-detached again and the edit is gone.
	// One namespace-level signal also covers the case no per-call result can:
	// a write refused by the runtime loop's tail, long after the response went
	// out.
	//
	// Self-healing: it is derived from the persist failure STREAK the tail
	// retry already keeps (internal/namespace/persist_retry.go), so the first
	// write that lands clears it with no action from anyone.
	StateWriteError string `json:"stateWriteError,omitempty"`
}

// Dependency status values carried by DependencyDto.Status.
const (
	// DependencyUpToDate: the generator emits what the data already runs on.
	DependencyUpToDate = "up-to-date"
	// DependencyPendingMinor: a non-breaking bump the next start applies on
	// its own (the pin follows the container once it is RUNNING).
	DependencyPendingMinor = "pending-minor"
	// DependencyUpgradeAvailable: breaking, and this launcher can migrate it.
	DependencyUpgradeAvailable = "upgrade-available"
	// DependencyRequiresLauncherUpdate: breaking, and this launcher has no
	// migration for it — the pin holds the old image until one ships.
	DependencyRequiresLauncherUpdate = "requires-launcher-update"
	// DependencyUpgradeBlocked: breaking, this launcher HAS a migration for
	// the dependency, and the PAIR itself is refused — by the dependency's own
	// vendor (RabbitMQ 4.1 → 4.3 in one step), or by data too old to move
	// (ZooKeeper below 3.5).
	//
	// It gets its own value rather than borrowing requires-launcher-update's
	// because that one's whole meaning is "a newer launcher will fix this",
	// which here is a lie: nothing the launcher ships would help, and the
	// operator has an actual next step. StatusDetail says which and what to do.
	DependencyUpgradeBlocked = "upgrade-blocked"
	// DependencyBundleOlder: the bundle offers a version OLDER than the one
	// this namespace's data runs on, across a data format the older version
	// cannot read (a same-format backwards move — a reverted patch bump — is
	// not held back at all and never reaches this status).
	//
	// It is NOT an upgrade that is held back: there is nothing to migrate and
	// nothing to wait for, so it must borrow neither upgrade-available's words
	// nor upgrade-blocked's nor requires-launcher-update's — no launcher will
	// ever move data backwards, and `citeck deps upgrade` refuses it. The
	// launcher simply keeps the data where it is and says so; StatusDetail
	// carries the sentence, and names the rollback when this namespace is the
	// one that migrated away from exactly that version.
	DependencyBundleOlder = "bundle-older"
)

// DependencyDto is one infrastructure dependency of the active namespace.
type DependencyDto struct {
	ID  string `json:"id"`  // internal/deps id ("postgres")
	App string `json:"app"` // generated container name ("postgres")
	// CurrentImage is what the DATA runs on (the pin), falling back to the
	// image the last generation emitted when nothing is pinned yet.
	CurrentImage   string `json:"currentImage"`
	CurrentVersion string `json:"currentVersion,omitempty"`
	// TargetImage is what the user would move to: the held-back upgrade when
	// there is one, otherwise the candidate the generator emitted.
	TargetImage   string `json:"targetImage"`
	TargetVersion string `json:"targetVersion,omitempty"`
	Status        string `json:"status"`
	// StatusDetail explains a status a fixed label cannot: today, why a
	// vendor-forbidden pair is blocked and which intermediate version to take.
	// Empty for every other status, so a renderer may print it unconditionally.
	//
	// It arrives RENDERED, in the language the request asked for: the builders
	// in internal/deps/migrate answer a msg.Message and the daemon renders it
	// at the boundary, so the short status LABEL and the sentence behind it are
	// now in the same language. (They were not: the label was a locale key and
	// this was English in all eight.)
	StatusDetail string `json:"statusDetail,omitempty"`
	// Migratable reports whether this LAUNCHER has a migration plan for the
	// dependency at all — independent of whether one is pending.
	Migratable bool `json:"migratable"`
	// Rollback is the "go back to what this dependency ran on before the last
	// migration" offer, absent when there is nothing to go back to.
	Rollback *DependencyRollbackDto `json:"rollback,omitempty"`
}

// DependencyRollbackDto is the offer to put one dependency back on the image
// AND the data-volume generation it ran on before its last completed
// migration.
//
// Absent when there is nothing to go back to (no migration has completed, or
// the last one was already rolled back — a rollback clears its own target,
// because there is no roll-forward). Present with Available=false and a
// Problem when the pin names a target the launcher cannot use: the retained
// volume is gone, which is a state the launcher actively creates by telling
// the operator they may reclaim it.
//
// It is deliberately not derivable from the migration RESULT: a namespace has
// one result slot, so migrating a second dependency would erase the first
// one's offer while its retained volume was still on disk. The offer lives on
// the pin; only MigratedAt is read from the result.
type DependencyRollbackDto struct {
	ToImage   string `json:"toImage"`
	ToVersion string `json:"toVersion,omitempty"`
	// Volume is the RETAINED volume the rollback would run on — the one the
	// migration copied from and never wrote to.
	Volume string `json:"volume"`
	// FrozenVolume is the volume the namespace runs on TODAY. A rollback keeps
	// it and never reads it again, so everything written since the migration
	// becomes unreachable: that is the whole content of the confirmation.
	FrozenVolume string `json:"frozenVolume"`
	// MigratedAt is when the migration being undone finished (epoch ms), or 0
	// when the result slot no longer holds it — the pin carries the target and
	// the result carries the date, so a replaced result costs the sentence its
	// timestamp and nothing else.
	MigratedAt int64 `json:"migratedAt,omitempty"`
	Available  bool  `json:"available"`
	// Problem says why an existing target cannot be used. Empty when
	// Available. It is the RENDERED sentence; ProblemMsg beside it is the same
	// sentence as data.
	Problem string `json:"problem,omitempty"`
	// ProblemMsg is Problem before the locale is applied, and it never reaches
	// the wire. It exists because one caller does not want to READ the
	// sentence, it wants to know WHICH sentence it is: the dependency edit
	// gate's wayBack tells "the retained volume is gone" from every other
	// unavailable offer, and comparing rendered prose would break the moment
	// the offer is rendered in Russian. Compare ProblemMsg.Key.
	ProblemMsg msg.Message `json:"-"`
}

// DependencyMigrationDto is the live progress of the running migration.
// Percent is the STEP's own sub-progress (0 = indeterminate), not the overall
// one: the steps are wildly uneven (a dump is minutes, a volume create is
// milliseconds), so a percentage across them would be a lie.
type DependencyMigrationDto struct {
	ID        string  `json:"id"`
	Step      string  `json:"step"`
	StepIndex int     `json:"stepIndex"`
	StepCount int     `json:"stepCount"`
	Percent   float64 `json:"percent,omitempty"`
	Message   string  `json:"message,omitempty"`
	// MessageMsg is Message before the locale is applied. It never reaches the
	// wire: the running migration is published once, daemon-globally, and read
	// back by every client in its own language, so the sentence has to survive
	// as data until the request that renders it.
	MessageMsg msg.Message `json:"-"`
	// Kind discriminates a ROLLBACK from a migration: "" is a migration,
	// "rollback" is one. The two share this channel on purpose — a rollback is
	// three steps on the same progress events, so the CLI's renderer and the
	// dialog's progress screen work unchanged and only the title differs.
	Kind string `json:"kind,omitempty"`
	// StepIDs is the running plan's ACTUAL step list, in order — plan.Steps'
	// ids, verbatim, including repeats. It exists because a multi-hop copy
	// upgrade repeats ids (pre-upgrade/start-new/post-upgrade once per rung,
	// and an intermediate rung's own stop shares "stop-new" with the plan's
	// FINAL cleanup step): a client that hardcodes the single-hop vocabulary
	// and locates a step by `indexOf(id)` finds the id's ONLY listed
	// position — the plan's LAST one for "stop-new" — and marks every row
	// before it done, verify included, while verify has not run yet. Only the
	// daemon knows the plan's real shape, so it is the one place this can be
	// fixed: the client positions a row by StepIndex/StepCount now, never by
	// matching ids, and this is what it positions them AGAINST.
	//
	// Omitted (omitempty) rather than always sent is deliberate: an older
	// daemon this field predates sends none, and the client's fallback is its
	// OWN hardcoded vocabulary, unaffected either way — nothing on the wire
	// changed shape for it to trip over.
	StepIDs []string `json:"stepIds,omitempty"`
}

// DependencyMigrationResultDto is the verdict of the last migration, kept
// until the next one replaces it.
type DependencyMigrationResultDto struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	FinishedAt int64  `json:"finishedAt"` // epoch ms
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	// OldVolume names the volume the previous data was left in (success
	// only) — the launcher never deletes it, so this is what the user needs
	// to reclaim the space once they trust the new version.
	OldVolume string `json:"oldVolume,omitempty"`
	// Kind discriminates a rollback ("rollback") from a migration (""). They
	// share the one result slot a namespace has, and "17.5 → 18.6 finished"
	// and "18.6 → 17.5 finished" are otherwise indistinguishable.
	Kind string `json:"kind,omitempty"`
}

// DependenciesDto is the dependency list plus whatever a migration has left
// behind: one running now, the last verdict, and an unfinished rollback.
type DependenciesDto struct {
	Items      []DependencyDto               `json:"items"`
	Migration  *DependencyMigrationDto       `json:"migration,omitempty"`
	LastResult *DependencyMigrationResultDto `json:"lastResult,omitempty"`
	// RollbackPending is non-empty when an interrupted migration's journal is
	// still open: its rollback has not succeeded, the launcher retries it at
	// every start, and until it does the dependency's pin is frozen (the
	// runtime's RUNNING re-pin hook stands aside while a journal exists) and
	// no new migration is accepted.
	RollbackPending string `json:"rollbackPending,omitempty"`
}

// DependencyUpgradeDto is one held-back upgrade, as carried by NamespaceDto.
type DependencyUpgradeDto struct {
	ID   string `json:"id"`
	App  string `json:"app"`
	From string `json:"from"`
	To   string `json:"to"`
	// Migratable reports that this launcher can move THIS PAIR: it ships a
	// plan for the dependency and the pair is one the plan accepts.
	Migratable bool `json:"migratable"`
	// Blocked is non-empty when the hop is refused by the DEPENDENCY'S OWN
	// vendor — a refusal a newer launcher would not lift — and carries the
	// sentence saying so, including the intermediate version to take first
	// when the vendor documents one.
	//
	// It is the discriminator the upgrades banner splits on, and it is not
	// derivable from Migratable: a blocked pair reports Migratable false (the
	// launcher genuinely will not move it), so a banner that only asked that
	// question would file a vendor refusal under "a newer launcher is needed"
	// and send the operator to update one that would refuse it just the same.
	Blocked string `json:"blocked,omitempty"`
	// BundleOlder reports that the held-back candidate is OLDER than what the
	// data runs on. It is not an upgrade at all, so the upgrades banner leaves
	// it out entirely rather than filing it under one of its groups.
	//
	// Without it the banner had no way to tell: a backwards hold reports
	// Migratable false (no launcher moves data backwards) and Blocked empty (a
	// backwards move is never asked the vendor's UPGRADE question), which is
	// exactly the shape of "a newer launcher is needed" — so the operator
	// would have been sent to update a launcher that would never apply it.
	BundleOlder bool `json:"bundleOlder,omitempty"`
}

// DependencyMigrateRequestDto is the body of POST …/dependencies/{id}/migrate.
type DependencyMigrateRequestDto struct {
	// ReplaceExistingVolume confirms deleting a target volume that already
	// exists (a leftover from an earlier attempt). Without it the migration
	// refuses rather than overwrite data it did not create.
	ReplaceExistingVolume bool `json:"replaceExistingVolume"`
}

// PreflightResult is the RENDERED result of GET …/dependencies/{id}/preflight
// (and its rollback sibling): what the confirm dialog and `citeck deps
// upgrade` show before anything is touched. Problems block the operation;
// Warnings need an explicit confirmation (today: an existing target volume).
//
// It is the wire half of migrate.PreflightResult, which carries the same
// facts with its sentences still as msg.Message — the locale is applied at the
// HTTP boundary, where the reader is known, and what leaves the daemon is a
// plain string in the same JSON fields. So neither the web UI nor the CLI has
// to learn a rendering path, and the sentence lives in exactly one place
// (internal/i18n/locales/*.json) instead of being maintained twice.
//
// Problems and Warnings are JSON ARRAYS on the wire, never null: the web
// dialog maps over both without a guard for the ordinary case, and the CLI's
// `for range` over a nil slice hides the difference. The renderer builds them
// with i18n.Translator.RenderAll, which returns a non-nil empty slice for the
// same reason.
type PreflightResult struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	// Sizes in bytes. Host = the filesystem holding the dump; Volume = the
	// filesystem holding the data volumes (the Docker VM's disk on a
	// macOS/Windows desktop, which is NOT the host's).
	DataSizeBytes       int64 `json:"dataSizeBytes"`
	RequiredHostBytes   int64 `json:"requiredHostBytes"`
	RequiredVolumeBytes int64 `json:"requiredVolumeBytes"`
	FreeHostBytes       int64 `json:"freeHostBytes"`
	FreeVolumeBytes     int64 `json:"freeVolumeBytes"`
	// SharedFilesystem reports that those two are ONE filesystem — the
	// ordinary server layout. The dump and the new cluster coexist on it, so
	// what has to fit there is RequiredTotalBytes and not either half alone.
	SharedFilesystem bool `json:"sharedFilesystem"`
	// RequiredTotalBytes is what that one filesystem must have free: the two
	// halves added up. It is 0 when SharedFilesystem is false, where a sum
	// across two disks means nothing — SharedFilesystem is the discriminator,
	// never the zero.
	RequiredTotalBytes   int64           `json:"requiredTotalBytes"`
	ExistingTargetVolume *ExistingVolume `json:"existingTargetVolume,omitempty"`
	WasRunning           bool            `json:"wasRunning"`
	// SpaceChecked reports that the space checks actually ran. It replaces the
	// old "RequiredHostBytes > 0" discriminator, which stopped being true the
	// moment a plan appeared that writes no host file at all: a copy upgrade
	// legitimately requires zero bytes on the host, and rendering a refused
	// preflight's zeros verbatim reads as a namespace with no data and a full
	// disk, printed above the real reason.
	SpaceChecked bool `json:"spaceChecked"`
}

// Measured reports whether the space checks actually ran. A refused preflight
// never probed anything, so every size on it is a zero that means "not
// measured" — and rendered verbatim that reads as a namespace with no data and
// a full disk ("Data size: 0 B", "Host (dump): need 0 B, free 0 B") printed
// above the real reason.
func (res PreflightResult) Measured() bool { return res.SpaceChecked }

// ExistingVolume describes a target volume that is already there — a leftover
// from an earlier attempt, or somebody else's data. Size and version are what
// let the user tell those two apart before confirming its deletion.
type ExistingVolume struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	// Version is what the data itself says it is: PostgreSQL's PG_VERSION, or
	// "empty" when the volume holds no such file. It is "" for a dependency
	// whose data carries no version marker at all (RabbitMQ, ZooKeeper) —
	// which is not the same as "empty", and a renderer must tell the two
	// apart rather than print a version the data never claimed.
	Version string `json:"version"`
}

// LinkDto represents a named URL link associated with a namespace.
type LinkDto struct {
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	Icon           string  `json:"icon,omitempty"`
	Order          float64 `json:"order"`
	Category       string  `json:"category,omitempty"`       // grouping header in the sidebar (English fallback)
	CategoryKey    string  `json:"categoryKey,omitempty"`    // i18n key for the built-in category header; web resolves it, falling back to Category
	Description    string  `json:"description,omitempty"`    // English fallback tooltip
	DescriptionKey string  `json:"descriptionKey,omitempty"` // i18n key; web resolves the localized tooltip, falling back to Description
	AlwaysEnabled  bool    `json:"alwaysEnabled,omitempty"`  // remains clickable when namespace is STOPPED (Kotlin parity)
	// Custom marks a workspace-config-declared link (WorkspaceLink). For these
	// the daemon computes enablement from DependsOn app status and sets Disabled
	// directly (the UI must NOT re-gate them on namespace-wide running status);
	// links whose dependencies are absent from the namespace are omitted entirely.
	Custom    bool     `json:"custom,omitempty"`
	Disabled  bool     `json:"disabled,omitempty"`  // custom link: a present dependency is not RUNNING
	DependsOn []string `json:"dependsOn,omitempty"` // custom link: app IDs it depends on (for tooltips)
}

// EventDto represents a server-sent event for state changes.
//
// Type-specific fields (omitempty so legacy events stay unchanged on the wire):
//   - "pull_progress": Percent (0..100), Phase (active layer id / status string).
//   - "pull_auth_required": After holds the registry host extracted from the image
//     reference so the UI can pre-fill the credentials dialog.
//   - "snapshot_progress": Current (1-based volume index), Total (volume count),
//     After (volume name). Emitted once per volume during export/import so the
//     UI can render a determinate progress bar inside the blocking overlay.
//   - "app_init_step": Current (1-based init step), Total (init container
//     count), After (short step name). Emitted only when the init step index
//     changes during STARTING; all fields zero/empty once the init phase ends
//     (the UI clears its "init {step}/{total}" suffix).
//   - "namespace_updating": After holds "true"/"false". Raised while an
//     "Update And Start" pass is accepted but has not yet reached the runtime
//     (reloadMu wait, git pull, bundle resolve, generate) — a stretch where no
//     namespace or app status changes, so this is the only signal the click was
//     acted on. It carries no NamespaceID and is NOT namespace-scoped — treat it
//     purely as a "refetch me" trigger. The namespace-scoped truth is
//     NamespaceDto.Updating, which the daemon reports only for the namespace the
//     pass is pinned to (see Daemon.updateInFlightNsID); a client must not infer
//     scope from this event.
//   - "disk_low" / "disk_ok": Path (monitored filesystem path), FreeBytes,
//     ThresholdBytes (low-disk threshold). Emitted by the daemon's disk
//     monitor on state CHANGE only — once when free space drops below the
//     threshold and once on recovery, never re-emitted while the state holds.
//   - "deps_migration_start" / "deps_migration_progress" /
//     "deps_migration_complete" / "deps_migration_error": AppName holds the
//     DEPENDENCY id ("postgres"), Phase the step id, Current/Total the step
//     index/count, Percent the step's own sub-progress (0 = indeterminate),
//     After a human message — on start "<from> → <to>", on error the reason
//     (the rollback has already run by the time it is sent), on complete
//     after a finalize failure the warning. StepIDs (start/progress only) is
//     the plan's real step list — see DependencyMigrationDto.StepIDs.
//     NamespaceID is set; the namespace-scoped truth for a client that
//     connects mid-migration is NamespaceDto.DependencyMigration.
//
// The four deps_migration_* type strings are the constants below; the daemon
// broadcasts them and the CLI selects on them. The web store cannot import Go,
// so it keeps its own literals with a comment naming these.
type EventDto struct {
	Type        string `json:"type"`
	Seq         int64  `json:"seq"`
	Timestamp   int64  `json:"timestamp"`
	NamespaceID string `json:"namespaceId"`
	AppName     string `json:"appName"`
	Before      string `json:"before"`
	After       string `json:"after"`
	// AfterMsg is After before the locale is applied, for the events that
	// carry an operator-facing sentence there (the dependency migration and
	// rollback progress). It never reaches the wire — writeSSEEvent renders it
	// into After for the ONE subscriber it is writing to, because a broadcast
	// fans out to a desktop UI in Russian and a CLI in German at the same
	// moment and there is no single language the replay ring could hold.
	AfterMsg msg.Message `json:"-"`
	Percent  float64     `json:"percent,omitempty"`
	Phase    string      `json:"phase,omitempty"`
	Current  int         `json:"current,omitempty"`
	Total    int         `json:"total,omitempty"`
	// Path / FreeBytes / ThresholdBytes are present on "disk_low" / "disk_ok"
	// events only (omitempty keeps every other event unchanged on the wire).
	Path           string `json:"path,omitempty"`
	FreeBytes      int64  `json:"freeBytes,omitempty"`
	ThresholdBytes int64  `json:"thresholdBytes,omitempty"`
	// StepIDs is the running migration's plan.Steps ids, verbatim — see
	// DependencyMigrationDto.StepIDs for why a client needs the real list
	// rather than a hardcoded one. Present on "deps_migration_start" and
	// "deps_migration_progress" once the plan exists (never on "preparing",
	// where there is no plan yet to name).
	StepIDs []string `json:"stepIds,omitempty"`
}

// The dependency-migration event types. They lived as separate literals in the
// daemon and as a second set in the CLI, where a typo on either side is a
// migration that streams no progress and a `citeck deps upgrade` that waits
// for a terminal event which never matches.
//
// EventDepsMigrationPrefix is the family: the CLI selects this dependency's
// events off the shared stream with it (the stream also carries every app
// status and pull event of the namespace being stopped and started).
const (
	EventDepsMigrationPrefix   = "deps_migration_"
	EventDepsMigrationStart    = EventDepsMigrationPrefix + "start"
	EventDepsMigrationProgress = EventDepsMigrationPrefix + "progress"
	EventDepsMigrationComplete = EventDepsMigrationPrefix + "complete"
	EventDepsMigrationError    = EventDepsMigrationPrefix + "error"
)

// DependencyMigrationStepPreparing is the step id published BEFORE the plan
// exists. Building a plan runs the whole preflight — including a `du` of the
// data volume, minutes on a real cluster — and until this the UI and the CLI
// had nothing at all between the click and the first real step: a dead button.
// It is not one of the plan's steps (migrate.PostgresStepIDs), and it is
// published with StepCount 0, which is how a client tells the two apart: a
// step count of zero means "no plan yet", so render a spinner, not a list.
const DependencyMigrationStepPreparing = "preparing"

// HealthStatusStarting is the HealthDto.Status a daemon reports while it is
// still BOOTING. The daemon binds its Unix socket before the slow boot work
// (git pull, Docker enumeration, namespace start) so that "is this process
// alive?" can be answered immediately — the desktop update health gate asks
// exactly that question, and while the socket was bound last it was really
// measuring how long the whole boot took, which failed a good release on a host
// whose Docker was unreachable. Readers that need a FULLY booted daemon (the
// CLI's waitForDaemon, the desktop wrapper's UI proxy gate) branch on this
// value; everything else is refused with ErrCodeDaemonStarting meanwhile.
const HealthStatusStarting = "starting"

// HealthDto reports the overall daemon health status.
type HealthDto struct {
	Status  string           `json:"status"` // "healthy", "degraded", "unhealthy"
	Healthy bool             `json:"healthy"`
	Checks  []HealthCheckDto `json:"checks"`
}

// HealthCheckDto represents a single health check result.
type HealthCheckDto struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ExecResultDto is the response from executing a command in a container.
type ExecResultDto struct {
	ExitCode int64  `json:"exitCode"`
	Output   string `json:"output"`
}

// ExecRequestDto is the request to execute a command in a container.
type ExecRequestDto struct {
	Command []string `json:"command"`
}

// AppInspectDto contains detailed container inspection data.
type AppInspectDto struct {
	Name         string            `json:"name"`
	ContainerID  string            `json:"containerId"`
	Image        string            `json:"image"`
	Status       string            `json:"status"`
	State        string            `json:"state"`
	Ports        []string          `json:"ports"`
	Volumes      []string          `json:"volumes"`
	Env          []string          `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	RestartCount int               `json:"restartCount"`
	StartedAt    string            `json:"startedAt"`
	Uptime       int64             `json:"uptime"`
}

// AppImageDto is the image-details view shown in the drawer's image popup.
// When Present is false the image isn't pulled locally; the UI offers a Pull.
// Pulling/PullError reflect an in-flight or failed explicit pull.
type AppImageDto struct {
	Ref          string   `json:"ref"`
	Present      bool     `json:"present"`
	Pulling      bool     `json:"pulling,omitempty"`
	PullError    string   `json:"pullError,omitempty"`
	ID           string   `json:"id,omitempty"`
	RepoDigests  []string `json:"repoDigests,omitempty"`
	Size         int64    `json:"size,omitempty"`
	OS           string   `json:"os,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	Created      string   `json:"created,omitempty"`
}

// ErrorDto is the standard error response format.
type ErrorDto struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Namespace lifecycle status values carried by NamespaceDto.Status.
// These mirror namespace.NsRuntimeStatus and are the single source of
// truth for the wire format — untyped so the namespace package can
// adopt them as its typed NsRuntimeStatus values (see namespace/runtime.go).
const (
	NsStatusStopped  = "STOPPED"
	NsStatusStarting = "STARTING"
	NsStatusRunning  = "RUNNING"
	NsStatusStopping = "STOPPING"
	NsStatusStalled  = "STALLED"
)

// Per-app lifecycle status values carried by AppDto.Status. Mirror of
// namespace.AppRuntimeStatus — same single-source-of-truth pattern as the
// NsStatus* constants above.
const (
	AppStatusReadyToPull    = "READY_TO_PULL"
	AppStatusPulling        = "PULLING"
	AppStatusPullFailed     = "PULL_FAILED"
	AppStatusReadyToStart   = "READY_TO_START"
	AppStatusDepsWaiting    = "DEPS_WAITING"
	AppStatusStarting       = "STARTING"
	AppStatusRunning        = "RUNNING"
	AppStatusFailed         = "FAILED"
	AppStatusStartFailed    = "START_FAILED"
	AppStatusStopping       = "STOPPING"
	AppStatusStoppingFailed = "STOPPING_FAILED"
	AppStatusStopped        = "STOPPED"
	// AppStatusUpdating is the in-flight recreate state: the runtime sent
	// SIGTERM to the old container because its deployment hash diverged from
	// the new desired definition, and the very next leg is READY_TO_PULL →
	// PULLING → READY_TO_START → STARTING. STOPPING is reserved for explicit
	// user-initiated stops; UPDATING marks transitions the runtime drives
	// itself so the daemon log reads the way the state machine actually
	// behaves rather than masking a stop via desiredNext lookahead.
	AppStatusUpdating = "UPDATING"
)

// ErrCodeAppNotFound and related constants are machine-readable error codes for API consumers.
const (
	ErrCodeAppNotFound        = "APP_NOT_FOUND"
	ErrCodeSnapshotInProgress = "SNAPSHOT_IN_PROGRESS"
	ErrCodeInvalidConfig      = "INVALID_CONFIG"
	ErrCodeInvalidRequest     = "INVALID_REQUEST"
	ErrCodeSSRFBlocked        = "SSRF_BLOCKED"
	ErrCodeRateLimited        = "RATE_LIMITED"
	ErrCodeNotConfigured      = "NOT_CONFIGURED"
	ErrCodeAppAlreadyRunning  = "APP_ALREADY_RUNNING"
	ErrCodeNamespaceRunning   = "NAMESPACE_RUNNING"
	ErrCodeCSRFMissing        = "CSRF_MISSING"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeNamespaceExists    = "NAMESPACE_EXISTS"
	ErrCodeReloadInProgress   = "RELOAD_IN_PROGRESS"
	ErrCodeDesktopOnly        = "DESKTOP_ONLY"
	ErrCodeWorkspaceExists    = "WORKSPACE_EXISTS"
	ErrCodeWorkspaceNotFound  = "WORKSPACE_NOT_FOUND"
	ErrCodeNamespaceNotFound  = "NAMESPACE_NOT_FOUND"
	ErrCodeWorkspaceInUse     = "WORKSPACE_IN_USE"
	// ErrCodeDaemonStarting is returned (HTTP 503, with a Retry-After header)
	// by every route except GET /health while the daemon is still booting. The
	// socket is bound before the slow boot phase so a client — and above all
	// the desktop update health gate — can tell "the process is alive" from
	// "the process is dead"; until the real routes are in, there is no runtime,
	// no store and no Docker client to answer with, and inventing a "running"
	// answer for /daemon/status would leave the caller worse off than waiting.
	ErrCodeDaemonStarting = "DAEMON_STARTING"
	// ErrCodeAuthRequired is returned (HTTP 401) by the TCP transport when
	// daemon.yml api_auth is enabled and the request carries neither a valid
	// `Authorization: Bearer <token>` header nor the session cookie minted by
	// GET /auth/session. The Web UI shows its token prompt on this code.
	ErrCodeAuthRequired = "AUTH_REQUIRED"
	// ErrCodeEncryptionNotSetUp is returned by secret-write endpoints when the
	// SecretService has no master password yet (Kotlin parity — desktop never
	// auto-initializes encryption). The UI catches this and runs the
	// CreateMasterPwd flow before retrying the original save.
	ErrCodeEncryptionNotSetUp = "ENCRYPTION_NOT_SET_UP" //nolint:gosec // G101: error code constant, not a credential
	// ErrCodeSecretNotFound is returned (HTTP 404) by PUT /api/v1/secrets/{id}
	// when no secret with the given id exists.
	ErrCodeSecretNotFound = "SECRET_NOT_FOUND" //nolint:gosec // G101: error code constant, not a credential
	// ErrCodeWsRepoSyncFailed is returned (HTTP 502) when the ACTIVE workspace
	// points at a CUSTOM git repo that cannot be synced (typically a 401 on a
	// TOKEN-auth workspace with a missing/bad token) AND no cached clone is
	// usable. Welcome-data endpoints (quick starts, workspace snapshots) and
	// workspace activation surface it instead of silently serving the built-in
	// fallback workspace (Kotlin 1.x parity: workspace load failed hard). The
	// message carries the repo URL plus the underlying git error text
	// ("authentication required", "repository not found", …) so the Web UI's
	// GitPullErrorDialog heuristic also matches.
	ErrCodeWsRepoSyncFailed = "WS_REPO_SYNC_FAILED"
	// ErrCodeBundleNotSynced is returned (HTTP 409) when a namespace create
	// requests a "LATEST" bundle key but the bundle repo has no synced
	// versions to pin it to. The launcher never persists a symbolic "LATEST"
	// (that would silently auto-update between versions on reload), so it
	// refuses to create a namespace in that broken state — sync the repo first.
	ErrCodeBundleNotSynced = "BUNDLE_NOT_SYNCED"
	// ErrCodeNoBundleConfigured is returned (HTTP 409) when a namespace create
	// ends up with no bundle ref at all — neither from the request nor from the
	// workspace's namespace template nor from its first bundle repo. Every
	// Citeck service comes from the bundle, while the infra apps (postgres,
	// mongo, rabbitmq, zookeeper, mailpit, pgadmin, onlyoffice) are generated
	// unconditionally with hardcoded fallback images, so such a namespace comes
	// up as seven third-party containers reporting RUNNING with none of the
	// product in it. Refused at create rather than persisted, like an
	// unpinnable "LATEST" above. In practice it means the workspace config is
	// unusable (no bundleRepos) — sync the workspace repo first.
	ErrCodeNoBundleConfigured = "NO_BUNDLE_CONFIGURED"
	// ErrCodeDependencyVersionLocked is returned (HTTP 400) by PUT
	// /apps/{name}/config when the edit would move a registered infra
	// dependency (postgres, rabbitmq, …) to an image that is a BREAKING change
	// against the version its data runs on. Patches are applied after the
	// generator's pin gate, so without this a `citeck edit postgres` to 18
	// would put PostgreSQL 18 on a 17 data directory with no migration and no
	// rollback. The message names `citeck deps upgrade <id>`.
	ErrCodeDependencyVersionLocked = "DEPENDENCY_VERSION_LOCKED"
	// ErrCodeLongOpInProgress is returned (HTTP 409) by every route that
	// starts, reshapes or destroys the namespace while a long operation —
	// snapshot export/import or a dependency migration — holds the daemon's
	// long-operation lock. A migration stops the namespace itself, so the
	// "namespace must be stopped" guards those routes already had are exactly
	// the state a migration puts it in; this code is what keeps a Start, a
	// config edit or a namespace delete from racing the migration's containers
	// and volumes.
	ErrCodeLongOpInProgress = "LONG_OP_IN_PROGRESS"
	// ErrCodeDependencyUnknown is returned (HTTP 404) when the {id} path
	// segment names no registered dependency (internal/deps.Lookup).
	ErrCodeDependencyUnknown = "DEPENDENCY_UNKNOWN"
	// ErrCodeDependencyNotMigratable is returned (HTTP 409) when the
	// dependency exists but THIS launcher ships no migration plan for it — the
	// pin holds the old image and the answer is to update the launcher.
	ErrCodeDependencyNotMigratable = "DEPENDENCY_NOT_MIGRATABLE"
	// ErrCodeDependencyPairUnsupported is returned (HTTP 409) when this
	// launcher HAS a migration for the dependency but the DEPENDENCY'S OWN
	// vendor does not support the requested hop — RabbitMQ 4.1 → 4.3 in one
	// step, ZooKeeper data older than 3.5.
	//
	// It is distinct from ErrCodeDependencyNotMigratable on purpose: that one
	// means "update the launcher", and here updating the launcher changes
	// nothing. The body carries what the operator can actually do instead.
	ErrCodeDependencyPairUnsupported = "DEPENDENCY_PAIR_UNSUPPORTED"
	// ErrCodeDependencyUpToDate is returned (HTTP 409) when nothing is being
	// held back for that dependency, so there is nothing to migrate to.
	ErrCodeDependencyUpToDate = "DEPENDENCY_UP_TO_DATE"
	// ErrCodeDependencyPreflightFailed is returned (HTTP 409) when the plan
	// could not be built: the preflight found a blocking problem (too little
	// disk, a data version that is not what the pin claims) or an existing
	// target volume the caller has not confirmed replacing. The message is the
	// preflight's own, and nothing has been touched.
	ErrCodeDependencyPreflightFailed = "DEPENDENCY_PREFLIGHT_FAILED"
	// ErrCodeDependencyMigrationInProgress is returned (HTTP 409) when a
	// migration journal is open for this namespace. That covers two states,
	// and the message tells them apart: a migration running right now, or an
	// interrupted one whose ROLLBACK has not succeeded — the launcher retries
	// that at every start, and until it does, the leftovers the journal
	// describes are still on the host, so starting a second migration over
	// them is exactly what the journal exists to prevent.
	ErrCodeDependencyMigrationInProgress = "DEPENDENCY_MIGRATION_IN_PROGRESS"
	// ErrCodeDependencyNamespaceBusy is returned (HTTP 409) when the namespace
	// is neither plainly RUNNING nor plainly STOPPED. A migration's first step
	// stops the namespace, and Runtime.Stop only ENQUEUES that command: a
	// runtime mid-transition may never read it (the same trap as the Update &
	// Start queue's STOPPING arm), so the migration would burn its whole stop
	// timeout and fail after the user confirmed it.
	ErrCodeDependencyNamespaceBusy = "DEPENDENCY_NAMESPACE_BUSY"
	// ErrCodeDependencyBackwards is returned (HTTP 409) by both migration
	// routes when the bundle's candidate is OLDER than the version the
	// namespace's data runs on. It is a held-back candidate like any other, so
	// it reaches the routes as a pending "upgrade" — but there is no upgrade
	// to run, and the two codes that were reachable before it existed both lie:
	// DEPENDENCY_NOT_MIGRATABLE says a newer launcher would help (none will
	// ever move data backwards) and DEPENDENCY_PAIR_UNSUPPORTED reports a
	// vendor's refusal to a question nobody asked. The body carries the
	// bundle-older sentence, which names the rollback when there is one.
	ErrCodeDependencyBackwards = "DEPENDENCY_BACKWARDS"
	// ErrCodeDependencyNoRollbackTarget is returned (HTTP 409) by the two
	// rollback routes when the dependency's pin records no previous state:
	// nothing has migrated it, or it has already been rolled back (a rollback
	// clears its own target — there is no roll-forward).
	ErrCodeDependencyNoRollbackTarget = "DEPENDENCY_NO_ROLLBACK_TARGET"
	// ErrCodeLauncherTooOld is returned (HTTP 409) when the namespace config
	// being written names a bundle whose minLauncherVersion is above this
	// launcher's version. It is raised on WRITE only — creating, editing,
	// upgrading — never on load: refusing to load would leave the operator
	// unable to open the namespace and pick a different bundle, which is the
	// only way out of the situation.
	ErrCodeLauncherTooOld = "LAUNCHER_TOO_OLD"
)

// UpgradeRequestDto is the request body for the namespace upgrade endpoint.
type UpgradeRequestDto struct {
	BundleRef string `json:"bundleRef"`
}

// --- Reload plan (dry-run) ---

// ReloadPlanAppDto is one app's predicted outcome of a reload, as computed by
// GET /namespace/reload-plan without applying anything.
type ReloadPlanAppDto struct {
	Name string `json:"name"`
	// Verdict is one of: create | recreate | keep | remove | detached
	// (namespace.PlanVerdict* constants).
	Verdict string `json:"verdict"`
	// DiffAdded / DiffRemoved are deployment-hash-input lines present only in
	// the new / only in the current definition (recreate verdicts only). The
	// lines are human-readable ("env:KEY=value", "imageDigest=sha256:…").
	DiffAdded   []string `json:"diffAdded,omitempty"`
	DiffRemoved []string `json:"diffRemoved,omitempty"`
	// SnapshotTag marks a kept app with a :snapshot image. A real reload
	// re-pulls such images from the registry before the hash diff, so "keep"
	// can become "recreate" if a new image was pushed under the same tag.
	SnapshotTag bool `json:"snapshotTag,omitempty"`
}

// ReloadPlanSummaryDto counts plan entries per verdict.
type ReloadPlanSummaryDto struct {
	Create   int `json:"create"`
	Recreate int `json:"recreate"`
	Keep     int `json:"keep"`
	Remove   int `json:"remove"`
	Detached int `json:"detached"`
}

// ReloadPlanDto is the response of GET /namespace/reload-plan: the per-app
// plan of what a reload would do right now, plus bundle-version context.
type ReloadPlanDto struct {
	Apps    []ReloadPlanAppDto   `json:"apps"`
	Summary ReloadPlanSummaryDto `json:"summary"`
	// BundleBefore / BundleAfter are the resolved bundle versions currently
	// active vs. freshly resolved for this plan (equal when nothing changed).
	BundleBefore string `json:"bundleBefore,omitempty"`
	BundleAfter  string `json:"bundleAfter,omitempty"`
	// BundleFallback is true when bundle resolution failed and the plan was
	// computed from the cached bundle (same fallback a real reload uses).
	BundleFallback bool `json:"bundleFallback,omitempty"`
	// WouldSkip is true when an actual reload would refuse to apply this set:
	// the cached-bundle fallback produced fewer apps than are currently
	// running, and doReloadEx preserves the current runtime in that case.
	WouldSkip bool `json:"wouldSkip,omitempty"`
}

// --- Welcome Screen ---

// NamespaceSummaryDto is a lightweight namespace representation for the welcome screen.
type NamespaceSummaryDto struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	BundleRef   string `json:"bundleRef"`
}

// WorkspaceDto describes a workspace for API consumers (desktop-only multi-workspace).
//
// RepoPullPeriod is an ISO 8601 duration string (e.g. "PT2H"); AuthType is
// "NONE" or "TOKEN". SecretID references a reusable secret (one GitLab token
// shared by several workspaces); when empty, TOKEN auth falls back to the
// legacy per-workspace secret under key "ws:{id}:repo".
// Defaults applied at the storage layer when fields are empty.
type WorkspaceDto struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	RepoBranch     string `json:"repoBranch"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
	SecretID       string `json:"secretId,omitempty"`
	Active         bool   `json:"active"`
	Namespaces     int    `json:"namespaces"`
}

// WorkspaceCreateDto is the request body for POST /api/v1/workspaces.
// ID may be empty — the daemon derives a safe slug from Name.
// SecretID optionally links a reusable git-token secret for repo auth.
type WorkspaceCreateDto struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	RepoBranch     string `json:"repoBranch,omitempty"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
	SecretID       string `json:"secretId,omitempty"`
}

// WorkspaceUpdateDto is the request body for PUT /api/v1/workspaces/{id}.
// Name + repo fields are optional — only non-empty fields are applied.
// SecretID uses the pointer sentinel convention (see NamespaceEditDto):
// absent (nil) = unchanged, empty string = unlink the secret reference.
type WorkspaceUpdateDto struct {
	Name           string  `json:"name,omitempty"`
	RepoURL        string  `json:"repoUrl,omitempty"`
	RepoBranch     string  `json:"repoBranch,omitempty"`
	RepoPullPeriod string  `json:"repoPullPeriod,omitempty"`
	AuthType       string  `json:"authType,omitempty"`
	SecretID       *string `json:"secretId,omitempty"`
}

// QuickStartDto represents a quick-start template entry. BundleRef is the
// resolved "repo:key" reference the QS button surfaces as its subtitle
// (Kotlin parity: WelcomeScreen.kt:387 renders `namespaceConfig.bundleRef`).
type QuickStartDto struct {
	Name      string `json:"name"`
	Template  string `json:"template"`
	Snapshot  string `json:"snapshot,omitempty"`
	BundleRef string `json:"bundleRef,omitempty"`
}

// --- Secrets ---

// SecretMetaDto contains non-sensitive secret metadata for API responses.
// Username (BASIC_AUTH / REGISTRY_AUTH) is metadata, not a credential — the
// write-only edit form prefills it from here. The VALUE is never returned by
// any endpoint.
type SecretMetaDto struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Scope string `json:"scope"`
	// Host is the registry/git host this secret authenticates against; the
	// host-filtered secret picker uses it so a credential is reused per host
	// instead of re-entered. Empty for host-agnostic secrets.
	Host      string `json:"host,omitempty"`
	Username  string `json:"username,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// SecretCreateDto is the request body for creating or updating a secret.
//
// Username is set for BASIC_AUTH / REGISTRY_AUTH only; Value carries the
// password verbatim (Kotlin AuthSecret.Basic parity — passwords containing
// ':' must round-trip untouched).
type SecretCreateDto struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
	Value    string `json:"value"`
	Scope    string `json:"scope,omitempty"`
	Host     string `json:"host,omitempty"`
}

// SecretUpdateDto is the request body for PUT /api/v1/secrets/{id} — a
// WRITE-ONLY partial edit. Every field is optional: an empty/absent field
// keeps the stored one. Value especially: empty means "value unchanged",
// so the UI can edit name/scope without ever seeing (or re-entering) the
// secret value. The secret's Type is immutable through this endpoint.
type SecretUpdateDto struct {
	Name     string `json:"name,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Username string `json:"username,omitempty"`
	Value    string `json:"value,omitempty"`
	Host     string `json:"host,omitempty"`
}

// RegistryBindingDto binds an image-registry host to a stored REGISTRY_AUTH
// secret for the active workspace. An empty SecretID removes the binding.
type RegistryBindingDto struct {
	Host     string `json:"host"`
	SecretID string `json:"secretId"`
}

// --- Diagnostics ---

// DiagnosticCheckDto represents a single diagnostic check result.
type DiagnosticCheckDto struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warning", "error"
	Message string `json:"message"`
	Fixable bool   `json:"fixable"`
}

// DiagnosticsDto aggregates all diagnostic check results.
type DiagnosticsDto struct {
	Checks []DiagnosticCheckDto `json:"checks"`
}

// DiagFixResultDto reports the outcome of applying diagnostic fixes.
type DiagFixResultDto struct {
	Fixed   int    `json:"fixed"`
	Failed  int    `json:"failed"`
	Message string `json:"message"`
}

// --- Snapshots ---

// SnapshotDto represents a snapshot file with metadata.
type SnapshotDto struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Size      int64  `json:"size"`
}

// --- Namespace creation ---

// NamespaceCreateDto is the request body for creating a new namespace.
type NamespaceCreateDto struct {
	Name               string   `json:"name"`
	AuthType           string   `json:"authType"`
	Users              []string `json:"users,omitempty"`
	Host               string   `json:"host"`
	Port               int      `json:"port"`
	TLSEnabled         bool     `json:"tlsEnabled"`
	TLSMode            string   `json:"tlsMode,omitempty"` // "self-signed", "letsencrypt", "custom"
	PgAdminEnabled     bool     `json:"pgAdminEnabled"`
	BundleRepo         string   `json:"bundleRepo"`
	BundleKey          string   `json:"bundleKey"`
	WorkspaceID        string   `json:"workspaceId,omitempty"`
	Snapshot           string   `json:"snapshot,omitempty"`       // snapshot ID from workspace config
	Template           string   `json:"template,omitempty"`       // namespace template ID
	MasterPassword     string   `json:"masterPassword,omitempty"` // encryption master password
	UseDefaultPassword bool     `json:"useDefaultPassword"`       // use default "citeck" password
}

// SnapshotDownloadDto is the request body for downloading a snapshot from a URL.
type SnapshotDownloadDto struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Name   string `json:"name,omitempty"` // output file name (auto-generated if empty)
}

// NamespaceCreateDefaultsDto is the pre-filled form payload for the create
// dialog. Mirrors the Kotlin 1.x `toFormData(null)` path in NamespacesService:
// auto-generated "Citeck #N" name + bundle/auth defaults pulled from the
// workspace's "default" namespace template (with LATEST → first repo + LATEST
// fallback). Returned by GET /namespace/create-defaults.
type NamespaceCreateDefaultsDto struct {
	Name       string   `json:"name"`
	BundleRepo string   `json:"bundleRepo"`
	BundleKey  string   `json:"bundleKey"`
	AuthType   string   `json:"authType"`
	Users      []string `json:"users,omitempty"`
}

// NamespaceEditDto exposes the typed subset of namespace.yml that the Web
// UI's "edit namespace" form drives. Mirrors the field set the Kotlin
// EditNamespaceDialog exposed (name, bundleRef, authType, users, proxy host
// + port, TLS toggle, pgAdmin toggle). Round-trip safe: GET returns the
// values stored in the target namespace's namespace.yml (bundle key RAW —
// a stored "LATEST" is returned as "LATEST", never display-resolved); PUT
// applies them on top of the existing on-disk YAML so fields outside this
// DTO are preserved. Partial-payload semantics on PUT: empty Name/AuthType/
// Host, nil Users and nil TLSEnabled/PgAdminEnabled mean "leave unchanged" —
// a partial payload never wipes the stored values.
type NamespaceEditDto struct {
	Name       string   `json:"name"`
	BundleRepo string   `json:"bundleRepo"`
	BundleKey  string   `json:"bundleKey"`
	AuthType   string   `json:"authType"`
	Users      []string `json:"users,omitempty"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	// TLSEnabled / PgAdminEnabled use pointers so an absent field on PUT
	// means "leave unchanged" (mirrors the AuthType/Users semantics above);
	// only an explicit true/false applies. GET always fills both.
	TLSEnabled     *bool `json:"tlsEnabled,omitempty"`
	PgAdminEnabled *bool `json:"pgAdminEnabled,omitempty"`
	// MongoEnabled follows the same pointer convention. GET reports the
	// EFFECTIVE answer (the stored flag when set, otherwise the default for
	// this namespace's config generation), so the form shows what the
	// namespace actually runs rather than the absence of a key.
	MongoEnabled *bool `json:"mongoEnabled,omitempty"`
	// ConfigVersion is the namespace-config generation (namespace.yml
	// `apiVersion`), 1 for everything created before the field carried meaning.
	// The UI uses it to decide which legacy toggles are worth showing at all:
	// a namespace created at generation 2+ never has MongoDB, so offering the
	// checkbox there would be offering a switch with nothing behind it.
	ConfigVersion int `json:"configVersion,omitempty"`
}

// BundleInfoDto describes a bundle repository and its available versions.
type BundleInfoDto struct {
	Repo     string   `json:"repo"`
	Versions []string `json:"versions"`
}

// ValidationErrorDto is returned when server-side form validation fails.
type ValidationErrorDto struct {
	Error  string          `json:"error"`
	Fields []FieldErrorDto `json:"fields"`
}

// FieldErrorDto identifies a specific form field validation error.
type FieldErrorDto struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// --- System / file-manager helpers ---

// OpenDirRequestDto is the request body for the "open directory in OS file
// manager" endpoint. The kind identifies a server-side allowlisted path so
// the request itself never carries a raw filesystem path: this avoids any
// path-traversal foothold and keeps the API stable when desktop and server
// modes resolve the directory differently.
type OpenDirRequestDto struct {
	// Kind selects which allowlisted directory to open.
	// Supported values: "volumes" (current namespace's volumes/runtime base),
	// "snapshots" (current namespace's snapshot cache folder).
	Kind string `json:"kind"`
}

// OpenDirResponseDto reports what happened. Path is always populated (even
// when Opened is false in server-mode) so the UI can show / copy it.
type OpenDirResponseDto struct {
	Opened bool   `json:"opened"`
	Path   string `json:"path"`
	// Mode is "desktop" (Wails / xdg-open used) or "server" (path returned only).
	Mode    string `json:"mode"`
	Message string `json:"message,omitempty"`
}

// --- Export directory (per-app outbound artifacts) ---

// ExportFileDto describes one file in an app's export directory — the
// container's OUTPUT mount (/citeck/export), where a heap dump, a pg_dump or a
// thread dump lands so a human can take it off the box.
//
// HostPath is the file's absolute path on the daemon's host. It is here so a
// CLI running on that same host can move the file instead of streaming it
// through the daemon (a heap dump is heap-sized); a remote client simply
// ignores it and downloads.
type ExportFileDto struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	HostPath string `json:"hostPath,omitempty"`
}

// --- JVM diagnostics ---

// JVMCommandRequestDto is the body of the per-app jcmd endpoint. Command is a
// jcmd verb ("Thread.print", "GC.class_histogram", "help"); Args are its
// options. They are joined into a single attach argument by the daemon —
// HotSpot's attach protocol takes the whole jcmd line in one slot.
type JVMCommandRequestDto struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// JVMCommandResponseDto carries the JVM's own output verbatim.
type JVMCommandResponseDto struct {
	App     string `json:"app"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

// HeapDumpResponseDto names the dump that was written into the app's export
// directory. The file is fetched through the export API — the only route that
// also works when the daemon is on another host.
type HeapDumpResponseDto struct {
	App  string `json:"app"`
	File string `json:"file"`
	Size int64  `json:"size"`
}
