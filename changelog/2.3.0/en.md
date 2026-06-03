# 2.3.0
## Fixes

- Daemon no longer hammers Let's Encrypt on restart / reload while a
  rate-limit marker is active. `ensureACMECert` now checks
  `acme.IsRateLimited` before calling `ObtainCertificate`, preventing
  amplification of the backoff window across daemon bounces.
- `citeck restart <app>` during namespace shutdown can no longer race
  into a deadlock. A second NS-STOPPING recheck under the re-acquired
  lock rejects the restart cleanly instead of routing the app back to
  `READY_TO_PULL` mid-shutdown (which would wedge the continuation
  chain and hang `Shutdown()`).
- Daemon reload no longer writes raw embedded templates before
  `Generate()` renders them with real secrets. A failed reload no
  longer leaves bind-mount files containing unrendered placeholders.
- Retry-loop timers in pull / start / stop / probe workers use
  `time.NewTimer(d).Stop()` instead of `time.After(d)`. Cancelation no
  longer leaks unfired timers (observable under heavy liveness thrash
  or pull retries).

## Security

- Go toolchain bumped **1.26.1 → 1.26.2** — includes patches for five
  stdlib CVEs reached by the daemon (`crypto/x509` verify paths,
  `crypto/tls` TLS 1.3 KeyUpdate DoS, `archive/tar` unbounded alloc).
- `modernc.org/sqlite` bumped **v1.48.1 → v1.48.2** — eliminates the
  pre-update-hook ABI uncertainty.
- Master-password import handler (`/api/v1/secrets/submit-master-password`)
  now caps the request body via `http.MaxBytesReader` and returns a
  clean 413 on oversized input, matching the other mutation handlers.

## Web UI

- `FormDialog` and `JournalDialog` fully i18n — "Cancel", "Submit",
  "Working...", "Close", "Select", "Filter...", "No matching rows",
  row-count labels, and the required-field validation message now
  translate across all 8 locales. Added `form.fieldRequired`,
  `common.submit`, `common.select`, and six `journal.*` keys.
- Reconnect toast (`'Connection restored, state refreshed'`) now
  translated via `store.connectionRestored`.
- Daemon logs streaming no longer duplicates the replayed tail on
  TLS / slow-network connections. The initial buffer settles with a
  400 ms idle window before switching to delta append.
- Dashboard and App layout now use **selector-based Zustand
  subscriptions**. Events that mutate only internal store fields
  (`reconnectDelay`, `lastSeq`, `stream`) no longer cascade a re-render
  through the entire app tree.
- Dashboard migration-check dialog resets when the daemon drops and
  returns, so a daemon restart re-fires the check instead of silently
  skipping it.
- SSE reconnect: generation counter bumps **before** closing the
  previous stream. A stale `onClose` callback from the prior stream
  can no longer double-schedule a reconnect.
- `RightDrawer` and `JournalDialog` close buttons now carry
  `aria-label` for screen readers.

## Docs

- `CLAUDE.md` architecture table reflects reality: `internal/actions/`
  removed, `internal/i18n/` + `internal/desktop/` added,
  `internal/namespace/nsactions/` description corrected (it's now a
  constants + helpers stub, not an executor framework).
- `CLAUDE.md` top-level description: embedded Web UI is served over
  TCP in server mode (previously said "disabled in server mode");
  `i18n.ts` bundles all 8 locales synchronously (previously said "lazy
  loading"); binary size corrected to ~24 MB; release workflow matrix
  corrected to `linux/{amd64,arm64}` with `draft: false`; reconciler
  backoff ceiling corrected to 10 m.
- `README.md` + `README.ru.md`: `citeck restart [-d|--detach]` (stale
  `--wait` flag removed), `citeck stop [app...]` (multi-app signature),
  added `citeck dump-system-info` row to the CLI reference table.
- `CHANGELOG.md` historical corrections: 2.1.0 build matrix
  (`darwin/*` was never shipped), 2.0.0 command list (no `self-update`
  / `webui cert` ever existed — replaced with real names), 2.2.0
  "Compatibility" section dropped.

## Internal cleanup

- **`internal/actions` framework removed** — the state-machine rewrite
  in 2.2.0 made it dead code. Every `Runtime` was spawning a 20-worker
  pool + stall watcher (**21 goroutines per namespace**) that never
  processed a single action. Pull/Start/Stop actually run directly
  through `runtime_workers.go` on the dispatcher. `nsactions.*Executor`
  types also dropped; `NewRuntimeWithActions` collapsed into
  `NewRuntime`.
- **Dead helpers deleted**: `appfiles.ExtractTo` + tests (180 LOC),
  `DaemonClient.{ListBundles, DeleteSecret, GetConfig, PutConfig}`,
  `config.ResolveBundlesDir`, `RuntimeClient.WaitForContainer` (only
  `_Exit` variant is live), `DaemonStatus.IsReady`, `cli.Yes`,
  `prompt.NewOption[T]`, `Runtime.appWg` (plus three vacuous `Wait()`
  call sites), `RecordOpts` variadic on `OperationHistory.Record`, five
  unused `runtime_testutil` `With*` options + `InjectResult`, two dead
  web components (`QuickLinks`, `StatsBar`), and 72 orphan locale
  entries across 8 files.
- **Test stability**: six `time.Sleep`-based negative assertions
  (phase4b/4c/5a/5c/7c) converted to `assert.Never` polling. Fails
  fast the moment a state transition happens, robust on slow CI.
  `TestDetachSkipsPostDetachStepDispatches` no longer leaks a
  `dispatchLoop` goroutine.
- Net diff for the 2.3.0 release: **−1263 lines of code**.
