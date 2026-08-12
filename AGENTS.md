# AGENTS.md

Guidance for AI coding agents working in this repository. Claude Code reads it through
the `CLAUDE.md` symlink (which points here); other agents read `AGENTS.md` directly.

## Project Overview

Citeck Launcher manages Citeck namespaces and Docker containers. It is a single Go binary (~24 MB) that serves as both CLI and daemon. The embedded React Web UI is **not offered in server mode** — the CLI/TUI is the supported server interface. The `daemon.yml` `server.webui.enabled` flag is still parsed but is **inert in server mode**: the runtime gate ignores it and the TCP listener binds in server mode only via the explicit `CITECK_SERVER_WEBUI=1` dev/E2E hatch (never through config). Desktop mode renders the same Web UI through a Wails webview that talks to the daemon over the Unix socket (its TCP listener is off too, except the `CITECK_DESKTOP_TCP=1` hatch).

The repo also keeps the legacy Kotlin/JVM launcher under git tags `v1.0.0`–`v1.3.9` for reference and rollback (`git show v1.3.8:path/to/file`). The desktop migrator opens its H2 `storage.db` read-only on first start.

## Build & Development Commands

### Go + Web UI (primary)

```bash
make check                    # FULL local gate (CI superset + eslint) — run before committing/tagging
make build                    # Build Go binary + embed React web UI → dist/bin/citeck-server
make build-fast               # Build Go only (skip web rebuild) → dist/bin/citeck-server
make build-desktop            # Build desktop (Wails) binary → dist/bin/citeck-launcher
make test                     # Run all tests (Go + Vitest)
make test-unit                # Go unit tests only (./internal/...)
make test-race                # Go tests with race detector + 120s timeout
make test-coverage            # Go coverage report → coverage.html
make lint                     # Run Go (golangci-lint) + Web (eslint) linters
make fmt                      # Format Go code
make tidy                     # go mod tidy
make tools                    # Install golangci-lint v2.11.4
make clean                    # Remove build artifacts
dist/bin/citeck-server start --foreground   # Run daemon in foreground
./dist/bin/citeck-launcher            # Run desktop app (Wails webview)
```

### Desktop installers

Packaging configs live in `packaging/` (nfpm → deb/rpm, WiX → msi, macOS scripts → dmg).

**macOS signing is load-bearing, and its step ORDER is a contract.** An app that is not
Developer ID signed AND notarized is rejected by Gatekeeper after any browser download with
the misleading Finder dialog *"citeck-launcher is damaged and can't be opened"* — the word
"damaged" means "not notarized", not "corrupt file". `packaging/macos/release.sh` therefore
runs `make-app.sh` → `sign-app.sh` → `make-dmg.sh` → `notarize-dmg.sh`: signing must come
after `make-app.sh` (which rewrites `Info.plist` via PlistBuddy — the last mutation of the
bundle) and before the dmg is built from it. `make-dmg.sh` uses `ditto`, not `cp -R`, because
`cp` drops the extended attributes that carry the seal. Hardened-runtime entitlements
(`entitlements.plist`) grant JIT + unsigned executable memory — notarization passes without
them but the embedded WebKit view crashes on first render. Signing is driven by
`MACOS_SIGN_IDENTITY` + the `MACOS_NOTARY_*` secrets; with none configured the scripts fall
back to an **ad-hoc** signature. Set the repo variable `MACOS_REQUIRE_SIGNED=1` once a
certificate exists to make an unsigned macOS build fail the release instead of shipping.

**Ad-hoc is not a no-op — it is 1.x parity.** Verified by inspecting the shipped artifacts:
the 1.x jpackage bundle had `Contents/_CodeSignature` (`CS_ADHOC`, empty CMS blob — no
certificate), while the 2.9.0 bundle had no `_CodeSignature` at all. Both were equally
un-notarized, so the regression was purely the missing **bundle-level** seal, and it changes
which dialog the user gets: an ad-hoc signed bundle yields *"the developer cannot be verified"*
(bypassable with right-click → Open), an unsigned one yields *"is damaged and can't be opened"*
(hard block, no in-UI recovery). Go's linker ad-hoc signs the arm64 Mach-O automatically, but
Gatekeeper assesses the BUNDLE, so that never helped. Note the distinction `sign-app.sh` draws:
a **missing certificate** is a routine state (warn, ad-hoc, continue), but **codesign itself
failing** is fatal — on a macOS runner that means a broken toolchain or a malformed bundle, and
continuing would publish the very "is damaged" artifact the script exists to prevent. Do not
"soften" that into a warning: a green release that silently contains the bug is worse than a red
one, and nobody reads `::warning::` on a passing build.
A `v*.*.*` tag builds all installers via `.github/workflows/release-go.yml` and attaches
them to the GitHub Release alongside the server tarballs. Release artifacts are named
`citeck-desktop_<version>_<os>_<arch>.<ext>` (the package/app *identity* stays `citeck-launcher`
for the upgrade contract). Clean upgrade over the legacy
1.* installer: same deb/rpm package name `citeck-launcher`, shared Windows `UpgradeCode`,
and a `citeck-launcher.app` bundle name matching 1.*'s path. Run the local clean-upgrade
e2e with `./scripts/test/test-deb-upgrade.sh` (needs Docker + GTK3 dev libs).

## Architecture

### Go Daemon + CLI (`internal/`)

