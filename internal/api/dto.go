package api

// ActionResultDto is the response for simple action endpoints.
type ActionResultDto struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
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
}

// LinkDto represents a named URL link associated with a namespace.
type LinkDto struct {
	Name           string  `json:"name"`
	URL            string  `json:"url"`
	Icon           string  `json:"icon,omitempty"`
	Order          float64 `json:"order"`
	Category       string  `json:"category,omitempty"`       // grouping header in the sidebar
	Description    string  `json:"description,omitempty"`    // English fallback tooltip
	DescriptionKey string  `json:"descriptionKey,omitempty"` // i18n key; web resolves the localized tooltip, falling back to Description
	AlwaysEnabled  bool    `json:"alwaysEnabled,omitempty"`  // remains clickable when namespace is STOPPED (Kotlin parity)
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
//   - "disk_low" / "disk_ok": Path (monitored filesystem path), FreeBytes,
//     ThresholdBytes (low-disk threshold). Emitted by the daemon's disk
//     monitor on state CHANGE only — once when free space drops below the
//     threshold and once on recovery, never re-emitted while the state holds.
type EventDto struct {
	Type        string  `json:"type"`
	Seq         int64   `json:"seq"`
	Timestamp   int64   `json:"timestamp"`
	NamespaceID string  `json:"namespaceId"`
	AppName     string  `json:"appName"`
	Before      string  `json:"before"`
	After       string  `json:"after"`
	Percent     float64 `json:"percent,omitempty"`
	Phase       string  `json:"phase,omitempty"`
	Current     int     `json:"current,omitempty"`
	Total       int     `json:"total,omitempty"`
	// Path / FreeBytes / ThresholdBytes are present on "disk_low" / "disk_ok"
	// events only (omitempty keeps every other event unchanged on the wire).
	Path           string `json:"path,omitempty"`
	FreeBytes      int64  `json:"freeBytes,omitempty"`
	ThresholdBytes int64  `json:"thresholdBytes,omitempty"`
}

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