| Package | Purpose |
|---|---|
| `internal/cli/` | Cobra CLI commands (start, stop, status, setup, install, upgrade, etc.) |
| `internal/daemon/` | HTTP server, API routes (SSE events, config, volumes), middleware |
| `internal/namespace/` | Config parsing, container generator, runtime state machine, reconciler |
| `internal/docker/` | Docker SDK wrapper (containers, images, exec, logs via stdcopy, probes) |
| `internal/bundle/` | Bundle definitions and resolution from git repos |
| `internal/git/` | Git clone/pull via go-git (pure Go, with token auth, hard-reset, reclone). Reclone has two distinct intents — see "Reclone safety" below |
| `internal/config/` | Filesystem paths, daemon config (daemon.yml), workspace dir scanner |
| `internal/storage/` | Store interface + FileStore (server) + SQLiteStore (desktop) + SecretService (AES-256-GCM encryption) |
| `internal/h2migrate/` | Pure-Go H2 MVStore reader (chunk → layout → meta → maps), LZF decompressor, TransactionStore `VersionedValue` wrapper stripping, AES-GCM secrets decrypt, Kotlin→Go translators for `ApplicationDef` / `BundleDef`, one-shot `storage.db → storage.db.kotlin-bak` backup, drives H2→SQLite migration (no JAR / JRE) |
| `internal/client/` | DaemonClient over Unix socket + mTLS TCP transport |
| `internal/output/` | Text/JSON output formatter, tables, colors |
| `internal/api/` | Shared API types (DTOs), path constants |
| `internal/appdef/` | Application definition models (ApplicationDef, ApplicationKind) |
| `internal/appfiles/` | Embedded resource files (go:embed) |
| `internal/form/` | Form field specs, built-in field definitions, validation |
| `internal/snapshot/` | Volume snapshot export/import (ZIP + tar.xz) |
| `internal/tlsutil/` | TLS cert utilities (self-signed, client cert, CA pool loader) |
| `internal/fsutil/` | Atomic file write (temp+fsync+rename), RotatingWriter (log rotation), CleanLogHandler (human-readable slog), shared zip extraction helper (`ExtractZip`, `zip.go`) |
| `internal/acme/` | ACME/Let's Encrypt client + auto-renewal service |
| `internal/license/` | Byte-exact Go port of the Kotlin license-signature verification (canonical signing-form serialization is a hard compat contract — licenses signed by the Kotlin 1.x infrastructure must verify unchanged) |
| `internal/update/` | Desktop auto-update service: GitHub latest-release discovery, changelog fetch (`changelog/` repo files), tarball staging with sha256 verify + ACTIVE ed25519 release-signature seam (`signature.go`; non-empty `embeddedSigningPubKeyHex` ⇒ `.sig` is mandatory, an unsigned release fails closed on auto-update), staged-payload manifest with health-gate states (staged/pending/good/failed); `updatetest/` fake GitHub for tests |
| `internal/i18n/` | Embedded JSON locale files for the **Go CLI/TUI only** (see Localization — the web UI uses its own separate TS locale files) |
| `internal/desktop/` | Wails thin-wrapper: supervises the daemon as a child process (`supervisor.go`) chosen via the daemon-binary selection seam (`binselect.go`), drives it over the daemon↔wrapper native-verb control socket (`control.go` → `wrapper.sock`) with a capabilities feature-detection contract (`caps.go`); single-instance guard + orphan-daemon reap retained; daemon lifecycle bound to the wrapper via `CITECK_WRAPPER_PID` (Pdeathsig on Linux + a pid-poll watchdog in the daemon for macOS/Windows) |
| `internal/namespace/nsactions/` | Pull/start retry constants and auth-error helpers for `runtime_workers.go` |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`. Darcula/Lens dark theme.

The lists below are **load-bearing highlights, not a full inventory** — `web/src/components/` alone has ~40 files. Always `ls web/src/{pages,components,lib,hooks}` before assuming a file does or doesn't exist.

**Pages (`web/src/pages/`)** — Dashboard, Welcome, AppDetail, Logs, DaemonLogs, Config, Volumes, Secrets, Diagnostics, Licenses, WindowEditor, WindowLogs, DockerNotAvailable. Notable:
- `Dashboard.tsx` — sidebar + app table + right drawer overlay + bottom panel
- `Config.tsx` — `/config`, the **Settings** page: workspace git settings + registry credential bindings + daemon health. (The raw namespace.yml editor was removed; per-app YAML editing lives in AppConfigEditor / WindowEditor.) **It is workspace-level, NOT namespace-level** — nothing on it is namespace-scoped, and the old `hasNamespace` guard on the route made the TabBar gear a silent no-op whenever no namespace was open: `/config` redirected to `/`, `/` rendered Welcome, and the gear (hidden at `/`) vanished with it, so the icon appeared to disappear on click. The same redirect also swallowed a fast click during boot while `namespace` is still `null`. Do not re-add that guard. The page is also the only non-error surface for **registry host→secret bindings** (`GET /registry-bindings`), which were otherwise editable solely through the dialog that pops up on a pull-auth failure — pick the wrong secret once and there was no way back, because the daemon accepts any binding whose secret merely exists (`registry_auth.go` `addAuth` never validates the secret against the host), so `/registry-bindings/missing` reports the host as satisfied and preflight never prompts again.
- `WindowEditor.tsx` / `WindowLogs.tsx` — standalone pages for desktop multi-window mode (`/window/...` routes, no app shell, theme via `useInheritedTheme`)
- `Licenses.tsx` — enterprise license management (paste signed-license JSON; stored as encrypted secrets)
- `DockerNotAvailable.tsx` — full-screen "Docker is not available" screen (Kotlin parity)

**Components (`web/src/components/`)** — notable:
- `CodeEditor.tsx` — the shared CodeMirror-based code editor (YAML/JSON/JS/shell/dockerfile highlighting, in-editor search) used by WindowEditor and AppConfigEditor
- `AppTable.tsx`, `BottomPanel.tsx`, `RightDrawer.tsx`, `LogViewer.tsx`, `DaemonLogsViewer.tsx`, `AppDrawerContent.tsx`, `AppConfigEditor.tsx`, `RestartEvents.tsx` — dashboard surfaces
- `LogViewport.tsx` — virtualized log list (tanstack-virtual). Follow model: **breaking** follow happens on user intent (wheel-up) so a bursty stream can't swallow it via the programmatic-scroll gate; the ≤50px bottom heuristic only re-arms it. Rows are keyed by `LogEntry.id` (`getItemKey`). **Wrap OFF = fixed-row fast path**: per-row `measureElement` is NOT attached (rows are uniform; height probed once from the first rendered row) — attaching it caused a measure/adjust storm that stuttered upward scrolling at the edge of the measured window; wrap ON re-enables measurement + `shouldAdjustScrollPositionOnItemSizeChange` (an *instance* property, not a constructor option, in the pinned virtual-core), and toggling wrap resets the measurement cache (`virtualizer.measure()`). Content mousedown pauses stream flushes (via `LogViewer`'s `selecting` state) so text selection survives, and puts the `log-select-drag` guard class on `<body>` (`index.css`) so a drag past the viewport edge cannot pull the toolbar/window chrome into the selection; Ctrl+A select-all auto-clears on Escape/`selectionchange` (stale flag used to re-select the container on every scroll — the "scroll got slow" bug). **Copy is buffer-backed, not DOM-backed**: the native selection only spans mounted rows (rows past overscan unmount and silently leave it), so the copy handler serves Ctrl+A from the full filtered list and a drag selection from tracked logical bounds — entry id + char offset per endpoint, recorded on `selectionchange` during the drag (`web/src/lib/logSelection.ts`); copies from editable elements are never hijacked. Tracked bounds are invalidated by USER ACTIONS (click outside the viewer, Escape, select-all, fresh drag), never by DOM state — engines collapse/detach the selection when rows unmount, and a detached anchor is not a "foreign selection". WebKitGTK (desktop webview) extras: rows positioned via `top` not `transform` (its caret hit-test is transform-unaware in places), blank lines render an NBSP (an empty div has no caret position), and a focus clamp re-extends the selection to the row under the pointer when the engine's hit-test lands >1 row away (the "selection jumps to the top of the list" bug). The clamp runs on `selectionchange` — NOT mousemove — because past the container edge the engine extends via its own drag-autoscroll timer with no mouse events; mousemove only records the pointer Y. Drag starts on the LEFT button only (a right-click's mouseup never arrives — the "drag" stayed armed, the selection followed the bare cursor and per-selectionchange clamp work made wheel scrolling crawl); missed-mouseup nets are `blur`/`visibilitychange`, NOT `ev.buttons===0` (scroll-synthesized mousemoves can carry stale buttons)
- `FormDialog.tsx` (spec-driven forms), `JournalDialog.tsx` (data-table dialog), `ConfirmModal.tsx`, `Modal.tsx`, `Toast.tsx`, `ContextMenu.tsx`, `Select.tsx`, `StatusBadge.tsx`, `ErrorBoundary.tsx` — reusable primitives
- Domain dialogs: `NamespaceDialog`/`NamespaceEditDialog`, `SecretsDialog` + `SecretsUnlockGuard` + `MasterPasswordDialog`, `SnapshotsDialog`, `VolumesDialog`, `RegistryCredentialsDialog`, `GitPullErrorDialog`, `UpdateDialog`/`UpdateNotification`, `WorkspaceFormDialog` (the typed workspace git form — extracted from `WorkspaceSelector` once the Settings page became a second caller), `MigrationDegradedBanner`
- `SecretPicker.tsx` — **`activeIdx` indexes the FILTERED (`visible`) list, never the raw `secrets` array.** The two diverged: the mouse-open path seeded the index from the unfiltered list while Enter committed from the filtered one, so click-then-Enter bound a secret belonging to a *different registry host* (and with the default `activeIdx = 0`, always the first visible row). Every seed goes through one helper and is clamped to the current row count; re-derive it whenever `visible` changes shape (host change, `showAllHosts` toggle, background reload). A selection filtered out by the host filter is kept in the list and flagged with its real host rather than silently rendering as valid on the closed trigger.
- `RegistryCredentialsDialog.tsx` — takes a `manage` prop. The error flow ("Sign in to…" / "Save & Retry") implies a stuck pull to retry; opened from Settings there is none, so the wording switches. Unbinding is `POST /registry-bindings` with an empty `secretId` — the daemon always supported it, there was simply no UI.

**Lib (`web/src/lib/`)** — notable:
- `api.ts` — REST API client (fetchWithTimeout, CSRF, exported API_BASE)
- `store.ts` — Zustand dashboard store (SSE events, exponential backoff reconnect)
- `i18n.ts` — i18n store (8 locales bundled synchronously, t() + useTranslation())
- `websocket.ts` — SSE EventSource wrapper (not WebSocket despite filename)
- `types.ts` — TypeScript interfaces matching Go DTOs
- plus smaller zustand stores/helpers: `panels`, `tabs`, `toast`, `theme`, `desktop`, `updateStore`, `windowBus`, `errorModal`, `longOp`, `daemonStatus`, `files`, `datetime`

**Hooks (`web/src/hooks/`):**
- `useResizeHandle.ts` — pointer-capture drag hook for bottom panel resize
- `useContextMenu.ts` — context menu state management
- `useInheritedTheme.ts` — desktop child windows inherit the main window's theme
- `useLogStream.ts` — log stream buffer + level parsing/colors (`LEVEL_COLORS`). Buffer is `LogEntry[]` with **monotonic per-line ids** (virtualizer row keys — row identity survives front-trims), chunks are **coalesced** (one state update per `LOG_FLUSH_INTERVAL_MS`), the window **freezes while `follow=false`** (no front-trim under a reading user; tail cap re-applied on follow resume) and **pauses while `paused=true`** (mouse drag-selection in progress). The backlog↔live seam is **lossless**: the follow stream opens BEFORE the backlog GET, its lines are held until the backlog lands, and the overlap is deduped on merge (`overlapLineCount`)
- `useLogFilter.ts` — client-side log level/search filtering over entries (`filterEntries`)

### Entry Point

`cmd/citeck/main.go` — CLI entry point (cobra root command).

## Localization (i18n)

8 locales: **en** (source of truth), **ru, zh, es, de, fr, pt, ja**. There are **three independent localization assets** — they do NOT share strings:

| Asset | Path | Consumed by | Format |
|---|---|---|---|
| CLI / TUI strings | `internal/i18n/locales/<loc>.json` | Go binary (`internal/i18n/i18n.go`, `//go:embed locales/*.json`) | flat JSON, **dotted keys** (`"setup.s3.access_key"`) |
| Web UI strings | `web/src/locales/<loc>.ts` | React UI (`web/src/lib/i18n.ts`, static imports) | flat TS object (`'key': 'value'`), single-quoted |
| Changelog notes | `changelog/<version>/<loc>.md` | in-app update dialog (fetched at runtime via `changelog/index.json`) | markdown |

**The CLI JSON and Web TS sets are content-disjoint (0 shared keys).** Despite older docs, the web UI does **not** read `internal/i18n` — it has its own ~485-key TS files. A string shown in the web UI must be added to `web/src/locales/`, not the JSON.

Rules when changing strings:
- Add/rename/remove a key in **all 8 files** of the relevant asset. Key parity is enforced for both assets: the web UI via `web/src/locales/locales.test.ts` (`missing keys` / `extra keys` tests), the CLI JSON via `internal/cli/i18n_test.go` (`TestLocaleCompleteness`). Note: *value*-completeness (English left untranslated in non-en files) is only tested for the web TS files — for the CLI JSONs, keep values translated by hand.
- **Translate the VALUE, don't leave English.** `locales.test.ts` also has a value-completeness test that fails on any *multi-word* value left identical to `en` (the "key exists but never translated" gap) — single-word loanwords/cognates (Name, Status, Port, Bundle, Namespace…) are allowed; brand/format-string exceptions live in its `IDENTICAL_OK` allowlist.
- Adding a changelog version: create all 8 `changelog/<ver>/<loc>.md` files **with real translations** (don't copy en into the others) and add the `index.json` entry. `changelog(...)` only fetches when `latest > current`.
- To audit drift, evaluate each locale's key→value map and flag values identical to en (a script that strips the TS `import`/`export` and `eval`s the object literal works for both the JSON and TS assets).

## Code Style

### Go
- Standard `gofmt` formatting
- `golangci-lint` v2.11.4 with 20 linters (`.golangci.yml`): dupl, errorlint, gochecknoinits, gocritic, gocyclo, gosec, govet (shadow), ineffassign, misspell (US), modernize, nakedret, nestif, prealloc, revive, staticcheck, testifylint, unconvert, unparam, unused, wrapcheck. **Always run `make tools` before linting to get the pinned CI version** — newer gosec taint analysis catches more G703/G706 false positives than 2.7.2.
- Tabs for indentation (Go standard)
- Custom slog handler (`fsutil.CleanLogHandler`): `2026-04-01T02:58:51Z INFO  Message key=value`

### Web (React/TypeScript)
- Tailwind CSS 4 for styling
- ESLint for linting
- lucide-react for icons

## Key Dependencies

### Go
- **CLI**: spf13/cobra
- **Docker**: docker/docker/client (official SDK) + docker/docker/pkg/stdcopy
- **YAML**: gopkg.in/yaml.v3
- **CLI output**: charmbracelet/lipgloss
- **Testing**: stretchr/testify
- **HTTP**: net/http (stdlib, Go 1.22+ routing)
- **Logging**: log/slog (stdlib)
- **Embed**: embed (stdlib, for web UI + appfiles)
- **SQLite**: modernc.org/sqlite (pure Go, no CGO — desktop mode storage)

### Web UI
- **Framework**: React 19 + TypeScript
- **Build**: Vite
- **Styles**: Tailwind CSS 4
- **Icons**: lucide-react
- **State**: Zustand
- **Testing**: Vitest + Testing Library
- **E2E**: Playwright

## Key Technical Decisions

- **SSE** (not WebSocket) for real-time events
- **Daemon transports**: Unix socket (chmod 0600) for the local CLI; the Web UI over TCP is **not offered in server mode** — the runtime gate (`daemon.webUITCPAllowed`, `bootstrap.go` `startWebUI`) ignores `server.webui.enabled` and binds the listener in server mode only via the `CITECK_SERVER_WEBUI=1` dev/E2E hatch. When the hatch binds it, a localhost bind gets the full API gated by the `X-Citeck-CSRF` header (plus bearer-token auth when `api_auth` is enabled) and non-localhost binds require mTLS (`setupMTLS`). `--no-ui` also force-disables.
- **Security model — why the server Web UI is code-disabled**: the localhost Web UI TCP port serves the **full privileged API** (incl. `citeck exec` into containers ⇒ effectively root on the host). The `X-Citeck-CSRF` header defeats browser-based CSRF but is **not authentication** — any local OS user/process that can reach the port would have full control. So rather than ship it as an opt-in config toggle, the server-mode Web UI is **hard-disabled in code**: `server.webui.enabled: true` in `daemon.yml` is inert, and the only way to bind it in server mode is the explicit `CITECK_SERVER_WEBUI=1` dev/E2E hatch (used by the Playwright e2e in `web/`). `citeck ui` refuses in server mode unless that hatch is set. The infrastructure behind it stays in place but unreachable via config: **API token auth** (`daemon.yml` `api_auth.enabled: true`) gates every `/api/*` TCP request with `Authorization: Bearer <token>` or the session cookie from the `GET /auth/session?token=…` handshake (constant-time compare, HttpOnly SameSite=Strict cookie, 24h TTL), else 401 `AUTH_REQUIRED`; token = `api_auth.token` or auto-generated 32-byte `conf/api-token` (0600); the Unix socket, desktop wrapper, and mTLS-authenticated requests bypass, static UI assets stay public. See `internal/daemon/apiauth.go`.
- **Smart regenerate** via deployment hash comparison (like `docker-compose up`) — unchanged containers keep running
- **Namespace runtime is a single-threaded state machine with signal-queue wake-ups.** One `runtimeLoop` goroutine owns every mutation to `r.apps`, per-app status, namespace status, and persistence. External commands enqueue via `cmdQueue` (typed, coalescing, 500ms back-pressure); workers (pull / start / stop / probe / stats / reconcile / liveness) run off-loop on the dispatcher, post typed Results back on `resultCh`; `applyWorkerResult` applies state-machine transitions under lock. Per-iteration `stepAllApps` walks all non-detached apps driving transitions T1–T33. See the `internal/namespace/runtime.go` package header for the concurrency rules and architecture diagram.
- **3-phase doStart**: I/O outside lock → prepare plans → atomic state commit under lock. Detached apps get STOPPED status in the commit phase (not pulled/started). Stale containers (existing && !reuse) enter as STOPPING+initialSweep+desiredNext=READY_TO_PULL so the state machine drives the recreate; no parallel inline Docker stops.
- **`:snapshot` pre-pull is Update & Start only**: images whose tag contains "snapshot" are pulled from the registry before the hash diff, but only when `refreshImages=true` — threaded `doReloadEx`→`Runtime.Start`/`Regenerate`→`doStart`/`doRegenerate` and set true only by the explicit Update & Start / Force Update action. A config-edit reload and boot auto-start pass `refreshImages=false` and diff against cached digests, recreating only apps whose config actually changed (an app's image string change via the gear still recreates it, since `GetHashInput` includes the image). Deliberate tradeoff: without the gate, a same-tag dev re-push would be silently missed (hash computed from stale local digest matches running container → adoption) — but re-pulling on every reload also meant hidden re-pulls of unrelated apps on a plain config edit. Update & Start pays that cost explicitly; a config edit does not. This gate is **mode-agnostic** (not desktop-only): in server mode too, `citeck reload` (→ `handleReloadNamespace` → `doReload` → `refreshImages=false`) reconciles config only and does NOT re-pull `:snapshot` digests, while `citeck start` / `citeck start --force` (→ `handleStartNamespace` → `updateAndStartAsync` → `refreshImages=true`) is the way to pick up a new same-tag image. Release-tag deployments are unaffected either way (only `:snapshot` images refresh).
- **The Update&Start button ALWAYS posts `/namespace/start`, whatever the namespace status.** It used to pick the endpoint client-side — `primaryAction = isRunning ? 'reload' : 'start'` in `NamespaceControls.tsx`, justified by a comment claiming 1.x "used reload semantics when running". 1.x did no such thing: `NamespaceRuntime.runtimeThreadAction()` handles `StartNsCmd` with **no branch on `nsStatus`** (`v1.3.9`), mapping `forceUpdate` to the git policy only (`REQUIRED` vs `ALLOWED`) and then re-driving every non-detached app. Routing the running case to `/namespace/reload` meant `refreshImages=false`, so the `:snapshot` digest refresh was skipped, every hash was recomputed from the stale local digest and matched, and `doRegenerate` no-op'd on every app — the button did nothing but blip RUNNING→STARTING→RUNNING. `handleStartNamespace` derives Start-vs-Regenerate from the live status server-side, so one endpoint covers both states; do not reintroduce the client-side branch. Pinned by `web/src/components/NamespaceControls.test.tsx`.
- **Update&Start waits for `reloadMu`; it must never be TryLock-dropped.** Every other async reload path may fold into an in-flight reload, but this one may not: that reload nearly always carries `refreshImages=false`, so folding silently discards the digest refresh that *is* the action, while HTTP already answered 200. `updateAndStartAsync` keeps a single-slot queue (`updatePending` / `updatePendingForce` on `Daemon`) — one pass waits on the lock, extra clicks fold into it (it starts after they arrive, so it satisfies them), and the force flag is OR-ed so a Force click can't be downgraded to a throttled pull. It also **re-samples the namespace status after acquiring the lock**, because waiting can outlast the sample and a wrong `startNotRegenerate` makes `Runtime.Start` early-return ("Runtime already running") and lose the command. `updateStartActionFor` maps that status three ways, and the two non-obvious arms are load-bearing: **STARTING → Regenerate** (the loop is alive, so `Start`'s CAS would drop the command — and since the queue lets a click land during an in-flight pass, STARTING is exactly what a second click samples on a bundle that takes minutes to come up), and **STOPPING → skip**. `Start` looks right for STOPPING because it calls `awaitStoppedLoopExit`, but that only waits once `shutdownComplete` is *closed*, and `signalShutdown` fires at the tail of the stop chain — i.e. only when the status is already STOPPED. Through the whole real STOPPING window the channel is open, the wait returns immediately and the CAS fails, so the pass would pay for a git pull whose result nothing consumes. It bails with a Warn instead; the click is refused, not silently absorbed. **Known limitation:** a click during STOPPING is not honored — the user must retry once the namespace has stopped. Pinned by `internal/daemon/update_and_start_queue_test.go`.
- **`NamespaceDto.Updating` covers the pre-runtime stretch so a click is never silent.** Between the accepted `/namespace/start` and the runtime handoff — the `reloadMu` wait, the git pull, the bundle resolve, the file generation — nothing touches namespace or app status by design, so the UI had no way to show the click was being acted on. `Daemon.updateInFlight` is raised **synchronously in the handler**, before the 200 and before the worker goroutine is spawned (raising it afterwards could race a fast pass that clears it first, leaving it stuck on), and lowered in the worker's defer. What actually keeps it correct across chained updates is that the worker **re-asserts `setUpdateInFlight(true)` right after it acquires `reloadMu`**: the raise path and the CAS both run on the *request* goroutine, which never takes `reloadMu`, so holding the lock orders nothing against them and a previous pass can lower the flag in the gap between its own `updatePending` check and a new click's CAS. The re-assert is self-healing and idempotent (`setUpdateInFlight` broadcasts only on a real transition); the end-of-pass `updatePending` check is a best-effort narrowing, not a guarantee. The same "hand your intent to the pass that will run for you" rule covers the other two folded-click fields — `updatePendingForce` and `updatePendingNsID` — both recorded *before* the CAS and consumed *after* `updatePending` is cleared, so no interleaving can strand a force or drop a folding click's target namespace. `setUpdateInFlight` broadcasts a `namespace_updating` SSE event only on a real transition, and the flag also rides in the DTO so a client that connects or reloads mid-update still sees it. It is **not** a namespace status — the state machine owns those, and faking STARTING here would lie about the runtime. The flag is daemon-global but each pass is namespace-pinned, so `updateInFlightNsID` records which namespace it belongs to and `handleGetNamespace` reports `Updating` only when it matches — otherwise switching namespace while a pass sat queued would show "Updating…" on, and disable the button of, a namespace the pass has nothing to do with. On the client the flag is OR-ed with a short-lived local click echo, and **the echo is released by the post-click refetch, not merely by its 30s backstop**: the skip paths and a fast `doReloadEx` error finish in microseconds, after which nothing else would ever clear it and the button would sit spinning for the full timeout.
- **A failed pass is reported, not just logged.** Success and failure used to look identical to the user — the spinner stops and nothing changed — which is the same "my click went nowhere" the Updating flag exists to prevent. `NamespaceDto.UpdateError` + `UpdateErrorAt` carry why the last pass did not happen (a `doReloadEx` error, or the STOPPING refusal), pinned to the target namespace by the same rule as the flag, and cleared the moment a new click is accepted so a stale verdict never sits beside a running update. The web UI raises it on the **same error modal as the synchronous failure path** (`exec`'s catch) — one action, one surface — and de-dupes on `UpdateErrorAt` via a **module-scope** marker, not a ref: the failure stays in the DTO until the next click, so a component-scoped marker would re-raise the modal on every remount. Pinned by `TestUpdateAndStart_ReportsFailureToTheUI` / `TestUpdateAndStart_StoppingRefusalIsReported` / `TestUpdateFailure_IsScopedAndClearedByANewClick` and a vitest case, all mutation-checked.
- **The Update&Start button shows progress by swapping only its ICON, never its label.** Replacing the text resizes the control: RU measures 53px for the wrapped "Обновить и запустить" against 26px for "Обновление…", so the toolbar row jumped on every click; and at the RU width (~95px) "Обновление…" is a single unbreakable word that overflows and clips the spinner, so an overlaid progress label is not an option either. `Play` → spinning `Loader2` plus the disabled styling carries the signal, and `ns.updating` is the tooltip. Verified idle-vs-busy heights identical in all 8 locales. The button ORs the daemon flag with a short-lived local click echo (`CLICK_ECHO_TIMEOUT_MS`) so it reacts on the first frame rather than after an SSE round-trip; the echo is handed off during render (not in an effect — `react-hooks/set-state-in-effect`) and must not touch the timer ref there (`react-hooks/refs`).
- **Start publishes app state BEFORE phase-1 I/O.** `applyCommand` runs inline on the single-threaded `runtimeLoop`, so for the whole of `doStart` there is no `stepAllApps`, no `updateNsStatus` and no `flushEvents` — and `Runtime.Start` has already blanked `r.apps`, which makes the daemon backfill the entire catalog as STOPPED (`appDefsToStoppedApps`). Every service therefore read "Остановлен" until the phase-3 commit, a window dominated by the `:snapshot` pre-pull (concurrency 4, 2-min per-image cap) and reaching tens of seconds on a real bundle. `doStart` now seeds `r.apps` (non-detached → READY_TO_PULL, detached → STOPPED) and calls `flushEvents` before that I/O; `doRegenerate` likewise sets `NsStatusStarting` **before** the pre-pull rather than in its lock phase. Seeded statuses are provisional — the commit phase replaces `r.apps` wholesale — and cannot race the state machine, since the loop is blocked inside `doStart`. `flushEvents` takes `r.mu`, so call it outside the lock. Pinned by `internal/namespace/runtime_start_visibility_test.go`.
- **Graceful shutdown**: phased stop groups (proxy → webapps → keycloak → infra)
- **Detach mode** (binary upgrade): `Runtime.Detach()` exits without stopping containers for zero-downtime binary upgrades; Docker owns containers, not the launcher (same principle as kubelet restarts)
- **Per-app detach**: `citeck stop <app>` marks app as `manualStoppedApps` (desired-state-first, like k8s) and stops container. Detached apps are excluded from start/reload/regenerate, skipped by reconciler and liveness probes, and treated as satisfied in `waitForDeps`. `citeck start <app>` re-attaches. State persisted in `state-{nsID}.json` (server mode; desktop mirrors it in the SQLite `namespaces.state_json` column).
- **Template detachedApps**: workspace `namespaceTemplates[].detachedApps` applied on first start (no persisted state). Install wizard sets `template: "default"` in namespace.yml.
- **Custom workspace links**: workspace-config `links:` (`bundle.WorkspaceLink`: name/url/icon/order/category/description + `dependsOn: [appId]`) surface in the launcher sidebar alongside the built-ins. Flow: `Generate` copies them into `GenResp.CustomLinks` → `runtime.SetCustomLinks` (refreshed each load/reload) → `generateLinks` (in `ToNamespaceDto`, recomputed every SSE tick) gates each by dependency status: **hidden** (omitted from `Links`) when any dep is absent from the namespace (`generatedDefs`), **disabled** (`LinkDto.Disabled`) when a present dep is not RUNNING, enabled otherwise; no deps ⇒ always enabled. Custom links carry `LinkDto.Custom=true`; `Dashboard.tsx` gates them on `!disabled` only (NOT the namespace-wide running flag, unlike built-ins). They are pinned to the bottom of the sidebar (`customLinkOrderBase=1000` + configured order; built-ins span ~ -100..101).
- **`GetHashInput` stability** is a hard compatibility contract across versions — changes require migration
- **Volumes content hash covers directory mounts**: `computeVolumesContentHash` (feeds `GetHashInput`) hashes every bind-mounted file's content so an edited file recreates the container. A mount whose source is a **directory** (e.g. Spring webapps' `./app/<app>/props:/run/java.io/spring-props/`) is expanded via `expandDirMountKeys` to every generated file beneath it — without this, editing `application-launcher.yml` (or any dir-mounted props file) leaves the hash unchanged and the running app never picks the edit up. Restores Kotlin parity (`NsRuntimeFiles.fillFilesForAbsPath`). Two independent editors: `citeck edit <app>` (+`--reset`) edits the **ApplicationDef** (`/apps/{name}/config`); the mounted-**file** editor (`/apps/{name}/files/...`, incl. application-launcher.yml, reset via `/files/reset`) is exposed on the CLI via `citeck edit <app> --file <path>` (and in the web/desktop config editor). `citeck status` marks apps with either kind of edit with a `*` (from `AppDto.Edited` / `EditedFilesCount`).
- **Secrets**: AES-256-GCM per-secret encryption via `SecretService`; the master-password KDF is **Argon2id** for new envelopes (legacy PBKDF2 envelopes stay readable via a stored KDF discriminator — a hard compat contract; server mode auto-unlocks with the default password through the same dispatch); system secrets (JWT, OIDC, admin password, citeck SA) via `resolveOneSystemSecret` pattern
- **Desktop secrets-start gate**: on desktop, a namespace whose images pull from an **auth-required** registry (a `wsCfg.ImageRepos[].AuthType != ""` host — detected by `namespaceNeedsUserSecrets`/`shouldDeferStartForSecrets` in `internal/daemon/secrets_start_gate.go`) is **not auto-started** while the user-secret vault is `encrypted && locked` (custom master password, not yet unlocked). `namespace_loader.go` sets `shouldStart=false` + a transient `deferredForSecrets` flag on the active namespace (NOT persisted STOPPED); `handleUnlockSecrets` snapshots the active-ns fields under `configMu` and starts the deferred runtime after unlock (`rebuildAuthCaches` first). Desktop-only — the gate is behind `config.IsDesktopMode()` and only the START is gated (never system-secret resolution; server mode auto-unlocks at boot so it never triggers). Rationale: without this the namespace auto-started, private-registry pulls failed auth → `pull_auth_required` → `RegistryAuthBanner` auto-opened the credentials dialog (native `<dialog>` top-layer) **over** the master-password unlock modal, a dead end (saving a token → 423 "secrets are locked"). Frontend defense-in-depth: `RegistryAuthBanner` won't open the credentials dialog (auto OR manual button) while `getMigrationStatus().locked`, re-evaluated on `useSecretsLockStore` epoch bump.
- **citeck service account**: single shared SA named `citeck` used in two systems: (1) Keycloak master realm (admin role) for kcadm ops, (2) RabbitMQ (monitoring tag, vhost `/` full perms) for webapp AMQP auth and observer management-API monitoring. One 32-char random password stored as `_citeck_sa` system secret. Used by init script (kcadm.sh), admin password change handler, and webapp→Keycloak integration (`${KK_ADMIN_USER}/${KK_ADMIN_PASSWORD}` template vars + `ECOS_WEBAPP_RABBITMQ_USERNAME/_PASSWORD`). Survives snapshot import because Keycloak and RabbitMQ init actions create/sync the SA on every container start.
- **Admin password**: generated on first server-mode start; seeded into both Keycloak realms (`master` via `KC_BOOTSTRAP_ADMIN_PASSWORD` on empty DB + `ecos-app` realm via init script) and shared with RabbitMQ / PgAdmin admin-UI users. Webapps do NOT use the admin user to connect to RabbitMQ — they use the stable `citeck` SA, so admin-password rotation never requires webapp recreation. Desktop mode always uses "admin". The Keycloak init script never touches the master realm `admin` password on re-run, so rotations done via `citeck setup admin-password` are preserved across container restarts.
- **Admin password change** via `citeck setup admin-password`: rotates Keycloak `master` + `ecos-app` realms, RabbitMQ, and PgAdmin. The SA `citeck` password is stable (launcher uses it for internal Keycloak/RabbitMQ auth and must not lose access). Authenticates as the citeck SA → kcadm.sh set-password for `ecos-app` (fatal on failure), then `master` (also fatal — 2.1.0 policy: leaving the old master-console password live is a security hole; the error tells the user to retry `citeck setup admin-password`), then rabbitmqctl change_password for the RabbitMQ admin user (UI only, best-effort), then setup.py for PgAdmin (best-effort). All runtime, no container restart; **no webapp reload** — webapps use the citeck SA for RabbitMQ and are unaffected by the admin-password change.
- **Email config**: via env vars (`SPRING_MAIL_HOST/PORT/PROTOCOL`, `ECOS_NOTIFICATIONS_EMAIL_FROM_DEFAULT/FIXED`), NOT CloudConfig (disabled in server mode). When email configured, mailhog container is not generated and proxy skips `MAILHOG_TARGET` env.
- **Two storage backends**: flat files (server) / SQLite (desktop); desktop mode via explicit `--desktop` flag (hidden from server binary help)
- **Desktop thin-wrapper**: Wails wrapper supervises the daemon as a child process; daemon→wrapper native verbs over `wrapper.sock`; backend-defined tray (`GET /api/v1/desktop/tray-menu`); capabilities advertised via `CITECK_WRAPPER_CAPS` for forward-compatible feature detection; daemon child detaches on quit (containers keep running — kubelet principle); daemon lifecycle is bound to the wrapper via `CITECK_WRAPPER_PID` — Pdeathsig on Linux, a pid-poll watchdog in the daemon for macOS/Windows — so a hard-killed wrapper takes the daemon down (detaching) instead of orphaning it until the next launch's reap
- **Kotlin 1.x → Go 2.x migration**: pure-Go H2 MVStore reader (no JAR, no JRE download). `internal/h2migrate/mvstore.go` walks chunk headers → layout map → meta map → user-data maps, strips H2 `TransactionStore` `VersionedValue` wrapper (`varLong(operationId) || value`) on user maps, decompresses LZF pages (with extended back-ref byte-order fix). `internal/h2migrate/applicationdef_compat.go` + `bundledef_compat.go` translate Kotlin entity JSON into Go shapes. A one-shot atomic `storage.db → storage.db.kotlin-bak` backup runs before the first migration; `storage.db` itself is opened read-only so Kotlin 1.x can be reinstalled at any time. When the H2 reader cannot open the file at all, a filesystem-fallback safety net emits Kotlin-parity default authentication (BASIC + admin/fet). SQLite schema versions v3 (`Secret.Username`), v4 (per-workspace `SelectedNs` map), v5 (`git_repo_state` table), v6 (`namespaces` table: `config_yaml` + `state_json` on desktop) are applied after import.
  - **`Chunk.len` is a BLOCK COUNT, not a byte length** (`org.h2.mvstore.Chunk`), and H2 emits no `blocks` attribute. Reading it as bytes forced every chunk to a single 4096-byte block, so any page past that offset was silently skipped and the layout map came back without `meta.id` — surfacing as the useless error `layout map missing meta.id entry` on a perfectly healthy store. The chunk FOOTER position is independent on-disk proof: `chunk:19f,len:2` at byte 65536 has its checksum-valid footer at `65536 + 2*4096 - 128`.
  - **Layout and meta reads are STRICT; user-data maps stay lenient.** Layout/meta are the index of everything else, so a partial read there must be a hard error — the original silent `continue` / `return result, nil` paths are exactly why a chunk-read failure masqueraded as a missing key. Partial recovery is still allowed for user maps.
  - `header["H"]` is the MVStore **magic constant 2 in both H2 1.4.x and 2.x** — it is NOT the format version. Gate on the real `format:` field (accept 2/3; 1 gets an actionable error). Both header blocks are fletcher-verified and the higher valid `version:` wins; a torn newest chunk falls back to the newest valid one.
  - **The fallback must never silently zero recoverable data.** `ws/<id>/repo` is a git clone, so `RepoURL` (origin) and `RepoBranch` (HEAD) are recovered from it. `AuthType` is deliberately NOT inferred — an https remote is equally likely public or token-auth, and a wrong `TOKEN` guess fails pulls with a misleading auth error. `SecretID` / `RepoPullPeriod` exist only in `storage.db` and are genuinely lost.
  - **A degraded migration is reported to the UI.** `GET /api/v1/secrets/migration-status` gains `migrationDegraded: true` + a `degradedMigration` object (reason, backupPath, counts, `recoveredRepoUrls`, `lostFields`), rendered by `web/src/components/MigrationDegradedBanner.tsx`. Both keys are **absent** on a clean migration — test presence, not `=== false`. This exists because `hasPendingSecrets` is false *precisely when* the fallback ran (the pending-secrets blob is written only on the H2 path), so before this the user got a launcher that looked freshly installed and no warning anywhere but the log.
- **Reclone safety (`internal/git/repo.go`)**: `doPull` pulls from the on-disk `origin` remote while `doClone` clones from `opts.URL` — nothing structurally ties them together, so a reclone can cleanly and atomically install a **different repository**. Two guards prevent that, and both must stay: (1) **transient network errors are not corruption** — `isTransientNetworkError` (TLS handshake timeout, DNS, refused, reset, deadline, 5xx) returns the error and keeps the existing clone, instead of triggering a destructive re-clone on a one-second network blip; (2) reclone has **two distinct intents** — *re-point* (`repoConfigChanged` true; replacing contents is correct) and *repair* (called from `doPull`'s four failure paths), and the repair path refuses when the on-disk `origin` does not match `opts.URL`, deleting nothing. A missing `.git/citeck-repo-meta.json` sidecar (every clone made by the Kotlin 1.x launcher) falls back to comparing the real origin rather than assuming "unchanged". The `.tmp` + atomic swap was never the problem — the loss was semantic, not a torn write.
- **An unknown bundle repo id is a hard error** (`internal/bundle/resolver.go`). It used to fall through to `DefaultBundlesRepo`, cloning the public GitHub workspace into a directory named after the unknown id — which then armed the reclone hazard above. `syncBundleRepo` now takes `BundlesRepo` **by value** so a "not found" nil cannot reach it structurally. Per-field default inheritance for a *declared* repo (empty URL → default) is a separate, tested feature and must not be broken. The error names the known ids, the effective workspace URL/branch, and — when the configured URL is empty — says so explicitly, since a blanked `repo_url` is the fingerprint of a lossy 1.x migration. `WorkspaceSyncError` is deliberately NOT widened to cover the empty-URL case: that would hard-fail every default-repo sync error and 502 the Welcome endpoints on a fresh or offline box.
- **go-git** (pure Go) for git operations — no external git binary required
- **ACME** profiles via custom JWS; LE works with IPs via shortlived profile (~6 day certs)
- **Desktop self-signed HTTPS**: the namespace create/edit dialog (`NamespaceEditDialog.tsx`) shows an "Enable HTTPS (self-signed)" checkbox, gated on `isDesktopModeSync()` (hidden in the server web UI / e2e). Ticking it sends `tlsEnabled` (+ `tlsMode: "self-signed"` on create); the daemon's `applySelfSignedTLSDefaults` (create + edit routes, `routes_ns.go`) is **desktop-only** — it fills `Proxy.Host="localhost"` when empty (custom host preserved), then `ensureSelfSignedCert` materializes the cert. The **port is NOT persisted as 80/443** — `namespace.EffectiveProxyPort(ProxyProps)` derives the published/advertised port from the TLS scheme, consumed by `generateProxy`'s host-port `AddPort` and `BuildProxyBaseURL`. Derivation is **scoped to LOCAL hosts** (`localhost`/`127.0.0.1`/`::1`/empty): there `{0,80,443}` → 443 when TLS on, else 80 (a custom port like 8443 honored verbatim). So enabling HTTPS renders as `:443` and disabling reverts to `:80` **automatically and symmetrically** — a leftover stored 443 with TLS off self-corrects to 80 on the next reload (this fixed the "HTTP served on 443 → connection refused" bug from an earlier design that bumped the stored port and never reverted it). For a **non-local host the stored port is respected verbatim** (0 → scheme default): those deployments sit behind an external TLS terminator that forwards to whatever listen port the operator set — HTTP on 443 with TLS off (terminated upstream) is a legitimate setup and must NOT be rewritten (pinned by `TestProxyBaseURL_NonLocalHTTPOn443Preserved`). `selfSignedCertHosts` (`acme_certs.go`) issues the cert with loopback SANs `localhost`+`127.0.0.1`+`::1` for a loopback host in desktop mode. Server mode keeps its prior behavior byte-for-byte (single-host cert, no host rewriting). LE is never toggled from edit (`NamespaceEditDto` has no `tlsMode`), so a power-user LE YAML config survives.
- **Reconciler**: exponential backoff retry for failed apps (1m → 10m max); liveness probes with a **~60s tolerance window**. Does NOT touch detached (STOPPED + manualStoppedApps) apps.
- **Liveness tolerance is `(threshold-1) × period`, and the launcher targets ~60s of continuous failure** (`livenessGraceSeconds` / `livenessProbePeriodSeconds` / `livenessFailureThreshold` in `internal/namespace/reconciler.go`; every generated `LivenessProbe` uses the constant, pinned by `TestGeneratorLivenessTolerance`). The counter starts on the FIRST failed probe, so N failures span N-1 periods — hence 7 failures at a 10s cadence, not 6. The old value of 3 (≈20s) was too tight for the JVM apps this launcher runs: a stop-the-world pause does not refuse the connection, it just never answers, so every probe inside the pause burns its 5s timeout and counts as a failure — a `GC.heap_dump` on `citeck_eproc` tripped all 3 in 16s and the container was recreated mid-dump. Restarting a busy-but-alive app is worse than carrying a genuinely dead one for another 40s; the reconciler's crash/OOM path does not wait on probes and still catches real deaths. The **cadence** stays an operator knob (`daemon.yml` `reconciler.livenessPeriod`, default 10000 — `DefaultReconcilerConfig` used to say 30s while `periodForProbe` fell back to 10s, and since the struct is only wired in when a `reconciler:` section exists, setting an unrelated key like `reconciler.interval` silently tripled the window); the **tolerance** does not, so generated probes deliberately leave `PeriodSeconds` unset.
- **Snapshot import**: CLI normalizes .zip suffix, validates existence via list endpoint BEFORE stopping namespace. Server validates name/file before lock acquisition. Event types: `snapshot_complete`/`snapshot_error` for both export and import.
- **install.sh** is a thin bootstrap (~260 lines): fetch + SHA256 verify + exec. All lifecycle logic (install/upgrade/rollback/SIGKILL preserve/systemd drop-in) lives in Go (`internal/cli/installer_lifecycle.go`)
- **Install wizard**: prefers `systemctl start citeck` when systemd service is installed; falls back to `forkDaemon()` when systemd unavailable
- **CloudConfigServer** skipped in server mode (webapps disable it via env)
- **Memory**: Community needs 16GB RAM minimum; Enterprise (24 apps) needs 24–32GB. On 16GB, detach non-essential apps (`citeck stop onlyoffice attorneys ai edi`) to free 4–5GB.

## CLI Conventions

- `stop <app>` detaches (persists across restart); `stop` (no args) stops all but doesn't detach
- `start <app>` re-attaches; `start` (no args) starts all non-detached
- `--detach`/`-d` for async on all commands (start, stop, reload) — don't wait, return immediately
- `--force` for destructive ops (clean); dry-run by default
- `--format json` for scripting on any command
- `--yes` to skip confirmations
- Hidden flags: `--desktop`, `--no-ui`, `_daemon` (internal)
- `edit <app>` opens the app's effective ApplicationDef in `$EDITOR` and saves the change as a per-app override patch (like `kubectl edit`); `--reset` drops the override, `--from <file|->` pipes YAML from a file/stdin. Overrides persist in `state.EditedAppPatches` (server state / desktop SQLite) and recreate the container. Mirrors the desktop gear-icon config editor.
- `edit <app> --file <path>` edits a **mounted config file** instead (e.g. `app/<app>/props/application-launcher.yml`): stored as a delta over the generated content (`state.EditedFileEdits`) and applied via a reload (`--no-apply` to skip). `--reset` restores that file to the generated default; `--from <file|->` sets it non-interactively; `--list-files` lists an app's editable mounted files. Reuses the same daemon `/apps/{name}/files/...` endpoints as the web/desktop editor.

## Agent Testing Guide (server-side)

Constraints and tactics for automated server-side testing on a 16GB box. Read before running lifecycle tests.

### Memory management (critical)

Enterprise bundle needs ~15GB for 24 Java apps. On 16GB server any extra container (S3Mock, MailPit, Playwright browser) triggers OOM → SSH hangs 10-20 min → recovery.

**Always detach non-essential apps BEFORE adding test infrastructure:**

```bash
# Frees ~4-5GB on enterprise
for app in onlyoffice attorneys ecom service-desk ecos-project-tracker ai edi; do
  citeck stop $app 2>/dev/null
done
# Verify: free -h → 5GB+ available
```

If SSH becomes unresponsive: wait 5 minutes (OOM killer finishes), don't retry aggressively. `systemctl stop citeck` + kill orphan containers to recover.

### Startup timing

- **Infrastructure** (postgres, mongo, rabbitmq, zookeeper): 30s–1m
- **Keycloak**: 1–2 min (5–10 min on fresh DB — realm import)
- **Webapps**: 5–15 min (Java startup + startup probes 90×10s)
- **Enterprise full startup on 16GB**: 10–15 min
- **`citeck reload`** on enterprise: 2–5 min

Don't poll more frequently than 30s. Use `sleep 120 && check` in background tasks.

### Docker network gotchas

- **Server mode**: only proxy publishes ports. `docker port <container>` returns empty for webapps.
- **Internal access**: use `docker inspect <name> -f "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"` to get container IP, then curl on its `SERVER_PORT` env (17022, 17026, 17027, etc. — varies per app).
- **Port numbers are not stable** — read from `docker inspect <name> --format "{{json .Config.Env}}" | grep SERVER_PORT` each time.

### Keycloak and auth

- **citeck SA** is the service account used for all launcher→Keycloak ops and webapp→RabbitMQ AMQP auth. Password in the launcher_state plain key `_sys_citeck_sa` (see `internal/daemon/system_secrets.go`). Survives snapshot import. In Keycloak it has the master `admin` role; in RabbitMQ it has `monitoring` tag + vhost `/` full permissions.
- **Admin bootstrap password**: `docker exec citeck_keycloak_default printenv KC_BOOTSTRAP_ADMIN_PASSWORD` — one-time bootstrap for master admin, not the current password after snapshot restore.
- **OIDC client secret**: `docker exec citeck_keycloak_default /opt/keycloak/bin/kcadm.sh ...` to fetch (see `internal/namespace/generator_keycloak.go`, which renders the init script from `internal/appfiles/embedded/keycloak/init.sh.tmpl`).
- **Gateway access**: OIDC token via `/realms/ecos-app/protocol/openid-connect/token` + `Authorization: Bearer <token>`.
- **Webapp direct access**: JWT HMAC-SHA256 with `ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET`. Not straightforward — prefer gateway API when possible.

### S3 testing

- **Fake-S3** (`lphoward/fake-s3`): NOT compatible with MinIO SDK used by ecos-content. Fails on `?location=` request. Avoid.
- **S3Mock** (`adobe/s3mock`): works, but `initialBuckets` env var doesn't create buckets automatically. Create manually: `curl -X PUT http://s3mock:9090/ecos-content`.
- **MinIO**: works but ~300MB RAM (too heavy for 16GB enterprise).
- **Content upload via ECOS**: `POST /gateway/emodel/api/ecos/webapp/content` with `multipart/form-data` (file, name, dir). Returns `{"entityRef":"emodel/temp-file@..."}`.
- **Set default S3 storage**: mutate `eapps/config@app/emodel$default-content-storage` with `_value?json:{"ref":"content/storage@content-storage-s3"}`.

### Background task polling

Don't poll background SSH commands with `cat output.file` every second — wastes context. Instead:
- Start the command with `run_in_background: true` (notification on completion)
- Include `sleep N && check` in the command itself
- Use short timeouts (5–15s) for direct queries when checking state

### Common test scripts (server-side)

```bash
# Quick status
./scripts/dev/ssh.sh 'citeck status 2>/dev/null | grep -c "RUNNING"'
# Use grep -c "RUNNING" NOT -cw RUNNING — ANSI colors break word boundary

# Memory check
./scripts/dev/ssh.sh 'free -h | grep Mem'

# Emergency stop all containers (recover from OOM)
./scripts/dev/ssh.sh 'docker kill $(docker ps -q); citeck stop --shutdown'
```

## TUI Testing

TUI commands (`install`, `setup`, `migrate`, etc.) are tested via tmux — it provides
screen capture as plain text and accepts key injection without modifying the binary.

```bash
# Start a session with fixed dimensions
tmux new-session -d -s tui-test -x 120 -y 40

# Launch a TUI command
tmux send-keys -t tui-test "./dist/bin/citeck install" Enter
sleep 1

# Read current screen as plain text (no ANSI codes)
tmux capture-pane -t tui-test -p

# Send keys
tmux send-keys -t tui-test Down        # arrow down
tmux send-keys -t tui-test Up          # arrow up
tmux send-keys -t tui-test "" Enter   # confirm (C-m, not literal Enter)
tmux send-keys -t tui-test "sometext" # type text

# Tear down
tmux kill-session -t tui-test
```

**Autonomous test loop:** `make build-fast` → start tmux session → capture screen →
analyze text → send keys → capture → analyze → fix code if broken → rebuild → repeat.

Cover all interaction branches: happy path, validation errors, back navigation,
cancellation, edge inputs (empty, too long, invalid chars). Fix any regressions before
moving on.

### Visual screenshots via Playwright

To verify colors, layout, and styling — not just text content:

```bash
# 1. Capture screen with ANSI escape codes and convert to HTML
tmux capture-pane -t tui-test -p -e | aha --no-header > /tmp/tui-screen.html

# 2. Wrap in a styled page (dark terminal background, monospace font)
cat > /tmp/tui-preview.html << 'EOF'
<!DOCTYPE html><html><head><meta charset="utf-8"><style>
body { margin: 0; background: #1e1e2e; padding: 24px; }
pre { font-family: 'JetBrains Mono', monospace; font-size: 14px;
      line-height: 1.5; color: #cdd6f4; background: #1e1e2e;
      margin: 0; white-space: pre-wrap; }
</style></head><body><pre>CONTENT_HERE</pre></body></html>
EOF
# (replace CONTENT_HERE with the inner content of /tmp/tui-screen.html)

# 3. Serve locally and screenshot with Playwright
python3 -m http.server 18888 &
# then: browser_navigate http://localhost:18888/tui-preview.html → browser_take_screenshot
```

**When to use visual screenshots:** checking active-item highlight color, border/glyph
rendering, truncation, layout regressions. Plain text capture is sufficient for logic
and navigation tests.

**Why not vhs:** vhs requires `ttyd` + `ffmpeg` (~200 MB) and only records fixed
scenarios — no mid-session inspection. The tmux approach allows reading screen state
at each step and reacting programmatically.

## CI/CD

> **Before every release (and ideally before any push to master): run `make check`.**
> It is a local superset of the test.yml gate below (same steps + web eslint),
> so green locally means green in the release pipeline. This is not optional for
> releases — because plain pushes to master skip CI (see below), the `v*.*.*`
> release tag is otherwise the *first* time linters/gates run, and a failure
> there fails the release after the tag is already public. `gofmt` + `go vet` +
> `go test` alone are NOT sufficient (they pass while golangci-lint / coverage /
> govulncheck / deadcode / audit can fail). Prereqs: `make tools` once, plus
> CGO + GTK3 dev headers for the deadcode gate.

GitHub Actions (three workflows):
- **Test suite** (`.github/workflows/test.yml`, reusable `workflow_call` — never triggered on its own): `go vet`, `golangci-lint v2.11.4`, `go test -race ./internal/...`, `pnpm vitest run`, full server build, linux/arm64 cross-compile check, plus the contract gates added in 2.5.x: per-package coverage floors (`scripts/ci/coverage-floor.sh`), reachable-vuln scan (`scripts/ci/govulncheck.sh`, triaged via `scripts/ci/govulncheck-allowlist.txt`), dead-code check (`scripts/ci/deadcode.sh`, built with `-tags desktop,gtk3`), and `pnpm audit --prod --audit-level high` (triage advisories via `web/package.json` → `pnpm.auditConfig.ignoreCves` / `ignoreGhsas`). Single source of truth for "is this commit good?". **`make check` mirrors this job step-for-step** (plus eslint) for local runs.
- **CI workflow** (`.github/workflows/ci.yml`): a thin trigger shim that just calls test.yml. Runs ONLY on `pull_request` → master and `push` → `release/**` branches — **plain pushes to master do NOT run CI** (the tag-time run in release-go.yml is the gate for what gets published).
- **Release workflow** (`.github/workflows/release-go.yml`): triggered by `v*.*.*` tags; runs test.yml as a gate, then builds `linux/{amd64,arm64}` server binaries (matrix build) + desktop installers and publishes the GitHub release directly (`draft: false`). Uses `go-version-file: go.mod`. Contains a MANDATORY release-signing step: signature verification is active (`internal/update/signature.go` embeds a real key), so the step fails the build if `RELEASE_SIGNING_KEY` is not configured rather than shipping an unsigned release that bricks auto-update.
- **Linting**: `.golangci.yml` v2 format, 20 linters, G104 excluded (cleanup errors), test files relaxed for dupl/gosec/unparam.
